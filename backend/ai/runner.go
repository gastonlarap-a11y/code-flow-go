package ai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/gastonlarap-a11y/code-flow/backend/platform"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/proc"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/safego"
)

// Invocation is the one provider-neutral call shape (AI-002).
//
// `Prompt` is the ask and goes on argv for most engines; `StdinContent` is the data payload — a
// diff, a pull request's context, a finding, the two sides of a conflict. Keeping them apart is
// what lets each engine decide where its own CLI wants them, which is not the same answer twice.
type Invocation struct {
	Prompt       string
	SystemPrompt string
	StdinContent string
	Model        string
	AllowedTools []string
	// WorkingDir is the repository the run happens inside.
	WorkingDir string
	// AutoApproveEdits is only meaningful for an agentic engine that can write.
	AutoApproveEdits bool
	// SessionID resumes a previous conversation where the engine supports it.
	SessionID string
	// MCPConfig is a path to a per-review MCP configuration, when there is one.
	MCPConfig string
	// ReadOnly marks a run that cannot have written anything, which is what makes it safe to
	// repeat after a transient network failure (AI-055).
	ReadOnly bool
}

// Result is what a finished run produced.
type Result struct {
	// Text is the reply, extracted after the process exited — never assembled from the streamed
	// lines (AI-011).
	Text string
	// SessionID is the conversation id to resume with next time, where the engine has one.
	SessionID string
	// Trace is the activity log, for a run that asked to keep one.
	Trace []string
}

// Interpreter turns a finished process into a reply, or into the error the user sees.
//
// Declared here because every engine's answer is different in a way the runner must not know
// about: Claude reports its own failures as JSON on stdout while exiting non-zero, opencode's
// error event beats its exit status, and Codex's stdout is the reply with stderr as progress.
type Interpreter interface {
	// Interpret receives both captured streams and the exit code, already ANSI-stripped.
	Interpret(stdout, stderr string, exitCode int) (Result, error)
}

// CommandBuilder is how an engine turns an invocation into argv and a stdin payload.
type CommandBuilder interface {
	// BuildCommand returns the arguments after the binary name.
	BuildCommand(inv Invocation) []string
	// StdinPayload is what to write to the child's stdin. An engine whose CLI does not read stdin
	// returns the empty string, and that is a **declaration** rather than an omission — AI-054
	// reads it to tell a broken pipe from an engine that never wanted one.
	StdinPayload(inv Invocation) string
}

// Runner spawns a subprocess engine and turns it into a Result.
type Runner struct{ registry *RunRegistry }

// NewRunner binds a runner to the registry that observes its runs.
func NewRunner(registry *RunRegistry) Runner { return Runner{registry: registry} }

// Execute runs one invocation to completion (AI-009).
//
// The shape is dictated by two deadlocks it has to avoid. Stdin is written from its own goroutine
// concurrently with waiting for the child, so an engine that never reads stdin cannot wedge the
// pipe once the operating system's buffer fills. Both output pipes are pumped concurrently for the
// same reason, and because a child that fills stderr while nobody drains it stops writing stdout.
func (r Runner) Execute(
	ctx context.Context,
	run *Run,
	binary string,
	builder CommandBuilder,
	interpreter Interpreter,
	inv Invocation,
) (Result, error) {
	result, err := r.execute(ctx, run, binary, builder, interpreter, inv)
	if err == nil {
		return result, nil
	}

	// A run that never reached the network is repeated once — but **only** when it could not have
	// written anything (AI-055). Repeating a write-capable run would apply the agent's edits
	// twice, and a timed-out run is deliberately excluded: the far side may still be working.
	if inv.ReadOnly && run != nil && !run.Cancelled() && run.StopReason() == "" &&
		platform.IsTransientNetwork(err) {
		return r.execute(ctx, run, binary, builder, interpreter, inv)
	}
	return result, err
}

func (r Runner) execute(
	ctx context.Context,
	run *Run,
	binary string,
	builder CommandBuilder,
	interpreter Interpreter,
	inv Invocation,
) (Result, error) {
	resolved := ResolveBinary(binary, SearchDirs())

	cmd := proc.Command(context.WithoutCancel(ctx), resolved, builder.BuildCommand(inv)...)
	cmd.Dir = inv.WorkingDir
	// The augmented search path, so the CLI's own subprocesses — node, git — find each other. It
	// carries no credential: proc.Environment cannot add one (SEC-007).
	cmd.Env = proc.Environment(SearchDirs())

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return Result{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return Result{}, err
	}

	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("%s could not be started: %w", binary, err)
	}

	var (
		wg                 sync.WaitGroup
		outBytes, errBytes []byte
		stdinErr           error
	)
	wg.Add(3)

	payload := builder.StdinPayload(inv)
	safego.Go("ai-stdin", func() {
		defer wg.Done()
		stdinErr = feedStdin(stdin, payload)
	})
	safego.Go("ai-stdout", func() {
		defer wg.Done()
		outBytes = pump(stdout, run, StreamStdout)
	})
	safego.Go("ai-stderr", func() {
		defer wg.Done()
		errBytes = pump(stderr, run, StreamStderr)
	})

	// The deadline runs only while the process does.
	if run != nil {
		safego.Go("ai-silence", run.WatchSilence)
	}

	// Killing the tree, not the process: these CLIs spawn node, which spawns more. Killing only
	// the direct child leaves the real model call running — and billing — in a grandchild.
	stopped := make(chan struct{})
	if run != nil {
		safego.Go("ai-stop", func() {
			select {
			case <-run.Stopped():
				_ = cmd.KillTree()
			case <-stopped:
			}
		})
	}

	// **Every read finishes before `Wait` is called**, and that order is not a preference: `Wait`
	// closes the pipes `StdoutPipe` and `StderrPipe` return as soon as the process exits, and
	// `os/exec` says so plainly — "it is incorrect to call Wait before all reads from the pipe have
	// completed". Waiting first meant a pump could have its pipe closed out from under it with
	// output still in flight, and the run would come back missing its last lines. For a process
	// that writes a little and exits at once, missing all of them.
	//
	// Not theoretical: three CI runs in this package failed on it, each one a different piece of
	// output gone — the reply text, a stdout line, the stdin echo. Rare on an idle machine and
	// common on a loaded one, which is the worst shape a defect can have, and why it read as
	// flakiness for two releases.
	//
	// The wait is bounded by the run's own machinery. A pipe that a grandchild holds open after the
	// child exits stalls the reads; nothing arrives, the silence deadline fires, and `KillTree`
	// takes the grandchild with it, which closes the pipe. A call with no `Run` has no deadline —
	// but it has no bound on the process either, so this widens a case that was already unbounded
	// rather than opening a new one.
	wg.Wait()
	waitErr := cmd.Wait()
	close(stopped)

	// Asked after the wait: a run stopped while it was running has its reason set by then, and it
	// replaces whatever the killed process happened to print.
	if run != nil {
		if reason := run.StopReason(); reason != "" {
			return Result{}, errors.New(reason)
		}
	}

	// An incomplete stdin delivery fails the run rather than passing for a whole one (AI-054). An
	// engine that answers from half a diff answers confidently and wrongly, which is worse than
	// not answering — but only when there was a payload to deliver: an engine that declares it
	// reads no stdin has a broken pipe by design.
	if stdinErr != nil && payload != "" {
		return Result{}, fmt.Errorf("%s did not receive its input: %w", binary, stdinErr)
	}

	exitCode := 0
	if waitErr != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}

	stdoutText := stripANSI(proc.DecodeLossyUTF8(outBytes))
	stderrText := stripANSI(proc.DecodeLossyUTF8(errBytes))

	result, err := interpreter.Interpret(stdoutText, stderrText, exitCode)
	if run != nil {
		result.Trace = run.Trace()
	}
	return result, err
}

// feedStdin writes the payload and closes the pipe.
//
// Closing is not optional: a CLI reading stdin to end-of-file waits forever if nobody closes it,
// and the run's only bound would then be the silence deadline ten minutes later.
//
// A broken pipe is reported rather than swallowed — but see the caller: it is only an error when
// there was something to deliver. An engine that declares it reads no stdin produces one on every
// single run, and treating that as a failure is what hid a real broken delivery for months.
func feedStdin(stdin io.WriteCloser, payload string) error {
	defer func() { _ = stdin.Close() }()

	if payload == "" {
		return nil
	}
	written, err := io.WriteString(stdin, payload)
	if err != nil {
		return err
	}
	if written != len(payload) {
		return fmt.Errorf("wrote %d of %d bytes", written, len(payload))
	}
	return nil
}

// FailureDetail is the shape the four subprocess engines share for a non-zero exit. VERBATIM,
// including the Spanish sentinel for a process that printed nothing at all.
func FailureDetail(binary string, exitCode int, stdout, stderr string) string {
	detail := strings.TrimSpace(stderr)
	if detail == "" {
		detail = strings.TrimSpace(stdout)
	}
	if detail == "" {
		detail = "sin salida en stdout ni stderr"
	}
	return fmt.Sprintf("%s exited with an error (exit status: %d): %s", binary, exitCode, detail)
}
