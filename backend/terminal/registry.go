package terminal

import (
	"context"
	"errors"
	"sync"

	"github.com/charmbracelet/x/xpty"
	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/proc"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/safego"
	"github.com/google/uuid"
)

const (
	// The PTY's initial size. The renderer resizes it as soon as the pane is measured, so these
	// only govern the first moments — long enough for a shell's startup banner to wrap badly if
	// they were wrong.
	initialCols = 100
	initialRows = 30

	// readBufferBytes is one read from the PTY.
	//
	// Each chunk is decoded on its own with no state carried between them, so a multi-byte
	// character landing across a boundary becomes a replacement character rather than being
	// reassembled. That is 2.x's behaviour (AMBIGUOUS-FILE-c) and it is preserved: buffering a
	// partial sequence would hold back output that the user is waiting to see, and the case is
	// rare enough that nobody has reported it in two years.
	readBufferBytes = 4096

	// outputBuffer is how many chunks may wait to be emitted.
	//
	// The channel **blocks** when full rather than dropping. Terminal output is not a progress
	// signal where losing one of a thousand costs nothing — it is the content of the pane, and a
	// dropped chunk is a hole in the middle of a build log. Back-pressure slows the shell down,
	// which is the right trade.
	outputBuffer = 64
)

// errNoSession is what every command answers for an id that is not registered. VERBATIM.
var errNoSession = errors.New("no such terminal session")

// Registry owns the running terminal sessions.
//
// One registry for the application's lifetime rather than one per call: a terminal outlives the
// command that opened it by definition, so nothing here may be governed by a call's context.
type Registry struct {
	emitter bridge.Emitter

	mu       sync.Mutex
	sessions map[string]*session
}

type session struct {
	pty  xpty.Pty
	cmd  *proc.Cmd
	done chan struct{}
}

// NewRegistry builds the registry.
func NewRegistry(emitter bridge.Emitter) *Registry {
	if emitter == nil {
		emitter = bridge.NopEmitter{}
	}
	return &Registry{emitter: emitter, sessions: make(map[string]*session, 2)}
}

// Open starts a shell in cwd and returns the new session's id.
//
// The session is registered **before** the reader starts, so the id the caller receives is always
// one it can immediately write to, resize or close.
func (r *Registry) Open(ctx context.Context, cwd string) (string, error) {
	shell, err := resolveShell(ctx)
	if err != nil {
		return "", err
	}

	pty, err := xpty.NewPty(initialCols, initialRows)
	if err != nil {
		return "", err
	}

	// Through proc, like every other child: its own process group, no console window on Windows,
	// and an environment that cannot carry a credential.
	//
	// context.WithoutCancel, and this is the point: the shell outlives the command that opened it,
	// while the context Wails hands a call is cancelled the moment that call returns. Passing it
	// straight through would kill every terminal the instant it opened.
	cmd := proc.Command(context.WithoutCancel(ctx), shell.Path, shell.Args...)
	cmd.Dir = cwd
	// TERM is what makes the shell emit colour and the editor keys work at all. Not a credential,
	// so proc's environment rule is untouched by adding it here.
	cmd.Env = append(cmd.Env, "TERM=xterm-256color")

	// The PTY owns the spawn — attaching a pseudo-terminal is part of creating the process — so
	// proc's own Start never runs and its Windows Job Object has to be attached afterwards.
	// Without that, closing a terminal kills the shell and orphans whatever it was running.
	if err := pty.Start(cmd.Cmd); err != nil {
		_ = pty.Close()
		return "", err
	}
	cmd.AdoptStarted()

	id := uuid.NewString()
	current := &session{pty: pty, cmd: cmd, done: make(chan struct{})}

	r.mu.Lock()
	r.sessions[id] = current
	r.mu.Unlock()

	output := make(chan string, outputBuffer)

	// Three goroutines, and the order between them is the whole of FILE-015.
	//
	// Measured on both platforms: the read loop does **not** end when the shell exits — it stays
	// blocked until the PTY is closed. So waiting for the process is what closes the PTY, which is
	// what ends the reader, which is what drains the output and finally reports the exit. Without
	// the first of the three, a shell the user typed `exit` into would look like it was still
	// running.
	safego.Go("terminal-wait", func() {
		_ = xpty.WaitProcess(context.WithoutCancel(ctx), cmd.Cmd)
		_ = pty.Close()
	})

	safego.Go("terminal-read", func() {
		defer close(output)
		buffer := make([]byte, readBufferBytes)
		for {
			n, err := pty.Read(buffer)
			if n > 0 {
				// Blocks when the consumer is behind, which is the back-pressure above.
				output <- proc.DecodeLossyUTF8(buffer[:n])
			}
			if err != nil {
				// EOF on Unix, "The handle is invalid." on Windows once the PTY is closed. Both
				// are the normal end of a session, not a failure worth reporting.
				return
			}
		}
	})

	safego.Go("terminal-emit", func() {
		defer close(current.done)
		for chunk := range output {
			r.emitter.Emit("terminal:output", map[string]string{"id": id, "data": chunk})
		}
		// Only once the output has drained: an exit reported ahead of the last chunk would have
		// the pane close over text the user never saw.
		r.emitter.Emit("terminal:exit", map[string]string{"id": id})

		r.mu.Lock()
		delete(r.sessions, id)
		r.mu.Unlock()
	})

	return id, nil
}

// Write sends input to a session.
func (r *Registry) Write(id, data string) error {
	current, found := r.lookup(id)
	if !found {
		return errNoSession
	}
	_, err := current.pty.Write([]byte(data))
	return err
}

// Resize changes a session's PTY size (FILE-016).
//
// The parameters arrive as (cols, rows) and the library takes (width, height) — the same pair in
// the same order, which is worth stating because the two names invite swapping them, and a swapped
// pair produces a terminal that wraps at the wrong column rather than an error.
func (r *Registry) Resize(id string, cols, rows int) error {
	current, found := r.lookup(id)
	if !found {
		return errNoSession
	}
	return current.pty.Resize(cols, rows)
}

// Close kills a session's shell and everything it spawned.
//
// Closing an unknown id is not an error: the renderer closes a pane it may already have lost, and
// a red banner for a terminal that is already gone helps nobody.
//
// It kills the **tree**, not the process: a shell running a build has children, and killing only
// the shell leaves them orphaned with a CPU pegged.
func (r *Registry) Close(id string) {
	r.mu.Lock()
	current, found := r.sessions[id]
	r.mu.Unlock()

	if !found {
		return
	}

	// Killing the tree ends the process, which ends the wait, which closes the PTY, which ends the
	// reader — the same path a shell exiting on its own takes, so `terminal:exit` is emitted once
	// either way.
	_ = current.cmd.KillTree()
	<-current.done
}

// CloseAll ends every session, for shutdown.
func (r *Registry) CloseAll() {
	r.mu.Lock()
	ids := make([]string, 0, len(r.sessions))
	for id := range r.sessions {
		ids = append(ids, id)
	}
	r.mu.Unlock()

	for _, id := range ids {
		r.Close(id)
	}
}

func (r *Registry) lookup(id string) (*session, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, found := r.sessions[id]
	return current, found
}
