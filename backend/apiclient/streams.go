package apiclient

import (
	"context"
	"sync"
	"time"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/safego"
)

// The live-connection registry and the two events every transport reports through (API-060).
//
// Kept apart from `Cancels` on purpose: an in-flight send lives for one exchange and a socket lives
// for as long as somebody is watching it, so one map holding both would need a discriminator that
// nothing else would ever read.

// The two event names the renderer listens on, `VERBATIM` (`frontend/src/lib/ipc/events.ts`).
const (
	EventStreamMessage = "api:stream-message"
	EventStreamStatus  = "api:stream-status"
)

// The four directions a transcript line can have, `VERBATIM`.
const (
	DirectionSent     = "sent"
	DirectionReceived = "received"
	DirectionSystem   = "system"
	DirectionError    = "error"
)

// The four statuses, `VERBATIM`.
const (
	StatusConnecting = "connecting"
	StatusOpen       = "open"
	StatusClosed     = "closed"
	StatusError      = "error"
)

// StreamMessage is one line of a connection's transcript.
//
// Every frame in either direction becomes one, and so does every keepalive tick: the panel has no
// other way to inspect a live connection's history, so a frame that is not reported here did not
// happen as far as the user can tell.
type StreamMessage struct {
	ConnectionID string `json:"connection_id"`
	Direction    string `json:"direction"`
	// Channel is the Socket.IO event name, the MQTT topic, or empty.
	Channel string `json:"channel"`
	Payload string `json:"payload"`
	// Binary marks a payload that is base64 of a binary frame.
	Binary bool `json:"binary"`
	// At is Unix milliseconds.
	At int64 `json:"at"`
	// QoS and Retain are MQTT's alone, and absent everywhere else — hence `omitempty`, which is
	// what makes the renderer's `qos?: number` read as undefined rather than as a zero.
	QoS    *uint8 `json:"qos,omitempty"`
	Retain *bool  `json:"retain,omitempty"`
}

// StreamStatusEvent is a connection changing state.
type StreamStatusEvent struct {
	ConnectionID string `json:"connection_id"`
	Status       string `json:"status"`
	Detail       string `json:"detail"`
}

// WsConnectRequest opens a raw WebSocket.
type WsConnectRequest struct {
	URL          string      `json:"url"`
	Headers      [][2]string `json:"headers"`
	Subprotocols []string    `json:"subprotocols"`
	// PingIntervalMs of 0 disables keepalive entirely.
	PingIntervalMs uint64         `json:"ping_interval_ms"`
	Options        NetworkOptions `json:"options"`
}

// SocketIoConnectRequest opens a Socket.IO session over its own WebSocket.
type SocketIoConnectRequest struct {
	URL  string `json:"url"`
	Path string `json:"path"`
	// Namespace is the Socket.IO namespace; empty or "/" is the root one.
	Namespace string `json:"namespace"`
	// Version is `v4` (Socket.IO 3/4) or `v3` (Socket.IO 2).
	Version string      `json:"version"`
	Headers [][2]string `json:"headers"`
	// AuthJSON is the JSON object sent in the CONNECT packet, on v4 only.
	AuthJSON string         `json:"auth_json"`
	Query    [][2]string    `json:"query"`
	Options  NetworkOptions `json:"options"`
}

// ---- the registry ------------------------------------------------------------------------------

// connection is one live socket, as the registry holds it: a channel the command side writes into,
// a cancel that tears the pump down, and which transport it is.
//
// The transport is recorded so a command aimed at the wrong kind of connection is **refused** rather
// than quietly doing something adjacent: `api_ws_send` on a Socket.IO session would otherwise put a
// raw frame on a wire that expects Engine.IO framing, and the server would drop it with nobody the
// wiser.
type connection struct {
	commands chan streamCommand
	cancel   context.CancelFunc
	// done is closed by the pump on its way out, so a close can tell "it shut down cleanly" from
	// "it is wedged" instead of guessing.
	done      chan struct{}
	closeOnce sync.Once
	transport string
}

// finished is what a pump calls on its way out, exactly once however many goroutines it had.
func (c *connection) finished() { c.closeOnce.Do(func() { close(c.done) }) }

// The three transports a connection can be.
const (
	transportWS       = "websocket"
	transportSocketIO = "socket.io"
	transportMQTT     = "mqtt"
)

// accepts reports whether a command kind belongs to this transport.
func (c *connection) accepts(kind string) bool {
	if kind == commandClose {
		return true
	}
	switch c.transport {
	case transportWS:
		return kind == commandSend
	case transportSocketIO:
		return kind == commandEmit
	case transportMQTT:
		return kind == commandPublish || kind == commandSubscribe || kind == commandUnsubscribe
	default:
		return false
	}
}

// streamCommand is what a command posts to a live connection's pump.
//
// One type for all three transports rather than one each: they differ in what they put on the wire,
// not in what the UI asks of them, and a per-transport command type would be three enums the
// registry would have to tell apart before it could deliver anything.
type streamCommand struct {
	kind    string
	payload string
	channel string
	binary  bool
	// qos and retain are MQTT's alone; every other transport leaves them at zero and never reads
	// them.
	qos    byte
	retain bool
}

// millis turns a wire-supplied millisecond count into a duration, bounded.
//
// The bound is not a rule from the specification: the value crosses from JSON as a `uint64` and is
// multiplied by a million to become a duration, which a large enough number turns negative — a
// timer that then fires immediately and for ever, or a deadline already in the past. A day is
// further than any of these are meant to reach.
func millis(value uint64) time.Duration {
	const day = 24 * 60 * 60 * 1000
	if value > day {
		value = day
	}
	return time.Duration(value) * time.Millisecond //nolint:gosec // G115: bounded just above
}

// The command kinds.
const (
	commandSend        = "send"
	commandEmit        = "emit"
	commandPublish     = "publish"
	commandSubscribe   = "subscribe"
	commandUnsubscribe = "unsubscribe"
	commandClose       = "close"
)

// Streams is every live connection, and the emitter they all report through.
type Streams struct {
	mutex sync.Mutex
	live  map[string]*connection

	emitter bridge.Emitter
}

// NewStreams wires the registry to the emitter every transcript line goes out through.
func NewStreams(emitter bridge.Emitter) *Streams {
	if emitter == nil {
		emitter = bridge.NopEmitter{}
	}
	return &Streams{live: make(map[string]*connection, 4), emitter: emitter}
}

// register inserts a connection **before** the socket is dialled.
//
// That ordering is the rule, not an implementation detail: a `send` issued the instant `connect`
// returns — or while the handshake is still in flight — has to queue in the channel instead of
// failing with "no open connection", which is exactly what a panel that connects and immediately
// emits would otherwise do.
//
// A second connect under a live id closes the first: the renderer mints one id per connection, so
// a collision is a socket nobody is watching any more.
//
// `context.WithoutCancel` is what detaches the pump from the command that opened it — the same
// idiom a terminal session uses for its shell. The context a command is handed dies the moment the
// command returns, which for `connect` is milliseconds after the handshake, and a pump on it would
// close the socket it had just opened.
func (s *Streams) register(ctx context.Context, id, transport string) (context.Context, *connection) {
	running, cancel := context.WithCancel(context.WithoutCancel(ctx))
	entry := &connection{
		commands:  make(chan streamCommand, 64),
		cancel:    cancel,
		done:      make(chan struct{}),
		transport: transport,
	}

	s.mutex.Lock()
	if previous, found := s.live[id]; found {
		previous.cancel()
	}
	s.live[id] = entry
	s.mutex.Unlock()

	return running, entry
}

// unregister removes a connection, but **only if it is still the same one**.
//
// The guard matters on a teardown that races a reconnect under the same id: without it, the dying
// pump's cleanup would remove the entry the fresh connection had just installed, and the new socket
// would be live but unreachable.
func (s *Streams) unregister(id string, entry *connection) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.live[id] == entry {
		delete(s.live, id)
	}
}

// Close asks a connection to shut down, and is **safe on an unknown id**.
//
// A documented no-op rather than an error: a disconnect can legitimately race a connection that
// already died on its own, and an error there would show a failure for a button that did the right
// thing.
func (s *Streams) Close(id string) {
	s.mutex.Lock()
	entry, found := s.live[id]
	if found {
		delete(s.live, id)
	}
	s.mutex.Unlock()

	if !found {
		return
	}

	// The close request first, so the pump can put a protocol-level goodbye on the wire: a
	// WebSocket close frame, a Socket.IO DISCONNECT, an MQTT DISCONNECT. **Cancelling straight
	// away would race that**, and on MQTT losing the race is not cosmetic — a broker that sees the
	// socket drop without a goodbye publishes the last will, which is a message on other people's
	// dashboards announcing a client that in fact shut down cleanly.
	select {
	case entry.commands <- streamCommand{kind: commandClose}:
	default:
		// A full channel must not block the caller; the backstop below ends it either way.
	}

	// The cancel is the backstop for a pump wedged on a read, and it waits for the grace period
	// first. The pump's own teardown cancels too, so this only ever fires for one that did not
	// finish.
	safego.Go("stream-close-"+id, func() {
		select {
		case <-entry.done:
		case <-time.After(closeGrace):
		}
		entry.cancel()
	})
}

// closeGrace is how long a pump gets to say goodbye before the socket is torn out from under it.
const closeGrace = 2 * time.Second

// Send queues one raw WebSocket frame. `binary` says the payload is base64 of the bytes to send.
func (s *Streams) Send(id, payload string, binary bool) error {
	return s.send(id, streamCommand{kind: commandSend, payload: payload, binary: binary})
}

// Emit queues one Socket.IO event. The payload is a JSON value the caller already produced, and it
// is validated on the pump rather than here — a malformed one surfaces as an error status, which is
// where every other streaming failure surfaces too.
func (s *Streams) Emit(id, event, payloadJSON string) error {
	return s.send(id, streamCommand{kind: commandEmit, channel: event, payload: payloadJSON})
}

// Publish queues one MQTT message.
func (s *Streams) Publish(id, topic, payload string, qos byte, retain bool) error {
	return s.send(id, streamCommand{
		kind: commandPublish, channel: topic, payload: payload, qos: qos, retain: retain,
	})
}

// Subscribe queues one MQTT subscription.
func (s *Streams) Subscribe(id, topic string, qos byte) error {
	return s.send(id, streamCommand{kind: commandSubscribe, channel: topic, qos: qos})
}

// Unsubscribe queues one MQTT unsubscription.
func (s *Streams) Unsubscribe(id, topic string) error {
	return s.send(id, streamCommand{kind: commandUnsubscribe, channel: topic})
}

// send posts a command to a live connection.
//
// The channel is **taken out from under the lock** before anything is written to it: a full channel
// would otherwise block while still holding the lock every other command needs, which is one slow
// consumer away from freezing the whole panel.
func (s *Streams) send(id string, command streamCommand) error {
	s.mutex.Lock()
	entry, found := s.live[id]
	s.mutex.Unlock()

	if !found {
		return errNoConnection(id)
	}
	if !entry.accepts(command.kind) {
		return errWrongTransport(id, command.kind, entry.transport)
	}

	select {
	case entry.commands <- command:
		return nil
	case <-time.After(5 * time.Second):
		// A pump that has not drained its queue in five seconds is wedged, and saying so beats
		// blocking the caller's command for ever.
		return errConnectionBusy(id)
	}
}

// ---- reporting ---------------------------------------------------------------------------------

// reporter is one connection's view of the emitter: it stamps its own id and the clock onto every
// line, so no pump has to remember to.
type reporter struct {
	id      string
	emitter bridge.Emitter
	now     func() time.Time
}

func (s *Streams) reporterFor(id string) reporter {
	return reporter{id: id, emitter: s.emitter, now: time.Now}
}

func (r reporter) message(direction, channel, payload string, binary bool) {
	if r.emitter == nil {
		return
	}
	r.emitter.Emit(EventStreamMessage, StreamMessage{
		ConnectionID: r.id,
		Direction:    direction,
		Channel:      channel,
		Payload:      payload,
		Binary:       binary,
		At:           r.now().UnixMilli(),
	})
}

// system is the transcript line for something the transport did rather than something that was
// said: a ping, an upgrade, an option that was ignored.
func (r reporter) system(detail string) { r.message(DirectionSystem, "", detail, false) }

func (r reporter) failure(detail string) { r.message(DirectionError, "", detail, false) }

func (r reporter) status(status, detail string) {
	if r.emitter == nil {
		return
	}
	r.emitter.Emit(EventStreamStatus, StreamStatusEvent{
		ConnectionID: r.id, Status: status, Detail: detail,
	})
}
