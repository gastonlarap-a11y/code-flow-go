package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// The client for the **installed 2.7.1 core** — the side of the oracle that cannot be imported.
//
// MIGRATION-GO.md §2.3 documents the transport this port removed, and documents it precisely so
// that nobody rebuilds it by accident. This file is the one place it is allowed to exist, because
// reading the old answers is the only way to prove the new ones match.

const (
	// frameMax is the transport's own limit: a uint32 little-endian length, then UTF-8 JSON.
	frameMax = 64 << 20
	// readyTimeout bounds waiting for the core's ready line. A .NET start-up on a cold page cache
	// is slow; twenty seconds is far past it and still short enough to fail rather than hang.
	readyTimeout = 20 * time.Second
	// callTimeout bounds one request. Every command in the script is read-mostly and local.
	callTimeout = 30 * time.Second
)

// oldCore is a running 2.7.1 core and the rpc connection to it.
type oldCore struct {
	cmd  *exec.Cmd
	conn net.Conn

	mu      sync.Mutex
	nextID  int
	pending map[int]json.RawMessage
}

// startOldCore spawns the installed core against a base directory of our choosing and opens its
// rpc channel.
//
// # Why this one process does not go through shared/proc
//
// `proc.Environment` cannot add a variable, by design (SEC-007) — and replacing `HOME` is the
// entire safety mechanism here. The old core resolves its base directory from the home directory,
// so a run with the inherited environment would open **the developer's real `~/CodeFlow`**, run its
// migrations against it and write to its database. Redirecting `HOME` is what keeps a parity run
// from touching the data the port is supposed to preserve, so the environment is built explicitly,
// here, in a developer tool that is never shipped.
func startOldCore(ctx context.Context, binary, home string) (*oldCore, error) {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, fmt.Errorf("create the old core's home: %w", err)
	}

	// gosec G204: the binary is an operator-supplied path to their own installed application, and
	// the arguments are two literals. This is a developer tool run by hand, not a shipped surface.
	cmd := exec.CommandContext(ctx, binary, "--app-version", "2.7.1") //nolint:gosec
	cmd.Env = environmentWithHome(os.Environ(), home)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open the core's stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open the core's stdout: %w", err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", binary, err)
	}

	core := &oldCore{cmd: cmd, pending: map[int]json.RawMessage{}}

	// The handshake token, as one line, then the pipe is closed. The core reads that line before
	// anything else and exits 2 without it.
	token := uuid.NewString()
	if _, err := io.WriteString(stdin, token+"\n"); err != nil {
		core.stop()
		return nil, fmt.Errorf("write the handshake token: %w", err)
	}
	if err := stdin.Close(); err != nil {
		core.stop()
		return nil, fmt.Errorf("close the core's stdin: %w", err)
	}

	endpoint, err := awaitReady(ctx, stdout)
	if err != nil {
		core.stop()
		return nil, err
	}

	// Through a Dialer so the context governs it: a core that announced an endpoint it is not yet
	// listening on would otherwise hang a Ctrl-C.
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", endpoint)
	if err != nil {
		core.stop()
		return nil, fmt.Errorf("dial %s: %w", endpoint, err)
	}
	core.conn = conn

	// The hello frame identifies the channel. `rpc` carries requests and replies; `stream` carries
	// events, which this oracle does not compare.
	if err := core.write(map[string]string{"channel": "rpc", "token": token}); err != nil {
		core.stop()
		return nil, fmt.Errorf("send the hello frame: %w", err)
	}
	return core, nil
}

// awaitReady reads the core's stdout until it announces its endpoint.
//
// Anything else the core prints is forwarded, because a core that is about to fail usually says so
// on the line before.
func awaitReady(ctx context.Context, stdout io.Reader) (string, error) {
	type line struct {
		text string
		err  error
	}
	lines := make(chan line, 1)

	// A plain goroutine rather than safego.Go: this is package main in a tool, outside the
	// application whose panic handler safego exists to feed, and the CI gate scans `backend` only.
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			text := scanner.Text()
			if strings.Contains(text, "ready") {
				lines <- line{text: text}
				return
			}
			fmt.Fprintln(os.Stderr, "[old core] "+text)
		}
		lines <- line{err: errors.New("the core exited before it was ready")}
	}()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(readyTimeout):
		return "", fmt.Errorf("the core did not become ready within %s", readyTimeout)
	case got := <-lines:
		if got.err != nil {
			return "", got.err
		}
		fields := strings.Fields(got.text)
		if len(fields) == 0 {
			return "", fmt.Errorf("unreadable ready line: %q", got.text)
		}
		return fields[len(fields)-1], nil
	}
}

// Call sends one request and returns its reply, as the raw result or the verbatim error message.
func (c *oldCore) Call(method string, params map[string]any) (json.RawMessage, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.mu.Unlock()

	request := map[string]any{"id": id, "method": method}
	if params != nil {
		request["params"] = params
	}
	if err := c.write(request); err != nil {
		return nil, fmt.Errorf("send %s: %w", method, err)
	}

	if err := c.conn.SetReadDeadline(time.Now().Add(callTimeout)); err != nil {
		return nil, fmt.Errorf("set a deadline for %s: %w", method, err)
	}

	// The pump does not await handlers, so replies may in principle arrive out of order. This
	// oracle sends one at a time, but matching by id costs nothing and keeps that fact stated.
	for {
		frame, err := c.read()
		if err != nil {
			return nil, fmt.Errorf("read the reply to %s: %w", method, err)
		}

		var reply struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *string         `json:"error"`
		}
		if err := json.Unmarshal(frame, &reply); err != nil {
			return nil, fmt.Errorf("decode the reply to %s: %w", method, err)
		}
		if reply.ID != id {
			continue
		}
		if reply.Error != nil {
			return nil, errors.New(*reply.Error)
		}
		if reply.Result == nil {
			return json.RawMessage("null"), nil
		}
		return reply.Result, nil
	}
}

func (c *oldCore) write(payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode the frame: %w", err)
	}
	if len(body) > frameMax {
		return fmt.Errorf("the frame is %d bytes, past the transport's %d", len(body), frameMax)
	}

	header := make([]byte, 4)
	// gosec G115: bounded by the check immediately above — a frame past frameMax (64 MiB) never
	// reaches here, and uint32 holds four times that.
	binary.LittleEndian.PutUint32(header, uint32(len(body))) //nolint:gosec
	if _, err := c.conn.Write(append(header, body...)); err != nil {
		return fmt.Errorf("write the frame: %w", err)
	}
	return nil
}

func (c *oldCore) read() ([]byte, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(c.conn, header); err != nil {
		return nil, err
	}

	length := binary.LittleEndian.Uint32(header)
	if length > frameMax {
		return nil, fmt.Errorf("the frame announces %d bytes, past the transport's %d", length, frameMax)
	}

	body := make([]byte, length)
	if _, err := io.ReadFull(c.conn, body); err != nil {
		return nil, err
	}
	return body, nil
}

// stop closes the rpc channel, which is how the core is asked to exit, and kills it if it does not.
func (c *oldCore) stop() {
	if c.conn != nil {
		_ = c.conn.Close()
	}
	if c.cmd == nil || c.cmd.Process == nil {
		return
	}

	done := make(chan struct{})
	//nolint:errcheck // the exit status of a core being torn down is not a result.
	go func() { _ = c.cmd.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		// The same SIGKILL grace the 2.x shell used.
		_ = c.cmd.Process.Kill()
		<-done
	}
}

// environmentWithHome is the inherited environment with HOME replaced rather than appended.
//
// Appended would be ambiguous: execve passes the block through unchanged and which duplicate wins
// is the C library's business, not something to rely on when the whole point is that the old core
// must not find the real home directory.
func environmentWithHome(inherited []string, home string) []string {
	out := make([]string, 0, len(inherited)+1)
	for _, entry := range inherited {
		if name, _, found := strings.Cut(entry, "="); found && name == "HOME" {
			continue
		}
		out = append(out, entry)
	}
	return append(out, "HOME="+home)
}
