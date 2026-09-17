package platform

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// A GUI application launched from Finder or the Dock inherits launchd's PATH, not the one the
// user's shell builds. So `git`, `claude`, `codex`, `gh` and every tool installed by Homebrew,
// nvm, mise or pnpm are missing — while the same binary run from a terminal finds all of them.
// That asymmetry is why "it works when I run it myself" was a recurring 2.x bug report.
//
// The fix, ported from shell/src/login-path.ts, is to ask the user's login shell what its PATH is
// and merge it in before anything is spawned. Windows has no equivalent problem: a process there
// inherits the machine and user PATH from the registry regardless of how it was started.
//
// This is the one piece of 2.x behaviour that never had a spec rule of its own; it is transcribed
// in MIGRATION-GO.md §15.2.4.

// envMarker brackets the environment dump so a noisy profile — a shell that prints a banner, a
// version manager that greets you, a fortune — cannot be mistaken for output. Everything outside
// the pair is discarded.
const envMarker = "__CODEFLOW_ENV__"

// loginShellTimeout caps how long a broken or interactive profile can hold up start-up. A login
// shell that waits for input would otherwise hang the app before its window ever appears.
const loginShellTimeout = 2 * time.Second

// Logger is the slice of diagnostics.ShellLog this package needs. Declared here, at the consumer,
// so platform does not depend on diagnostics.
type Logger interface {
	Info(message string)
	Warn(message string)
}

// ApplyLoginShellPath merges the login shell's PATH into this process's, on macOS only.
//
// It is called at the very top of main, before any child process exists, because the merged PATH
// is inherited: doing it later would leave whichever tool ran first unable to find its binary.
//
// Every failure path keeps the inherited PATH and logs. Not finding the user's tools degrades the
// app; refusing to start over it would be worse.
func ApplyLoginShellPath(log Logger) {
	if runtime.GOOS != "darwin" {
		return
	}

	captured, ok := captureLoginShellPath()
	if !ok {
		warn(log, "[shell] could not read the login shell PATH; using the inherited one")
		return
	}

	merged := MergePath(captured, os.Getenv("PATH"))
	if merged == "" {
		return
	}
	if err := os.Setenv("PATH", merged); err != nil {
		warn(log, "[shell] could not set the merged PATH: "+err.Error())
		return
	}
	info(log, "[shell] PATH merged from the login shell")
}

func captureLoginShellPath() (string, bool) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	ctx, cancel := context.WithTimeout(context.Background(), loginShellTimeout)
	defer cancel()

	// -i as well as -l because a great many setups put their PATH edits in .zshrc/.bashrc, which a
	// non-interactive shell does not read. Stdin is left nil (so the child reads /dev/null and an
	// interactive prompt gets EOF rather than blocking) and stderr is discarded: a profile that
	// complains on every launch is not this function's problem.
	// gosec G204: the only variable is $SHELL, which is the user's own login shell — the thing
	// this function exists to ask. The script is a constant and carries no interpolated input.
	cmd := exec.CommandContext(ctx, shell, "-ilc", //nolint:gosec
		"echo "+envMarker+"; env; echo "+envMarker)

	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return "", false
	}

	return ExtractPath(string(out))
}

// ExtractPath pulls the PATH value out of a shell's environment dump.
//
// Exported for its tests: the parsing, not the spawning, is where this has historically gone
// wrong. A PATH value legitimately contains "=" (a directory can be named anything), so the line
// is split once on the first "=" and the remainder taken whole.
func ExtractPath(output string) (string, bool) {
	start := strings.Index(output, envMarker)
	if start < 0 {
		return "", false
	}
	rest := output[start+len(envMarker):]

	end := strings.Index(rest, envMarker)
	if end < 0 {
		// One marker and not two means the shell died midway; whatever came after it is a partial
		// dump we have no reason to trust.
		return "", false
	}

	for line := range strings.SplitSeq(rest[:end], "\n") {
		value, found := strings.CutPrefix(strings.TrimSpace(line), "PATH=")
		if !found {
			continue
		}
		if value = strings.TrimSpace(value); value != "" {
			return value, true
		}
		return "", false
	}
	return "", false
}

// MergePath joins the captured PATH in front of the inherited one, dropping empties and
// duplicates, first occurrence winning.
//
// Captured first because it is the more specific answer: if the user's shell puts a project's
// toolchain ahead of the system one, CodeFlow should spawn the same binary the user would get in
// their terminal. Keeping the inherited entries behind it means a tool that only launchd knows
// about is still reachable.
func MergePath(captured, inherited string) string {
	merged := make([]string, 0, 32)
	seen := make(map[string]struct{}, 32)

	for _, source := range []string{captured, inherited} {
		for entry := range strings.SplitSeq(source, ":") {
			if entry == "" {
				continue
			}
			if _, duplicate := seen[entry]; duplicate {
				continue
			}
			seen[entry] = struct{}{}
			merged = append(merged, entry)
		}
	}
	return strings.Join(merged, ":")
}

func info(log Logger, message string) {
	if log != nil {
		log.Info(message)
	}
}

func warn(log Logger, message string) {
	if log != nil {
		log.Warn(message)
	}
}
