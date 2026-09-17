package diagnostics_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gastonlarap-a11y/code-flow/backend/diagnostics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readLog(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}

func TestRedact(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"leaves an empty string alone": {"", ""},
		"leaves ordinary text alone": {
			"fatal: repository 'https://github.com/gastonlarap-a11y/code-flow.git' not found",
			"fatal: repository 'https://github.com/gastonlarap-a11y/code-flow.git' not found",
		},
		"credentials embedded in a URL": {
			"remote: https://gaston:ghp_secretvalue@github.com/x/y.git failed",
			"remote: https://***:***@github.com/x/y.git failed",
		},
		// The header pattern cannot start its value on a quote, so in JSON — where the value is
		// always quoted — it does not match at all and the bare-bearer pattern is what catches
		// the secret. That is the reason there are two patterns rather than one.
		"an auth header inside JSON is caught by the bearer pattern": {
			`{"headers":{"Authorization":"Bearer abcdefghijklmnop"},"status":401}`,
			`{"headers":{"Authorization":"Bearer ***"},"status":401}`,
		},
		"an unquoted auth header keeps its name and loses its value": {
			"Authorization: Basic dXNlcjpwYXNzd29yZA== and the rest",
			"Authorization: *** and the rest",
		},
		"an auth header is matched case-insensitively": {
			"authorization: token abcdefghijklmnop",
			"authorization: ***",
		},
		"x-api-key is a header too": {
			"x-api-key: 0123456789abcdef and the rest",
			"x-api-key: *** and the rest",
		},
		"a bare bearer without a header name": {
			"the server rejected Bearer abcdefghijkl",
			"the server rejected Bearer ***",
		},
		"a bearer shorter than eight characters is not a token": {
			"Bearer short",
			"Bearer short",
		},
		"a github personal access token": {
			"token ghp_0123456789abcdefghij is invalid",
			"token *** is invalid",
		},
		"a fine-grained github token": {
			"github_pat_0123456789abcdefghijkl rejected",
			"*** rejected",
		},
		"a slack token": {
			"xoxb-0123456789-abcdef rejected",
			"*** rejected",
		},
		"an openai key": {
			"sk-0123456789abcdefghijklmn rejected",
			"*** rejected",
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, diagnostics.Redact(tc.in))
		})
	}
}

// The header pattern has to run before the bare-bearer one. Reversed, "Authorization: Bearer x"
// would lose its value to the second pattern and then no longer match the first, leaving the
// header shape half-redacted. This asserts the composed result, which is what the file gets.
func TestRedactAppliesTheHeaderPatternBeforeTheBareBearer(t *testing.T) {
	got := diagnostics.Redact("Authorization: Bearer ghp_0123456789abcdefghij")

	assert.Equal(t, "Authorization: ***", got)
	assert.NotContains(t, got, "Bearer ***", "the header pattern must have consumed the value")
}

// Nothing that looked like a credential may survive, even in a line made of several of them.
func TestRedactHandlesSeveralSecretsInOneLine(t *testing.T) {
	got := diagnostics.Redact(
		"push to https://u:p@host/r failed, Authorization: Bearer abcdefghijkl, token ghp_0123456789abcdefghij",
	)

	assert.NotContains(t, got, "u:p@")
	assert.NotContains(t, got, "abcdefghijkl")
	assert.NotContains(t, got, "ghp_0123456789abcdefghij")
}

func TestErrorLogLineShape(t *testing.T) {
	dir := t.TempDir()
	log := diagnostics.NewErrorLog(dir)

	log.Record("get_status", errors.New("CHECKOUT_CONFLICT: your local changes would be overwritten"))

	line := strings.TrimRight(readLog(t, filepath.Join(dir, "errors.log")), "\n")
	assert.Regexp(t,
		regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} [+-]\d{2}:\d{2} {2}get_status {2}errorString: CHECKOUT_CONFLICT: your local changes would be overwritten$`),
		line,
	)
}

// The sentinel has to reach the log intact for the same reason it has to reach the renderer
// intact: a support session reads this file to find out which branch the code took.
func TestErrorLogRedactsButKeepsTheSentinel(t *testing.T) {
	dir := t.TempDir()
	log := diagnostics.NewErrorLog(dir)

	log.Record("clone_repo", errors.New("CREDENTIAL_REFUSED: https://u:ghp_0123456789abcdefghij@github.com refused"))

	line := readLog(t, filepath.Join(dir, "errors.log"))
	assert.Contains(t, line, "CREDENTIAL_REFUSED: ")
	assert.Contains(t, line, "***")
	assert.NotContains(t, line, "ghp_0123456789abcdefghij")
}

func TestErrorLogNamesTheDynamicType(t *testing.T) {
	dir := t.TempDir()
	log := diagnostics.NewErrorLog(dir)

	log.Record("send", fmt.Errorf("outer: %w", errors.New("inner")))

	assert.Contains(t, readLog(t, filepath.Join(dir, "errors.log")), "  send  wrapError: outer: inner")
}

func TestErrorLogIgnoresANilError(t *testing.T) {
	dir := t.TempDir()

	diagnostics.NewErrorLog(dir).Record("noop", nil)

	_, err := os.Stat(filepath.Join(dir, "errors.log"))
	assert.True(t, os.IsNotExist(err), "a nil error must not create the file")
}

// A directory that cannot be created is the case this whole package is built around: it must not
// turn a recoverable situation into a crash.
func TestWritingNeverFailsTheCaller(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "a-file")
	require.NoError(t, os.WriteFile(blocked, []byte("not a directory"), 0o644))

	assert.NotPanics(t, func() {
		diagnostics.NewErrorLog(blocked).Record("m", errors.New("x"))
		diagnostics.NewShellLog(blocked).Info("x")
	})
}

func TestRolloverHappensPastTwoMebibytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "errors.log")
	require.NoError(t, os.WriteFile(path, make([]byte, 2*1024*1024+1), 0o644))

	diagnostics.NewErrorLog(dir).Record("after_rollover", errors.New("fresh"))

	rolled, err := os.Stat(path + ".1")
	require.NoError(t, err, "the oversized file must have been moved aside")
	assert.EqualValues(t, 2*1024*1024+1, rolled.Size())
	assert.Contains(t, readLog(t, path), "fresh")
	assert.NotContains(t, readLog(t, path), "\x00")
}

// "> maxBytes", not ">=": a file sitting exactly at the limit is left alone. Both 2.x
// implementations made that test and a file that rolls one byte early would diverge from them.
func TestRolloverLeavesAFileSittingExactlyAtTheLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "errors.log")
	require.NoError(t, os.WriteFile(path, make([]byte, 2*1024*1024), 0o644))

	diagnostics.NewErrorLog(dir).Record("m", errors.New("appended"))

	_, err := os.Stat(path + ".1")
	assert.True(t, os.IsNotExist(err), "a file at exactly the limit must not roll over")
}

// The second rollover has to overwrite the first .1, not fail on it.
func TestRolloverOverwritesAnExistingSibling(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "errors.log")
	require.NoError(t, os.WriteFile(path+".1", []byte("older"), 0o644))
	require.NoError(t, os.WriteFile(path, make([]byte, 2*1024*1024+1), 0o644))

	diagnostics.NewErrorLog(dir).Record("m", errors.New("newest"))

	rolled, err := os.Stat(path + ".1")
	require.NoError(t, err)
	assert.EqualValues(t, 2*1024*1024+1, rolled.Size(), "the previous .1 must have been replaced")
}

func TestFormatShellLinePadsTheLevelToFive(t *testing.T) {
	for _, tc := range []struct {
		level diagnostics.Level
		want  string
	}{
		{diagnostics.LevelInfo, "2026-09-17T13:05:30.000Z  INFO   hello"},
		{diagnostics.LevelWarn, "2026-09-17T13:05:30.000Z  WARN   hello"},
		{diagnostics.LevelError, "2026-09-17T13:05:30.000Z  ERROR  hello"},
	} {
		t.Run(string(tc.level), func(t *testing.T) {
			assert.Equal(t, tc.want, diagnostics.FormatShellLine("2026-09-17T13:05:30.000Z", tc.level, "hello"))
		})
	}
}

func TestFormatShellLineRedacts(t *testing.T) {
	got := diagnostics.FormatShellLine("2026-09-17T13:05:30.000Z", diagnostics.LevelError, "Bearer abcdefghijkl rejected")

	assert.Equal(t, "2026-09-17T13:05:30.000Z  ERROR  Bearer *** rejected", got)
}

func TestShellLogWritesTheQuitReason(t *testing.T) {
	dir := t.TempDir()

	diagnostics.NewShellLog(dir).Info("[shell] quitting: the tray's Quit item")

	line := readLog(t, filepath.Join(dir, "shell.log"))
	assert.Contains(t, line, "INFO   [shell] quitting: the tray's Quit item")
	assert.Regexp(t, regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z {2}`), line)
}

func TestStartupLogLineShape(t *testing.T) {
	dir := t.TempDir()

	diagnostics.NewStartupLog(dir).Record("storage", errors.New("database is locked"))

	line := strings.TrimRight(readLog(t, filepath.Join(dir, "startup.log")), "\n")
	assert.Regexp(t,
		regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} [+-]\d{2}:\d{2} {2}startup/storage {2}database is locked$`),
		line,
	)
}

// The wrapped chain is what says which of four things called "open" actually failed.
func TestStartupLogWritesTheWholeChain(t *testing.T) {
	dir := t.TempDir()
	err := fmt.Errorf("stage storage: %w", fmt.Errorf("open database: %w", errors.New("permission denied")))

	diagnostics.NewStartupLog(dir).Record("storage", err)

	assert.Contains(t, readLog(t, filepath.Join(dir, "startup.log")),
		"stage storage: open database: permission denied")
}

// The stage that creates {base}/logs is one of the stages this log reports on, so "the directory
// does not exist" is an ordinary case, not an edge one.
func TestStartupLogFallsBackToTempWhenItsDirectoryIsUnusable(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "a-file")
	require.NoError(t, os.WriteFile(blocked, []byte("not a directory"), 0o644))
	fallback := filepath.Join(os.TempDir(), "startup.log")
	before := int64(-1)
	if info, err := os.Stat(fallback); err == nil {
		before = info.Size()
	}

	diagnostics.NewStartupLog(blocked).Record("directories", errors.New("cannot create the base directory"))

	info, err := os.Stat(fallback)
	require.NoError(t, err, "the line must have landed in the temp directory")
	assert.Greater(t, info.Size(), before)
}
