package git

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/proc"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/safego"
)

// Clone, fetch, pull and push (GIT-034).
//
// These four are the reason the whole package shells out, and they were already doing it in 2.x
// while everything else went through libgit2 (DIVERGENCE-GIT-c). Running the same `git` the user's
// terminal runs is what keeps SSH keys, credential helpers, `includeIf` config and two-factor
// prompts working untouched — none of which a library's own authentication support reaches.
//
// They are also the only operations that report progress, because they are the only ones that can
// take minutes.

// GitProgressEvent is one line a streamed operation printed.
//
// The name repeats the package deliberately: it is the payload of the `git:progress` event and the
// renderer's type of the same name, and a `git.ProgressEvent` would read as something else at the
// call site that emits it.
type GitProgressEvent struct { //nolint:revive // the wire name, matching the renderer's type
	Op   string `json:"op"`
	Line string `json:"line"`
}

// GitDoneEvent is emitted once when a streamed operation finishes, successfully or not.
type GitDoneEvent struct { //nolint:revive // the wire name, matching the renderer's type
	Op      string `json:"op"`
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// Network runs the operations that talk to a server, reporting progress as they go.
type Network struct {
	emitter bridge.Emitter
}

// NewNetwork binds the operations to an emitter. A nil one is replaced rather than guarded at every
// emit — a clone with nowhere to report to still has to clone.
func NewNetwork(emitter bridge.Emitter) Network {
	if emitter == nil {
		emitter = bridge.NopEmitter{}
	}
	return Network{emitter: emitter}
}

// Clone copies a repository into a new directory, with no working directory of its own.
func (n Network) Clone(ctx context.Context, url, dest string) error {
	return n.run(ctx, "clone", "", "clone", url, dest)
}

// Fetch updates the remote-tracking refs, defaulting to origin.
func (n Network) Fetch(ctx context.Context, repo string, remote *string) error {
	name := "origin"
	if remote != nil && *remote != "" {
		name = *remote
	}
	return n.run(ctx, "fetch", repo, "fetch", name)
}

// FetchRefspecs fetches explicit refspecs rather than the remote's default branches.
//
// Not a command: the PR review pipeline calls it to pull a pull request's exact head ref, which is
// what makes reviewing a fork's PR work at all. One `git fetch` for all of them rather than one
// each, so the negotiation, the connection and the authentication are paid once. It reports itself
// as `fetch`, so the progress log reads the same as any other.
func (n Network) FetchRefspecs(ctx context.Context, repo, remote string, refspecs []string) error {
	return n.run(ctx, "fetch", repo, append([]string{"fetch", remote}, refspecs...)...)
}

// Pull fetches and integrates, with the repository's own merge-or-rebase default.
//
// `--no-edit` is load-bearing rather than tidy (GIT-037). A divergent pull needs a merge commit,
// and without the flag git tries to open an editor for its message — against a child whose stdin
// was never redirected, so there is no TTY to open one on. The merge is applied to the working tree
// *before* the commit step, so the pull would exit non-zero having already merged but never
// committed, leaving the repository silently mid-merge with nothing saying why.
func (n Network) Pull(ctx context.Context, repo string) error {
	return n.run(ctx, "pull", repo, "pull", "--no-edit")
}

// Push sends the current branch, optionally setting its upstream.
//
// **`origin` is hardcoded** when setting the upstream — not read from the repository's remotes — so
// on a repository whose only remote is named something else, `push -u` fails. Ported as-is: it is
// 2.x's behaviour, and a repository configured that way has been failing this way all along.
func (n Network) Push(ctx context.Context, repo string, setUpstream bool) error {
	if !setUpstream {
		return n.run(ctx, "push", repo, "push")
	}

	branch, err := currentBranch(ctx, NewRunner(repo))
	if err != nil {
		return err
	}
	return n.run(ctx, "push", repo, "push", "-u", "origin", branch)
}

// currentBranch is HEAD's branch name, refusing the two states that have none.
func currentBranch(ctx context.Context, runner Runner) (string, error) {
	// Detached HEAD has no symbolic ref; an unborn one has a symbolic ref but no commit. 2.x
	// reported both with this one message, so both keep it.
	const notOnABranch = "cannot push -u from a detached HEAD"

	symbolic, err := runner.Run(ctx, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	if symbolic.Failed() {
		return "", errors.New(notOnABranch)
	}
	if _, err := headCommit(ctx, runner); err != nil {
		return "", errors.New(notOnABranch)
	}
	return strings.TrimSpace(symbolic.Stdout), nil
}

// run executes one streamed operation, publishing every line of both streams as it arrives.
//
// stdout and stderr are treated identically rather than one being an error channel: git writes most
// of its progress to stderr, so splitting them would leave the progress log empty and put
// "Receiving objects" in an error banner.
//
// The environment is the inherited one, untouched — no LC_ALL=C. These four are the operations
// whose output is *shown* rather than parsed, so it should arrive in the user's own language, and a
// credential helper may legitimately need to prompt.
func (n Network) run(ctx context.Context, op, dir string, args ...string) error {
	cmd := proc.Command(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return n.finish(op, err.Error(), false)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return n.finish(op, err.Error(), false)
	}
	if err := cmd.Start(); err != nil {
		// 2.x's wording for a git that would not start at all.
		return n.finish(op, "could not start git", false)
	}

	var (
		wg             sync.WaitGroup
		stdout, stderr []string
	)
	wg.Add(2)

	// Each pump owns its own slice, so nothing is shared between the two goroutines and the
	// collected lines are read only after Wait returns.
	pump := func(into *[]string, pipe io.Reader) func() {
		return func() {
			defer wg.Done()
			for line := range proc.ProgressLines(pipe) {
				*into = append(*into, line)
				n.emitter.Emit("git:progress", GitProgressEvent{Op: op, Line: line})
			}
		}
	}
	safego.Go("git-net-stdout", pump(&stdout, stdoutPipe))
	safego.Go("git-net-stderr", pump(&stderr, stderrPipe))
	wg.Wait()

	waitErr := cmd.Wait()
	return n.finish(op, failureDetail(op, cmd, stdout, stderr), waitErr == nil)
}

// failureDetail is what a failed operation has to say for itself.
//
// git writes most error detail to stderr, but a few rare misconfigurations only explain themselves
// on stdout — falling back keeps the UI from showing a bare failure with no reason. The exit-status
// label is the last resort, and the only way to reach it is a process that printed nothing at all
// and still exited non-zero.
func failureDetail(op string, cmd *proc.Cmd, stdout, stderr []string) string {
	switch {
	case len(stderr) > 0:
		return strings.Join(stderr, "\n")
	case len(stdout) > 0:
		return strings.Join(stdout, "\n")
	case cmd.ProcessState != nil:
		// VERBATIM, including ".NET's" phrasing of the exit status: the toast shows it.
		return fmt.Sprintf("git %s exited with exit status: %d", op, cmd.ProcessState.ExitCode())
	default:
		return fmt.Sprintf("git %s exited with exit status: unknown", op)
	}
}

// finish emits `git:done` and turns a failure into the command's error.
//
// The two strings are related but not identical, and that is 2.x's shape: the event carries the
// bare detail, the rejected call prefixes the operation. A listener showing both would otherwise
// print the operation name twice.
func (n Network) finish(op, detail string, success bool) error {
	message := "ok"
	if !success {
		message = detail
	}
	n.emitter.Emit("git:done", GitDoneEvent{Op: op, Success: success, Message: message})

	if success {
		return nil
	}
	return fmt.Errorf("git %s failed: %s", op, detail)
}
