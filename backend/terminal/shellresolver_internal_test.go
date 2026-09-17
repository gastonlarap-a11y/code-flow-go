package terminal

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An internal test because the resolver is not part of the package's surface: what callers get is
// a session, and which shell it runs is this file's business.

func TestTheUsersOwnShellIsUsed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows is always Git Bash, whatever $SHELL says")
	}
	t.Setenv("SHELL", "/bin/zsh")

	shell, err := resolveShell(t.Context())

	require.NoError(t, err)
	assert.Equal(t, "/bin/zsh", shell.Path,
		"a terminal that is not the user's own terminal is a worse tool than none")
	assert.Empty(t, shell.Args, "no arguments outside Windows")
}

// A shell with no $SHELL is a real state — a process started by launchd inherits very little.
func TestTheFallbackShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows resolves Git Bash instead")
	}
	t.Setenv("SHELL", "")

	shell, err := resolveShell(t.Context())

	require.NoError(t, err)
	assert.Equal(t, "/bin/bash", shell.Path)
}

// DIVERGENCE-FILE-c, stated as a test so nobody "improves" it into a PowerShell fallback: on
// Windows this refuses rather than handing back a different, less capable shell.
func TestWindowsRefusesRatherThanFallingBack(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the refusal only exists on Windows")
	}
	if _, found := windowsGitBash(t.Context()); found {
		t.Skip("Git for Windows is installed here, so the refusal cannot be reached")
	}

	_, err := resolveShell(t.Context())

	assert.EqualError(t, err, gitBashMissing)
}

// The message is the whole remedy: the user reads it in a toast with nothing else to go on.
func TestTheRefusalCarriesTheInstallLink(t *testing.T) {
	assert.Equal(t,
		"Git Bash not found — install Git for Windows (https://git-scm.com/download/win)",
		gitBashMissing)
}
