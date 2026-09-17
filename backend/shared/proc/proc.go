// Package proc spawns every child process CodeFlow creates: git, claude, codex, agy, opencode,
// npx, gh and the shells behind the terminal.
//
// It exists because three of its rules are easy to forget once and expensive afterwards:
//
//   - # Children run in their own process group
//
// In 2.x the relationship was the other way round: the .NET core was detached from Electron
// (BOOT-037). Now the app is the parent of every CLI, so a signal an AI CLI raises against its own
// process group must not reach CodeFlow. Each child gets its own group — Setpgid on Unix,
// CREATE_NEW_PROCESS_GROUP on Windows — which is also what makes killing the whole tree possible.
//
//   - # A cancelled run kills the tree, not the process
//
// `claude` spawns node, which spawns more. Killing only the direct child orphans the rest, and the
// user's "stop" leaves a CPU pegged. This is the C# Kill(entireProcessTree: true).
//
//   - # No credential is ever put in a child's environment
//
// SEC-007. The environment is the inherited one with PATH replaced, and nothing else added. Tokens
// reach an engine through its own config or not at all. The invariant is mechanical rather than a
// habit: Environment is the only way to build a child's env, and it cannot add a variable.
//
// On Windows there is a fourth, invisible one: without CREATE_NO_WINDOW every git call flashes a
// console window in a GUI app.
package proc

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"unicode/utf8"
)

// Cmd is an *exec.Cmd that has been placed in its own process group and can be killed as a tree.
//
// It embeds rather than wraps so callers keep Stdin/Stdout/Stderr, Dir and the rest of the
// standard surface; what it adds is the part that must not be forgotten.
type Cmd struct {
	*exec.Cmd

	// jobHandle is the Windows Job Object this process was assigned to, kept so KillTree can close
	// it and take the whole tree with it. Set in group_windows.go and unused on Unix, where the
	// process group is the entire mechanism — which is why the linter cannot see a reader for it
	// when it analyses a Unix build.
	jobHandle uintptr //nolint:unused
}

// Command builds a child process in its own group, with no console window on Windows.
//
// The context governs the process's lifetime exactly as exec.CommandContext defines it, but note
// that a context cancellation kills only the direct child. Anything that spawns its own children —
// every AI CLI — must be stopped with KillTree.
func Command(ctx context.Context, name string, args ...string) *Cmd {
	// gosec G204: launching a named binary with arguments is this package's entire purpose — git,
	// the AI CLIs and the terminal's shell. Arguments are passed as a slice, never through a
	// shell, so there is no injection surface here; what the callers may spawn is constrained by
	// BinaryDiscovery (§8.9), not by this constructor.
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec
	cmd.Env = Environment(nil)
	applyGroupAttributes(cmd)
	return &Cmd{Cmd: cmd}
}

// Start begins the process and, on Windows, attaches it to a Job Object so the whole tree dies
// with it.
func (c *Cmd) Start() error {
	if err := c.Cmd.Start(); err != nil {
		return err
	}
	c.attachToJob()
	return nil
}

// AdoptStarted completes the setup for a process someone else started.
//
// It exists for exactly one caller: a PTY library owns the spawn — it has to, since attaching a
// pseudo-terminal happens as part of creating the process — so Start above never runs and the
// Windows Job Object is never attached. Without this, closing a terminal would kill the shell and
// orphan whatever it was running.
//
// Safe to call on a process that was never started, and idempotent in the only way that matters:
// attaching a job twice fails harmlessly and leaves the taskkill fallback in charge.
func (c *Cmd) AdoptStarted() {
	if c == nil || c.Cmd == nil || c.Process == nil {
		return
	}
	c.attachToJob()
}

// Run starts the process and waits for it.
func (c *Cmd) Run() error {
	if err := c.Start(); err != nil {
		return err
	}
	return c.Wait()
}

// KillTree terminates the process and everything it spawned.
//
// It is safe to call on a process that has already exited, and on one that was never started;
// "stop this run" arrives from the UI whenever the user clicks, not when the process is ready.
func (c *Cmd) KillTree() error {
	if c == nil || c.Cmd == nil || c.Process == nil {
		return nil
	}
	return killTree(c)
}

// Environment builds a child's environment: this process's, with PATH replaced by the given
// entries when there are any, and nothing added.
//
// The signature is the enforcement. There is no way to pass an extra variable, so no call site can
// grow one, and the SEC-007 test — a fake engine binary that dumps its argv, env and stdin, asked
// to find secrets that were stored in the credential store — has a single place to trust.
//
// Deliberately not set, matching the C# exactly: git network commands (clone, fetch, pull, push)
// keep the inherited environment untouched, because their stderr is shown to the user in the
// user's own language and a credential helper may legitimately need to prompt. Only the local,
// parsed git commands get LC_ALL=C, and they set it through their own runner rather than here.
func Environment(pathEntries []string) []string {
	inherited := os.Environ()
	if len(pathEntries) == 0 {
		return inherited
	}

	joined := strings.Join(pathEntries, string(os.PathListSeparator))
	out := make([]string, 0, len(inherited)+1)
	replaced := false
	for _, entry := range inherited {
		if name, _, found := strings.Cut(entry, "="); found && strings.EqualFold(name, "PATH") {
			if replaced {
				continue // Windows environments can carry Path and PATH; keep one
			}
			out = append(out, "PATH="+joined)
			replaced = true
			continue
		}
		out = append(out, entry)
	}
	if !replaced {
		out = append(out, "PATH="+joined)
	}
	return out
}

// DecodeLossyUTF8 turns a chunk of process output into a string, substituting U+FFFD for each
// invalid byte.
//
// Go's string([]byte) keeps invalid bytes as-is, which then travel through JSON as an encoding
// error and reach the renderer as a failed parse rather than as mojibake. C#'s
// Encoding.UTF8.GetString replaced them, so terminal and AI output that is not valid UTF-8 — a
// binary file cat'd into a terminal, a CLI writing in the system code page — degraded visibly
// instead of breaking the channel. This keeps that behaviour.
func DecodeLossyUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}

	var sb strings.Builder
	sb.Grow(len(b))
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		if r == utf8.RuneError && size <= 1 {
			sb.WriteRune(utf8.RuneError)
			b = b[1:]
			continue
		}
		sb.Write(b[:size])
		b = b[size:]
	}
	return sb.String()
}
