// Package terminal runs the shell behind the app's terminal pane, one PTY per session.
package terminal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/proc"
)

// gitBashMissing is what Windows answers when Git for Windows is not installed. VERBATIM, URL
// included: it is the whole remedy, and the user reads it in a toast with nothing else to go on.
const gitBashMissing = "Git Bash not found — install Git for Windows (https://git-scm.com/download/win)"

// Shell is the command a terminal session runs.
type Shell struct {
	Path string
	Args []string
}

// resolveShell picks the shell for this platform (FILE-014).
//
// Everywhere but Windows this is the user's own `$SHELL`, because a terminal that is not their
// terminal — different prompt, different aliases, different rc file — is a worse tool than no
// terminal.
//
// On Windows it is **always Git Bash**, and the refusal below is deliberate.
func resolveShell(ctx context.Context) (Shell, error) {
	if runtime.GOOS != "windows" {
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/bash"
		}
		return Shell{Path: shell}, nil
	}

	path, found := windowsGitBash(ctx)
	if !found {
		// VERBATIM: the capital is the message, not a style slip. The user reads this sentence in
		// a toast, and it is the whole remedy.
		return Shell{}, errors.New(gitBashMissing) //nolint:staticcheck // ST1005
	}
	// A login, interactive shell: without both, the profile never runs and the prompt is bare.
	return Shell{Path: path, Args: []string{"--login", "-i"}}, nil
}

// windowsGitBash locates bash.exe (FILE-014, DIVERGENCE-FILE-c).
//
// **It does not fall back to PowerShell or cmd**, even though one of them is always available.
// That is the trade the original made on purpose: silently handing back a different, less capable
// shell is how the terminal used to break in ways nobody could explain — a script that worked in
// everyone else's terminal failing in this one. Failing loudly names the problem and its fix.
//
// The search starts from `git --exec-path`, which points inside the installation
// (`<root>\mingw64\libexec\git-core`), and walks up looking for `bin\bash.exe`. Six directories in
// total: the exec path itself and five ancestors, which reaches the root from every layout Git for
// Windows has shipped.
func windowsGitBash(ctx context.Context) (string, bool) {
	// A real deadline rather than the original's, which waited five seconds *after* a blocking
	// read and therefore bounded nothing. A hung git here would hang the terminal pane.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if execPath, err := proc.Command(ctx, "git", "--exec-path").Output(); err == nil {
		dir := strings.TrimSpace(string(execPath))
		for range 6 {
			if dir == "" {
				break
			}
			candidate := filepath.Join(dir, "bin", "bash.exe")
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, true
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	// git not on PATH, or an installation laid out differently. These two are where the installer
	// puts it.
	for _, candidate := range []string{
		`C:\Program Files\Git\bin\bash.exe`,
		`C:\Program Files (x86)\Git\bin\bash.exe`,
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}
