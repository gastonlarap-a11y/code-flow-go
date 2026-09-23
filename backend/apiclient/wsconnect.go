package apiclient

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/safego"
)

// The raw WebSocket transport (API-025…029).
//
// The pump **outlives the command that opened it**: `connect` registers, dials, starts the pump and
// returns, and the pump then reads frames and emits transcript lines for as long as the socket
// lives. That is why the registry carries an application-lifetime context — the one a command is
// handed dies the moment the command returns, which here is milliseconds after the handshake.

// readLimit is how large a single incoming frame may be. The library's own default is 32 KiB, which
// quietly kills any realistic payload an API console is pointed at.
const readLimit = 16 * 1024 * 1024

// The two refusals a command can get for an id.
func errNoConnection(id string) error {
	return fmt.Errorf("no open connection %s", id) //nolint:err113 // names the id the caller sent
}

func errConnectionBusy(id string) error {
	return fmt.Errorf("connection %s is not draining its queue", id) //nolint:err113 // names the id
}

func errWrongTransport(id, kind, transport string) error {
	return fmt.Errorf("%s is a %s connection and cannot %s", id, transport, kind) //nolint:err113 // names all three
}

var errEmptySocketIOPacket = errors.New("empty Socket.IO packet")

// normalizeScheme maps an http(s) URL onto its WebSocket twin (API-025).
//
// Case-insensitive on the **scheme only**: everything after it, case included, is preserved
// verbatim, because a path or a query is not this function's to normalise.
func normalizeScheme(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)

	for prefix, replacement := range map[string]string{"https://": "wss://", "http://": "ws://"} {
		if len(trimmed) >= len(prefix) && strings.EqualFold(trimmed[:len(prefix)], prefix) {
			return replacement + trimmed[len(prefix):]
		}
	}
	// Already `ws`/`wss`, or something else entirely — which surfaces as a dial error rather than
	// being guessed at here.
	return trimmed
}

// upgradeHeaders merges the caller's rows onto the handshake's own (API-026).
//
// **The first row for a name replaces, a repeat appends.** The asymmetry is the rule: the first row
// is the caller overriding something the handshake generated — `Host`, `Origin` — and a second row
// with the same name was typed twice on purpose, so it adds rather than overwrites.
func upgradeHeaders(rows [][2]string, subprotocols []string) http.Header {
	header := http.Header{}
	seen := make(map[string]bool, len(rows))

	for _, row := range rows {
		name := strings.TrimSpace(row[0])
		if name == "" {
			continue
		}
		canonical := http.CanonicalHeaderKey(name)

		if seen[canonical] {
			header.Add(canonical, row[1])
			continue
		}
		header.Set(canonical, row[1])
		seen[canonical] = true
	}
	_ = subprotocols // carried on the dial options instead, where the library owns the header
	return header
}

// cleanSubprotocols trims and drops the empties, so a list of blanks becomes no header at all
// rather than an empty one.
func cleanSubprotocols(subprotocols []string) []string {
	cleaned := make([]string, 0, len(subprotocols))
	for _, protocol := range subprotocols {
		if trimmed := strings.TrimSpace(protocol); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	return cleaned
}

// dialOptions builds what the handshake needs, TLS included (API-027).
func dialOptions(request WsConnectRequest) *websocket.DialOptions {
	options := &websocket.DialOptions{
		HTTPHeader:   upgradeHeaders(request.Headers, request.Subprotocols),
		Subprotocols: cleanSubprotocols(request.Subprotocols),
	}

	if !request.Options.VerifySSL {
		// Accepts any chain and any name — but the handshake **signature** is still verified by the
		// standard library's own crypto, because `InsecureSkipVerify` disables identity checking
		// and nothing else. A genuinely broken peer still fails, which is the distinction the
		// original draws and the one MQTT's equivalent loses (`BUG-API-d`).
		options.HTTPClient = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true, //nolint:gosec // G402: the user asked, per connection
					MinVersion:         tls.VersionTLS12,
				},
			},
		}
	}
	return options
}

// closeHandshakeResponse releases the body a **failed** handshake carries.
//
// A successful upgrade hands back a response whose body the library owns, and a failed one hands
// back an ordinary HTTP response that nothing else will read — leaked, that is one held connection
// per failed connect, in a process a user reconnects from repeatedly while a server is down.
func closeHandshakeResponse(response *http.Response) {
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
}

// ConnectWebSocket opens a socket and starts its pump (API-025…029).
//
// Registration happens **before** the dial, so a `send` issued the instant this returns — or while
// the handshake is still in flight — queues in the channel instead of failing with "no open
// connection".
func (s *Streams) ConnectWebSocket(ctx context.Context, id string, request WsConnectRequest) error {
	request.Options = request.Options.withDefaults()
	running, entry := s.register(ctx, id, transportWS)
	report := s.reporterFor(id)

	report.status(StatusConnecting, "")

	target := normalizeScheme(request.URL)

	// The handshake gets its own deadline; the socket that comes out of it does not, because a
	// connection nobody is writing to is not a failure.
	handshake, cancelHandshake := context.WithTimeout(running, millis(request.Options.TimeoutMs))
	socket, response, err := websocket.Dial(handshake, target, dialOptions(request))
	cancelHandshake()
	closeHandshakeResponse(response)

	if err != nil {
		s.unregister(id, entry)
		entry.cancel()
		detail := fmt.Sprintf("couldn't open %s: %v", target, err)
		report.failure(detail)
		report.status(StatusError, detail)
		return errors.New(detail) //nolint:err113 // the message is what the panel shows
	}
	socket.SetReadLimit(readLimit)

	report.status(StatusOpen, "")

	safego.Go("ws-pump-"+id, func() {
		s.pumpWebSocket(running, id, entry, socket, request.PingIntervalMs, report)
	})
	return nil
}

// pumpWebSocket reads frames and writes commands until one side stops (API-029).
//
// Reading and writing are two goroutines because the library allows exactly one concurrent reader
// and one concurrent writer, and a single loop selecting over both would have to poll — which on a
// socket that says nothing for an hour means an hour of wasted wakeups.
func (s *Streams) pumpWebSocket(
	ctx context.Context,
	id string,
	entry *connection,
	socket *websocket.Conn,
	pingIntervalMs uint64,
	report reporter,
) {
	status, detail := StatusClosed, "Closed by client"
	defer func() {
		// Always in this order: the socket first so the far side learns, then the registry entry,
		// then the status — a panel that saw `closed` before the entry was gone could reconnect
		// into a slot the dying pump was about to clear.
		_ = socket.Close(websocket.StatusNormalClosure, "")
		s.unregister(id, entry)
		entry.finished()
		entry.cancel()
		report.status(status, detail)
	}()

	frames := readFrames(ctx, socket)
	ticker := keepalive(pingIntervalMs)
	if ticker != nil {
		defer ticker.Stop()
	}

	for {
		var tick <-chan time.Time
		if ticker != nil {
			tick = ticker.C
		}

		select {
		case <-ctx.Done():
			return

		case command, open := <-entry.commands:
			if !open || command.kind == commandClose {
				return
			}
			if err := writeFrame(ctx, socket, command); err != nil {
				status, detail = StatusError, err.Error()
				report.failure(detail)
				return
			}
			report.message(DirectionSent, "", command.payload, command.binary)

		case frame, open := <-frames:
			if !open {
				return
			}
			keepGoing, frameStatus, frameDetail := s.handleFrame(frame, report)
			if !keepGoing {
				status, detail = frameStatus, frameDetail
				return
			}

		case <-tick:
			if err := socket.Ping(ctx); err != nil {
				status, detail = StatusError, err.Error()
				report.failure(detail)
				return
			}
			report.system("ping sent")
		}
	}
}

// incoming is one frame, or the error that ended the read loop.
type incoming struct {
	kind websocket.MessageType
	data []byte
	err  error
}

// readFrames turns the blocking reader into a channel the pump can select on.
func readFrames(ctx context.Context, socket *websocket.Conn) <-chan incoming {
	frames := make(chan incoming, 8)

	safego.Go("ws-reader", func() {
		defer close(frames)
		for {
			kind, data, err := socket.Read(ctx)
			select {
			case frames <- incoming{kind: kind, data: data, err: err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	})
	return frames
}

// handleFrame maps one frame onto the transcript (API-029).
//
// A received `Ping` is **logged and not answered by hand**: the library queues and flushes its own
// `Pong`, and a manual reply would replace that queued frame rather than add to it.
func (s *Streams) handleFrame(frame incoming, report reporter) (keepGoing bool, status, detail string) {
	if frame.err != nil {
		status, detail = closeReason(frame.err)
		return false, status, detail
	}

	if frame.kind == websocket.MessageBinary {
		report.message(DirectionReceived, "", base64.StdEncoding.EncodeToString(frame.data), true)
		return true, "", ""
	}
	report.message(DirectionReceived, "", string(frame.data), false)
	return true, "", ""
}

// closeReason tells a clean close from a failure, and carries the far side's own words when it gave
// any — `"Closed by server"` is what a close frame with no reason reads as.
func closeReason(err error) (status, detail string) {
	if closed, ok := errors.AsType[websocket.CloseError](err); ok {
		reason := closed.Reason
		if strings.TrimSpace(reason) == "" {
			reason = "Closed by server"
		}
		return StatusClosed, reason
	}
	if errors.Is(err, context.Canceled) {
		return StatusClosed, "Closed by client"
	}
	return StatusError, err.Error()
}

// writeFrame puts one queued command on the wire.
func writeFrame(ctx context.Context, socket *websocket.Conn, command streamCommand) error {
	if command.binary {
		raw, err := base64.StdEncoding.DecodeString(command.payload)
		if err != nil {
			return fmt.Errorf("the binary frame is not base64: %w", err)
		}
		return socket.Write(ctx, websocket.MessageBinary, raw)
	}
	return socket.Write(ctx, websocket.MessageText, []byte(command.payload))
}

// keepalive schedules the pings (API-028).
//
// `ping_interval_ms == 0` disables them entirely. Otherwise the first one fires **one full period
// after** the handshake rather than immediately: a ticker that fired at once would ping in the same
// millisecond as the connection opened, which is pure noise in the transcript.
func keepalive(intervalMs uint64) *time.Ticker {
	if intervalMs == 0 {
		return nil
	}
	return time.NewTicker(millis(intervalMs))
}
