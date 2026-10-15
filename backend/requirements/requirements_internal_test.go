package requirements

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAVersionIsReadFromWhatEachProgramPrints(t *testing.T) {
	tests := []struct{ output, want string }{
		{"git version 2.50.1 (Apple Git-155)\n", "2.50.1"},
		{"v24.22.0\n", "24.22.0"},
		{"go version go1.27.2 darwin/arm64\n", "1.27.2"},
		{"Claude Code 2.4\n", "2.4"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got, err := parseVersion(tt.output)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
	_, err := parseVersion("command not found")
	require.ErrorIs(t, err, ErrNoVersion)
}

func TestNodeIsJudgedByWhatTheArchifyInstallerNeeds(t *testing.T) {
	for version, want := range map[string]bool{
		"24.22.0": true, "22.20.0": true, "22.20": true, "23.0.0": true,
		"22.19.9": false, "20.11.1": false, "x": false,
	} {
		assert.Equal(t, want, atLeast(version, minNode), version)
	}
}

// setHome points the user's home folder at dir for the test: `HOME` where Go reads it, and
// `USERPROFILE`, which is what os.UserHomeDir reads on Windows.
func setHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

// A fake program that prints a version, so the probe runs a real process.
func fakeProgram(t *testing.T, name, output string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake program is a shell script")
	}
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\necho '"+output+"'\n"), 0o700)) //nolint:gosec // G306: the test runs it
	return path
}

func TestEveryToolIsReportedFoundOrNot(t *testing.T) {
	setHome(t, t.TempDir()) // no real Archify interferes
	t.Setenv("ARCHIFY_BIN", "")
	programs := map[string]string{
		Git:  fakeProgram(t, "git", "git version 2.50.1"),
		Node: fakeProgram(t, "node", "v20.11.1"),
	}
	finder := Finder{Lookup: func(name string) string { return programs[name] }, Path: filepath.SplitList(os.Getenv("PATH"))}

	tools := finder.Status(t.Context(), t.TempDir())
	byID := map[string]Tool{}
	for _, tool := range tools {
		byID[tool.ID] = tool
	}
	assert.Equal(t, []string{Git, Node, Go, Archify}, []string{tools[0].ID, tools[1].ID, tools[2].ID, tools[3].ID})
	assert.Equal(t, Tool{ID: Git, Installed: true, Version: "2.50.1", Path: programs[Git], Supported: true}, byID[Git])
	assert.Equal(t, Tool{ID: Node, Installed: true, Version: "20.11.1", Path: programs[Node], Supported: false}, byID[Node], "too old for the skills installer")
	assert.Equal(t, Tool{ID: Go}, byID[Go])
	assert.Equal(t, Tool{ID: Archify}, byID[Archify])
}

func TestAnArchifyTooOldIsInstalledButNotSupported(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("ARCHIFY_BIN", "")
	install := filepath.Join(home, ".claude", "skills", "archify")
	require.NoError(t, os.MkdirAll(filepath.Join(install, "bin"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(install, "bin", "archify.mjs"), []byte("// archify"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(install, "package.json"), []byte(`{"version":"2.9.0"}`), 0o600))

	tools := Finder{Lookup: func(string) string { return "" }}.Status(t.Context(), "")
	assert.Equal(t, Tool{ID: Archify, Installed: true, Version: "2.9.0", Path: filepath.Join(install, "bin", "archify.mjs"), Supported: false}, tools[3])
}

func TestAProgramThatDoesNotAnswerIsStillFound(t *testing.T) {
	broken := fakeProgram(t, "go", "")
	require.NoError(t, os.WriteFile(broken, []byte("#!/bin/sh\nexit 3\n"), 0o700)) //nolint:gosec // G306: the test runs it
	tool := Finder{Lookup: func(string) string { return broken }}.program(t.Context(), Go, "version", nil)
	assert.Equal(t, Tool{ID: Go, Installed: true, Path: broken, Supported: true}, tool)
}
