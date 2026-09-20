// Package git replaces LibGit2Sharp with the `git` command line.
//
// That is the single largest behavioural decision of this port, and it was not taken for
// convenience: no Go library reaches parity. go-git has no stash and only partial merge support;
// git2go has been unreleased since 2022 and needs libgit2 built per platform. Meanwhile `git` was
// already a hard requirement — 2.x shelled out for clone, fetch, pull and push, and the Windows
// terminal is Git Bash.
//
// The cost is that semantics now come from git's own output rather than from libgit2's data
// structures, so parity is proven by tests against real temporary repositories and by the four
// git vector files, never by reasoning about what libgit2 used to do.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/proc"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/safego"
)

// Result is one git invocation's outcome. ExitCode is part of it because several operations
// classify on it — a stash that conflicts exits 1, a `git diff --no-index` that found differences
// exits 1, and neither is a failure.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Failed reports a non-zero exit.
func (r Result) Failed() bool { return r.ExitCode != 0 }

// Detail is the most useful text git produced, for an error message: stderr, else stdout.
func (r Result) Detail() string {
	if detail := strings.TrimSpace(r.Stderr); detail != "" {
		return detail
	}
	return strings.TrimSpace(r.Stdout)
}

// Runner invokes git against one repository.
//
// A type rather than a package function so the repository path is bound once and every call site
// is shorter than the trap it would otherwise contain: `git` run without `-C` operates on whatever
// directory the process happens to be in, which for a desktop app is the user's home.
type Runner struct {
	repo string
}

// NewRunner binds a runner to a repository path.
func NewRunner(repo string) Runner { return Runner{repo: repo} }

// Repo is the repository this runner operates on.
func (r Runner) Repo() string { return r.repo }

// baseArgs are prepended to every invocation.
//
//   - `-C` binds the repository explicitly.
//   - `core.quotepath=off` stops git from escaping non-ASCII filenames as \303\251 octal, which
//     would otherwise reach the renderer as mojibake for every accented path.
//   - `color.ui=never` because a user with `color.ui=always` in their global config would
//     otherwise have ANSI escapes parsed as part of filenames and statuses.
func (r Runner) baseArgs(args []string) []string {
	return append([]string{"-C", r.repo, "-c", "core.quotepath=off", "-c", "color.ui=never"}, args...)
}

// Run executes a read-only, parsed git command.
//
// `LC_ALL=C` pins the language: this port matches git's English text in several places — "would be
// overwritten", "CONFLICT", "only has N entries" — and under a Spanish or German locale those
// checks would silently stop matching and every stash conflict would classify as `unknown`.
// libgit2 always spoke English, so this restores the property rather than imposing a new one.
//
// `GIT_OPTIONAL_LOCKS=0` keeps a status read from taking the index lock, so a background refresh
// cannot block the user's own commit.
func (r Runner) Run(ctx context.Context, args ...string) (Result, error) {
	return r.run(ctx, r.baseArgs(args), []string{"LC_ALL=C", "GIT_OPTIONAL_LOCKS=0"})
}

// RunWrite executes a parsed git command that writes. Same language pinning, but the index lock is
// taken normally — a write that skipped it would race the user's own git.
func (r Runner) RunWrite(ctx context.Context, args ...string) (Result, error) {
	return r.run(ctx, r.baseArgs(args), []string{"LC_ALL=C"})
}

// RunNetwork executes clone, fetch, pull or push.
//
// Deliberately **not** language-pinned and given no extra environment at all. Its stderr is shown
// to the user rather than parsed, so it should arrive in the user's own language; and a credential
// helper may need to prompt, which a sanitised environment can prevent. 2.x set nothing on these
// either — verified in the source — so this is parity, not a new decision.
func (r Runner) RunNetwork(ctx context.Context, args ...string) (Result, error) {
	return r.run(ctx, r.baseArgs(args), nil)
}

// RunWithEnv is for the operations that need their own variables: checkpoints set GIT_INDEX_FILE
// and an author identity, commits set GIT_COMMITTER_*.
func (r Runner) RunWithEnv(ctx context.Context, env []string, args ...string) (Result, error) {
	return r.run(ctx, r.baseArgs(args), append([]string{"LC_ALL=C"}, env...))
}

// RawResult is an invocation whose stdout is file content rather than text to parse.
type RawResult struct {
	Stdout   []byte
	Stderr   string
	ExitCode int
}

// Failed reports a non-zero exit.
func (r RawResult) Failed() bool { return r.ExitCode != 0 }

// RunRaw executes a git command whose stdout must survive byte for byte — `git show :2:<path>`,
// `git cat-file`, restoring a file from a checkpoint.
//
// Run cannot be used for those: it decodes output line by line, which strips a lone `\r` and
// appends a trailing newline to content that had none. On a CRLF file that silently rewrites every
// line ending, and the user sees a whole-file diff they did not make.
func (r Runner) RunRaw(ctx context.Context, args ...string) (RawResult, error) {
	full := r.baseArgs(args)

	cmd := proc.Command(ctx, "git", full...)
	cmd.Dir = r.repo
	cmd.Env = append(proc.Environment(nil), "LC_ALL=C")

	// os/exec drains both pipes itself when they are plain writers, so there is no deadlock to
	// avoid here and no goroutine of ours to start.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := RawResult{Stdout: stdout.Bytes(), Stderr: stderr.String()}

	if err == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	return result, startFailure(r.repo, full, err)
}

// RunOutsideRepo runs git without binding a repository — `git --version`, `git init`, `git clone`.
func RunOutsideRepo(ctx context.Context, env []string, args ...string) (Result, error) {
	return execute(ctx, args, env, "")
}

func (r Runner) run(ctx context.Context, args, env []string) (Result, error) {
	return execute(ctx, args, env, r.repo)
}

// execute is the one place a git process is created.
func execute(ctx context.Context, args, extraEnv []string, dir string) (Result, error) {
	cmd := proc.Command(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if len(extraEnv) > 0 {
		cmd.Env = append(proc.Environment(nil), extraEnv...)
	}

	stdout, stderr, err := captureBoth(cmd)
	result := Result{Stdout: stdout, Stderr: stderr}

	if err == nil {
		return result, nil
	}

	// A non-zero exit is data, not a failure: several callers classify on the code. Only a failure
	// to *run* git at all is an error worth returning.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	return result, startFailure(dir, args, err)
}

// startFailure explains why git could not be started, rather than repeating what Go said.
//
// `exec` reports a missing working directory as `fork/exec /usr/bin/git: no such file or directory`
// — it attributes the failed chdir to the binary, because that is the path it was holding when the
// child called `execve`. Passed through, that sentence tells a user whose project folder was moved
// or unmounted that **git is not installed**, and prints the whole command line into a toast to say
// it. Both halves are wrong and the second is what they would take to a bug report.
//
// Found by the differential oracle (`tools/parity`): 2.7.1 answers `Path '<path>' doesn't point at
// a valid Git repository or workdir.` for the same call.
func startFailure(dir string, args []string, err error) error {
	if dir != "" {
		if info, statErr := os.Stat(dir); statErr != nil || !info.IsDir() {
			return fmt.Errorf("%s is not a directory that can be read; the repository may have been moved, renamed or unmounted", dir)
		}
	}
	return fmt.Errorf("run git %s: %w", strings.Join(args, " "), err)
}

// captureBoth reads stdout and stderr concurrently.
//
// Never CombinedOutput and never one after the other: git writes progress to stderr while writing
// data to stdout, and a reader that drains one pipe only deadlocks as soon as the other's buffer
// fills — which for a large diff is immediately and for a small one never, so it looks like an
// intermittent hang.
func captureBoth(cmd *proc.Cmd) (string, string, error) {
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", "", err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return "", "", err
	}
	if err := cmd.Start(); err != nil {
		return "", "", err
	}

	var (
		wg             sync.WaitGroup
		stdout, stderr strings.Builder
	)
	wg.Add(2)

	// Each goroutine writes to its own builder, so nothing is shared between them. wg.Done is
	// deferred inside the closure, so it runs while a panic unwinds and safego recovers after —
	// wg.Wait cannot hang, even on a defect.
	read := func(into *strings.Builder, from io.Reader) func() {
		return func() {
			defer wg.Done()
			for line := range proc.Lines(from) {
				into.WriteString(line)
				into.WriteString("\n")
			}
		}
	}
	safego.Go("git-stdout", read(&stdout, stdoutPipe))
	safego.Go("git-stderr", read(&stderr, stderrPipe))
	wg.Wait()

	return stdout.String(), stderr.String(), cmd.Wait()
}
