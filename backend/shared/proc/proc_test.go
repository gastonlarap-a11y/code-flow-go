package proc_test

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"testing/iotest"
	"time"
	"unicode/utf8"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/proc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnvironmentInheritsUnchangedWhenNoPathIsGiven(t *testing.T) {
	assert.Equal(t, os.Environ(), proc.Environment(nil))
	assert.Equal(t, os.Environ(), proc.Environment([]string{}))
}

func TestEnvironmentReplacesPath(t *testing.T) {
	t.Setenv("PATH", "/original/bin")

	env := proc.Environment([]string{"/opt/homebrew/bin", "/usr/bin"})

	sep := string(os.PathListSeparator)
	assert.Contains(t, env, "PATH=/opt/homebrew/bin"+sep+"/usr/bin")
	for _, entry := range env {
		assert.NotEqual(t, "PATH=/original/bin", entry, "the inherited PATH must be gone, not shadowed")
	}
}

// SEC-007. The signature is the enforcement — there is no parameter that could carry a token — and
// this is the assertion that nothing sneaks one in some other way.
func TestEnvironmentAddsNothingOfItsOwn(t *testing.T) {
	t.Setenv("CODEFLOW_TEST_MARKER", "inherited")

	before := map[string]bool{}
	for _, entry := range os.Environ() {
		before[entry] = true
	}

	for _, entry := range proc.Environment([]string{"/usr/bin"}) {
		if strings.HasPrefix(entry, "PATH=") {
			continue
		}
		assert.True(t, before[entry], "proc.Environment introduced %q", entry)
	}
}

func TestEnvironmentAddsPathWhenTheParentHasNone(t *testing.T) {
	// A parent with no PATH at all is what a launchd-started GUI app can look like before
	// ApplyLoginShellPath has run.
	env := proc.Environment([]string{"/usr/bin"})

	found := 0
	for _, entry := range env {
		if strings.HasPrefix(entry, "PATH=") {
			found++
		}
	}
	assert.Equal(t, 1, found, "exactly one PATH, whatever the parent had")
}

func TestDecodeLossyUTF8(t *testing.T) {
	for name, tc := range map[string]struct {
		in   []byte
		want string
	}{
		"empty":           {[]byte{}, ""},
		"plain ascii":     {[]byte("hello"), "hello"},
		"valid multibyte": {[]byte("café ✓ 日本語"), "café ✓ 日本語"},
		"a lone 0xff":     {[]byte{0xff}, "�"},
		"invalid in text": {append([]byte("ok"), 0xff, 'x'), "ok�x"},
		"truncated rune":  {[]byte{0xe6, 0x97}, "��"},
		"several invalid": {[]byte{0xff, 0xfe}, "��"},
	} {
		t.Run(name, func(t *testing.T) {
			got := proc.DecodeLossyUTF8(tc.in)
			assert.Equal(t, tc.want, got)
			// The point of the function: whatever came in, what comes out can be marshalled to
			// JSON and reach the renderer.
			assert.True(t, utf8.ValidString(got), "the result must always be valid UTF-8")
		})
	}
}

func TestCommandRunsAndReportsItsOutput(t *testing.T) {
	name, args := shellCommand("echo codeflow")

	out, err := proc.Command(t.Context(), name, args...).Output()

	require.NoError(t, err)
	assert.Equal(t, "codeflow", strings.TrimSpace(proc.DecodeLossyUTF8(out)))
}

func TestCommandSurfacesANonZeroExit(t *testing.T) {
	name, args := shellCommand("exit 3")

	err := proc.Command(t.Context(), name, args...).Run()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "exit status 3")
}

// "Stop this run" arrives whenever the user clicks, which includes before the process started and
// after it finished on its own.
func TestKillTreeIsSafeOnAProcessThatIsNotRunning(t *testing.T) {
	name, args := shellCommand("exit 0")

	never := proc.Command(t.Context(), name, args...)
	assert.NoError(t, never.KillTree(), "never started")

	finished := proc.Command(t.Context(), name, args...)
	require.NoError(t, finished.Run())
	assert.NoError(t, finished.KillTree(), "already exited")
}

func TestKillTreeStopsALongRunningChild(t *testing.T) {
	name, args := shellCommand(sleepScript(30))
	cmd := proc.Command(t.Context(), name, args...)
	require.NoError(t, cmd.Start())

	require.NoError(t, cmd.KillTree())

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the process outlived KillTree")
	}
}

// ---- line splitting -----------------------------------------------------------------------------

func collect(seq func(func(string) bool)) []string {
	out := make([]string, 0, 8)
	for line := range seq {
		out = append(out, line)
	}
	return out
}

// The two splitters have different jobs on purpose: one reassembles output for a parser, the other
// feeds a scrolling progress log.
func TestLineSplitting(t *testing.T) {
	// git redraws one line in place while it downloads, with no newline until the phase ends.
	const gitProgress = "Receiving objects:   1%\rReceiving objects:  52%\rReceiving objects: 100%\n"

	t.Run("Lines keeps a bare carriage return inside the line", func(t *testing.T) {
		assert.Equal(t,
			[]string{"Receiving objects:   1%\rReceiving objects:  52%\rReceiving objects: 100%"},
			collect(proc.Lines(strings.NewReader(gitProgress))),
			"a parser must not have line breaks invented inside its data")
	})

	t.Run("ProgressLines breaks on it", func(t *testing.T) {
		assert.Equal(t,
			[]string{"Receiving objects:   1%", "Receiving objects:  52%", "Receiving objects: 100%"},
			collect(proc.ProgressLines(strings.NewReader(gitProgress))),
			"a minute of download must not arrive as one line at the end")
	})

	t.Run("both treat CRLF as one terminator", func(t *testing.T) {
		for name, seq := range map[string]func(func(string) bool){
			"Lines":         proc.Lines(strings.NewReader("one\r\ntwo\r\n")),
			"ProgressLines": proc.ProgressLines(strings.NewReader("one\r\ntwo\r\n")),
		} {
			t.Run(name, func(t *testing.T) {
				assert.Equal(t, []string{"one", "two"}, collect(seq))
			})
		}
	})

	t.Run("ProgressLines yields the last line without a terminator", func(t *testing.T) {
		assert.Equal(t, []string{"done", "no newline here"},
			collect(proc.ProgressLines(strings.NewReader("done\rno newline here"))))
	})

	// The case a naive splitter gets wrong: a \r arriving at the edge of a read cannot be decided
	// until the next byte is known, or "one\r\ntwo" splits into three.
	t.Run("a carriage return split across reads is still one terminator", func(t *testing.T) {
		assert.Equal(t, []string{"one", "two"},
			collect(proc.ProgressLines(iotest.OneByteReader(strings.NewReader("one\r\ntwo")))))
	})
}

func shellCommand(script string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/C", script}
	}
	return "/bin/sh", []string{"-c", script}
}

func sleepScript(seconds int) string {
	if runtime.GOOS == "windows" {
		return "ping -n " + itoa(seconds) + " 127.0.0.1 >NUL"
	}
	return "sleep " + itoa(seconds)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
