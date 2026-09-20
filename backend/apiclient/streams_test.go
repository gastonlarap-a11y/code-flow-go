package apiclient_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/apiclient"
	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
)

// The streaming transports against real sockets. A transport tested through a fake of itself tests
// the fake, so every server here speaks the actual protocol.

// transcript collects what the pumps emitted, which is the only place a live connection's history
// exists.
type transcript struct {
	mutex    sync.Mutex
	messages []apiclient.StreamMessage
	statuses []apiclient.StreamStatusEvent
}

func (t *transcript) emitter() bridge.Emitter {
	return bridge.EmitterFunc(func(name string, payload any) {
		t.mutex.Lock()
		defer t.mutex.Unlock()

		switch event := payload.(type) {
		case apiclient.StreamMessage:
			t.messages = append(t.messages, event)
		case apiclient.StreamStatusEvent:
			t.statuses = append(t.statuses, event)
		}
		_ = name
	})
}

func (t *transcript) lines() []apiclient.StreamMessage {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	return append([]apiclient.StreamMessage{}, t.messages...)
}

func (t *transcript) events() []apiclient.StreamStatusEvent {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	return append([]apiclient.StreamStatusEvent{}, t.statuses...)
}

// waitForMessage blocks until a transcript line satisfies `matches`, or the test gives up.
func (t *transcript) waitForMessage(tb testing.TB, what string, matches func(apiclient.StreamMessage) bool) apiclient.StreamMessage {
	tb.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, line := range t.lines() {
			if matches(line) {
				return line
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	tb.Fatalf("no transcript line for %s; saw %+v", what, t.lines())
	return apiclient.StreamMessage{}
}

func (t *transcript) waitForStatus(tb testing.TB, status string) apiclient.StreamStatusEvent {
	tb.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, event := range t.events() {
			if event.Status == status {
				return event
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	tb.Fatalf("no %q status; saw %+v", status, t.events())
	return apiclient.StreamStatusEvent{}
}

func wsOptions() apiclient.NetworkOptions {
	return apiclient.NetworkOptions{TimeoutMs: 5_000, VerifySSL: true}
}

// ---- raw WebSocket -------------------------------------------------------------------------------

func TestAWebSocketEchoesThroughTheTranscript(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		socket, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = socket.CloseNow() }()

		for {
			kind, data, err := socket.Read(r.Context())
			if err != nil {
				return
			}
			if err := socket.Write(r.Context(), kind, data); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	log := &transcript{}
	streams := apiclient.NewStreams(log.emitter())

	require.NoError(t, streams.ConnectWebSocket(t.Context(), "c1", apiclient.WsConnectRequest{
		URL: server.URL, Options: wsOptions(),
	}))
	defer streams.Close("c1")

	log.waitForStatus(t, apiclient.StatusOpen)

	require.NoError(t, streams.Send("c1", "hola", false))

	sent := log.waitForMessage(t, "the sent line", func(line apiclient.StreamMessage) bool {
		return line.Direction == apiclient.DirectionSent
	})
	assert.Equal(t, "hola", sent.Payload)
	assert.Equal(t, "c1", sent.ConnectionID)
	assert.Positive(t, sent.At)

	received := log.waitForMessage(t, "the echo", func(line apiclient.StreamMessage) bool {
		return line.Direction == apiclient.DirectionReceived
	})
	assert.Equal(t, "hola", received.Payload)
	assert.False(t, received.Binary)
}

func TestABinaryFrameTravelsAsBase64InBothDirections(t *testing.T) {
	raw := []byte{0x00, 0xff, 0x10}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		socket, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = socket.CloseNow() }()

		kind, data, err := socket.Read(r.Context())
		if err != nil {
			return
		}
		// What arrived must be the decoded bytes, not their base64.
		if string(data) != string(raw) || kind != websocket.MessageBinary {
			_ = socket.Write(r.Context(), websocket.MessageText, []byte("wrong"))
			return
		}
		_ = socket.Write(r.Context(), websocket.MessageBinary, data)
	}))
	defer server.Close()

	log := &transcript{}
	streams := apiclient.NewStreams(log.emitter())

	require.NoError(t, streams.ConnectWebSocket(t.Context(), "c1", apiclient.WsConnectRequest{
		URL: server.URL, Options: wsOptions(),
	}))
	defer streams.Close("c1")
	log.waitForStatus(t, apiclient.StatusOpen)

	require.NoError(t, streams.Send("c1", base64.StdEncoding.EncodeToString(raw), true))

	received := log.waitForMessage(t, "the binary echo", func(line apiclient.StreamMessage) bool {
		return line.Direction == apiclient.DirectionReceived
	})
	assert.True(t, received.Binary)
	assert.Equal(t, base64.StdEncoding.EncodeToString(raw), received.Payload)
}

// Registration happens **before** the dial, so a send issued the instant connect returns queues in
// the channel instead of failing with "no open connection".
func TestASendIssuedImmediatelyAfterConnectIsQueuedNotRefused(t *testing.T) {
	arrived := make(chan string, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		socket, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = socket.CloseNow() }()

		_, data, err := socket.Read(r.Context())
		if err != nil {
			return
		}
		arrived <- string(data)
	}))
	defer server.Close()

	log := &transcript{}
	streams := apiclient.NewStreams(log.emitter())

	require.NoError(t, streams.ConnectWebSocket(t.Context(), "c1", apiclient.WsConnectRequest{
		URL: server.URL, Options: wsOptions(),
	}))
	defer streams.Close("c1")

	// No wait for `open` at all: this is the panel that connects and emits in the same handler.
	require.NoError(t, streams.Send("c1", "temprano", false))

	select {
	case got := <-arrived:
		assert.Equal(t, "temprano", got)
	case <-time.After(5 * time.Second):
		t.Fatal("the queued frame never arrived")
	}
}

func TestSendingToAnUnknownConnectionSaysSo(t *testing.T) {
	streams := apiclient.NewStreams(nil)

	err := streams.Send("no-existe", "hola", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no open connection no-existe")
}

// A command aimed at the wrong kind of connection is refused rather than quietly doing something
// adjacent: a raw frame on a wire expecting Engine.IO framing is dropped by the server with nobody
// the wiser.
func TestACommandForTheWrongTransportIsRefused(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		socket, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = socket.CloseNow() }()
		<-r.Context().Done()
	}))
	defer server.Close()

	log := &transcript{}
	streams := apiclient.NewStreams(log.emitter())

	require.NoError(t, streams.ConnectWebSocket(t.Context(), "c1", apiclient.WsConnectRequest{
		URL: server.URL, Options: wsOptions(),
	}))
	defer streams.Close("c1")
	log.waitForStatus(t, apiclient.StatusOpen)

	err := streams.Emit("c1", "mensaje", `{"a":1}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is a websocket connection and cannot emit")

	// And the raw send it *can* do still works.
	assert.NoError(t, streams.Send("c1", "hola", false))
}

// A close from the far side carries the server's own reason, and `"Closed by server"` when it gave
// none.
func TestACloseFromTheServerCarriesItsReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		socket, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		_ = socket.Close(websocket.StatusGoingAway, "el servidor se va")
	}))
	defer server.Close()

	log := &transcript{}
	streams := apiclient.NewStreams(log.emitter())

	require.NoError(t, streams.ConnectWebSocket(t.Context(), "c1", apiclient.WsConnectRequest{
		URL: server.URL, Options: wsOptions(),
	}))

	closed := log.waitForStatus(t, apiclient.StatusClosed)
	assert.Equal(t, "el servidor se va", closed.Detail)
}

// `API-038`: nothing reconnects. A dropped connection stays closed until the user asks again —
// silent reconnection would hide exactly the instability this tool exists to reveal.
func TestNothingReconnects(t *testing.T) {
	var dials int
	var mutex sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mutex.Lock()
		dials++
		mutex.Unlock()

		socket, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		_ = socket.Close(websocket.StatusNormalClosure, "")
	}))
	defer server.Close()

	log := &transcript{}
	streams := apiclient.NewStreams(log.emitter())

	require.NoError(t, streams.ConnectWebSocket(t.Context(), "c1", apiclient.WsConnectRequest{
		URL: server.URL, Options: wsOptions(),
	}))
	log.waitForStatus(t, apiclient.StatusClosed)

	time.Sleep(300 * time.Millisecond)

	mutex.Lock()
	defer mutex.Unlock()
	assert.Equal(t, 1, dials, "one dial, and no second one on its own")
}

func TestClosingIsSafeOnAnUnknownID(t *testing.T) {
	streams := apiclient.NewStreams(nil)

	// A documented no-op: a disconnect can legitimately race a connection that already died.
	assert.NotPanics(t, func() { streams.Close("nunca-existió") })
	assert.NotPanics(t, func() { streams.Close("") })
}

func TestAFailedHandshakeReportsAnErrorStatusAndFreesTheID(t *testing.T) {
	log := &transcript{}
	streams := apiclient.NewStreams(log.emitter())

	err := streams.ConnectWebSocket(t.Context(), "c1", apiclient.WsConnectRequest{
		URL: "ws://127.0.0.1:1/nope", Options: apiclient.NetworkOptions{TimeoutMs: 500, VerifySSL: true},
	})
	require.Error(t, err)

	failed := log.waitForStatus(t, apiclient.StatusError)
	assert.Contains(t, failed.Detail, "couldn't open")

	// The id is free again, so a retry under the same one is a fresh connection rather than a
	// collision with a corpse.
	assert.ErrorContains(t, streams.Send("c1", "hola", false), "no open connection")
}

// The pump outlives the command that opened it: a context cancelled the way Wails cancels one must
// not take the socket with it.
func TestThePumpOutlivesTheCommandsContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		socket, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = socket.CloseNow() }()

		for {
			kind, data, err := socket.Read(r.Context())
			if err != nil {
				return
			}
			if err := socket.Write(r.Context(), kind, data); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	log := &transcript{}
	streams := apiclient.NewStreams(log.emitter())

	commandCtx, cancelCommand := context.WithCancel(t.Context())
	require.NoError(t, streams.ConnectWebSocket(commandCtx, "c1", apiclient.WsConnectRequest{
		URL: server.URL, Options: wsOptions(),
	}))
	defer streams.Close("c1")

	log.waitForStatus(t, apiclient.StatusOpen)
	// Exactly what the bridge does the moment `api_ws_connect` returns.
	cancelCommand()

	require.NoError(t, streams.Send("c1", "sigo vivo", false))
	received := log.waitForMessage(t, "the echo after the command returned",
		func(line apiclient.StreamMessage) bool {
			return line.Direction == apiclient.DirectionReceived
		})
	assert.Equal(t, "sigo vivo", received.Payload)
}

// ---- Socket.IO -----------------------------------------------------------------------------------

// socketIOServer speaks enough of the protocol to exercise the sequencing: an Engine.IO OPEN, a
// Socket.IO CONNECT reply, and whatever the test's own handler does with the frames after that.
func socketIOServer(t *testing.T, handle func(socket *websocket.Conn, frame string) []string) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		socket, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = socket.CloseNow() }()

		open := `0{"sid":"el-sid","pingInterval":25000,"pingTimeout":20000}`
		if err := socket.Write(r.Context(), websocket.MessageText, []byte(open)); err != nil {
			return
		}

		for {
			_, data, err := socket.Read(r.Context())
			if err != nil {
				return
			}
			for _, reply := range handle(socket, string(data)) {
				if err := socket.Write(r.Context(), websocket.MessageText, []byte(reply)); err != nil {
					return
				}
			}
		}
	}))
}

// `API-035`: `open` waits for the server's own CONNECT. An emit between the upgrade and the reply
// would be dropped server-side, so the status does not claim readiness before it is real.
func TestTheOpenStatusWaitsForTheServersConnectReply(t *testing.T) {
	letConnectThrough := make(chan struct{})

	server := socketIOServer(t, func(_ *websocket.Conn, frame string) []string {
		if strings.HasPrefix(frame, "40") {
			<-letConnectThrough
			return []string{`40{"sid":"la-sesión"}`}
		}
		return nil
	})
	defer server.Close()

	log := &transcript{}
	streams := apiclient.NewStreams(log.emitter())

	require.NoError(t, streams.ConnectSocketIO(t.Context(), "s1", apiclient.SocketIoConnectRequest{
		URL: server.URL, Version: "v4", Options: wsOptions(),
	}))
	defer streams.Close("s1")

	// The transport is up and said so as a system line...
	log.waitForMessage(t, "the upgrade line", func(line apiclient.StreamMessage) bool {
		return line.Payload == "websocket upgraded"
	})

	// ...but `open` has not fired, because the session does not exist yet.
	for _, event := range log.events() {
		assert.NotEqual(t, apiclient.StatusOpen, event.Status,
			"readiness cannot be claimed before the server acknowledges the session")
	}

	close(letConnectThrough)
	opened := log.waitForStatus(t, apiclient.StatusOpen)
	assert.Equal(t, "la-sesión", opened.Detail, "the session id from the reply")
}

func TestAnEmittedEventRoundTrips(t *testing.T) {
	server := socketIOServer(t, func(_ *websocket.Conn, frame string) []string {
		switch {
		case strings.HasPrefix(frame, "40"):
			return []string{`40{"sid":"la-sesión"}`}
		case strings.HasPrefix(frame, "42"):
			// Echo the event back under a different name, so the two are told apart.
			return []string{`42["respuesta",{"eco":true}]`}
		}
		return nil
	})
	defer server.Close()

	log := &transcript{}
	streams := apiclient.NewStreams(log.emitter())

	require.NoError(t, streams.ConnectSocketIO(t.Context(), "s1", apiclient.SocketIoConnectRequest{
		URL: server.URL, Version: "v4", Options: wsOptions(),
	}))
	defer streams.Close("s1")
	log.waitForStatus(t, apiclient.StatusOpen)

	require.NoError(t, streams.Emit("s1", "mensaje", `{"a":1}`))

	sent := log.waitForMessage(t, "the emit", func(line apiclient.StreamMessage) bool {
		return line.Direction == apiclient.DirectionSent
	})
	assert.Equal(t, "mensaje", sent.Channel)
	assert.Equal(t, `{"a":1}`, sent.Payload)

	received := log.waitForMessage(t, "the reply", func(line apiclient.StreamMessage) bool {
		return line.Direction == apiclient.DirectionReceived
	})
	assert.Equal(t, "respuesta", received.Channel)
	assert.Equal(t, `{"eco":true}`, received.Payload)
}

func TestAServerPingIsAnsweredAtOnce(t *testing.T) {
	pongs := make(chan struct{}, 1)

	server := socketIOServer(t, func(_ *websocket.Conn, frame string) []string {
		switch {
		case strings.HasPrefix(frame, "40"):
			return []string{`40{"sid":"s"}`, "2"} // CONNECT reply, then an Engine.IO PING
		case frame == "3":
			select {
			case pongs <- struct{}{}:
			default:
			}
		}
		return nil
	})
	defer server.Close()

	log := &transcript{}
	streams := apiclient.NewStreams(log.emitter())

	require.NoError(t, streams.ConnectSocketIO(t.Context(), "s1", apiclient.SocketIoConnectRequest{
		URL: server.URL, Version: "v4", Options: wsOptions(),
	}))
	defer streams.Close("s1")

	select {
	case <-pongs:
		// An unanswered v4 ping is a dropped connection one ping-timeout later.
	case <-time.After(5 * time.Second):
		t.Fatal("the server's ping was never answered")
	}

	log.waitForMessage(t, "the ping line", func(line apiclient.StreamMessage) bool {
		return line.Payload == "ping received — pong sent"
	})
}

func TestAConnectErrorEndsTheSessionAsAnError(t *testing.T) {
	server := socketIOServer(t, func(_ *websocket.Conn, frame string) []string {
		if strings.HasPrefix(frame, "40") {
			return []string{`44{"message":"Not authorized"}`}
		}
		return nil
	})
	defer server.Close()

	log := &transcript{}
	streams := apiclient.NewStreams(log.emitter())

	require.NoError(t, streams.ConnectSocketIO(t.Context(), "s1", apiclient.SocketIoConnectRequest{
		URL: server.URL, Version: "v4", Options: wsOptions(),
	}))

	failed := log.waitForStatus(t, apiclient.StatusError)
	assert.Contains(t, failed.Detail, "Not authorized")
}

func TestTheClientsConnectPacketCarriesTheNamespaceAndAuth(t *testing.T) {
	frames := make(chan string, 4)

	server := socketIOServer(t, func(_ *websocket.Conn, frame string) []string {
		select {
		case frames <- frame:
		default:
		}
		if strings.HasPrefix(frame, "40") {
			return []string{`40{"sid":"s"}`}
		}
		return nil
	})
	defer server.Close()

	log := &transcript{}
	streams := apiclient.NewStreams(log.emitter())

	require.NoError(t, streams.ConnectSocketIO(t.Context(), "s1", apiclient.SocketIoConnectRequest{
		URL: server.URL, Version: "v4", Namespace: "/chat",
		AuthJSON: `{"token":"abc"}`, Options: wsOptions(),
	}))
	defer streams.Close("s1")

	select {
	case frame := <-frames:
		assert.Equal(t, `40/chat,{"token":"abc"}`, frame)
	case <-time.After(5 * time.Second):
		t.Fatal("no CONNECT packet arrived")
	}
}

func TestV3IsToldItsAuthWasIgnored(t *testing.T) {
	server := socketIOServer(t, func(_ *websocket.Conn, frame string) []string {
		if strings.HasPrefix(frame, "40") {
			return []string{`40{"sid":"s"}`}
		}
		return nil
	})
	defer server.Close()

	log := &transcript{}
	streams := apiclient.NewStreams(log.emitter())

	require.NoError(t, streams.ConnectSocketIO(t.Context(), "s1", apiclient.SocketIoConnectRequest{
		URL: server.URL, Version: "v3", AuthJSON: `{"token":"abc"}`, Options: wsOptions(),
	}))
	defer streams.Close("s1")

	// Said out loud rather than dropped silently: a user who configured auth and saw nothing would
	// reasonably conclude it was sent.
	log.waitForMessage(t, "the ignored-auth line", func(line apiclient.StreamMessage) bool {
		return strings.Contains(line.Payload, "Socket.IO v3 has no handshake auth")
	})
}

// `DIVERGENCE-API-b`: a binary attachment is shown as its own line and never spliced back into the
// placeholder it belongs to. Showing it beats dropping it, and reassembling is more than a console
// needs.
func TestABinaryAttachmentIsShownAsItsOwnLine(t *testing.T) {
	raw := []byte{0x01, 0x02, 0x03}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		socket, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = socket.CloseNow() }()

		_ = socket.Write(r.Context(), websocket.MessageText, []byte(`0{"sid":"s"}`))
		if _, _, err := socket.Read(r.Context()); err != nil {
			return
		}
		_ = socket.Write(r.Context(), websocket.MessageText, []byte(`40{"sid":"s"}`))
		_ = socket.Write(r.Context(), websocket.MessageText,
			[]byte(`451-["archivo",{"_placeholder":true,"num":0}]`))
		_ = socket.Write(r.Context(), websocket.MessageBinary, raw)

		<-r.Context().Done()
	}))
	defer server.Close()

	log := &transcript{}
	streams := apiclient.NewStreams(log.emitter())

	require.NoError(t, streams.ConnectSocketIO(t.Context(), "s1", apiclient.SocketIoConnectRequest{
		URL: server.URL, Version: "v4", Options: wsOptions(),
	}))
	defer streams.Close("s1")

	log.waitForMessage(t, "the attachment announcement", func(line apiclient.StreamMessage) bool {
		return strings.Contains(line.Payload, "1 attachment(s)")
	})
	attachment := log.waitForMessage(t, "the attachment itself", func(line apiclient.StreamMessage) bool {
		return line.Binary
	})
	assert.Equal(t, base64.StdEncoding.EncodeToString(raw), attachment.Payload)
	assert.Equal(t, apiclient.DirectionSystem, attachment.Direction)
}

func TestAnEmitOfInvalidJSONIsRefusedAndNothingIsSent(t *testing.T) {
	server := socketIOServer(t, func(_ *websocket.Conn, frame string) []string {
		if strings.HasPrefix(frame, "40") {
			return []string{`40{"sid":"s"}`}
		}
		return nil
	})
	defer server.Close()

	log := &transcript{}
	streams := apiclient.NewStreams(log.emitter())

	require.NoError(t, streams.ConnectSocketIO(t.Context(), "s1", apiclient.SocketIoConnectRequest{
		URL: server.URL, Version: "v4", Options: wsOptions(),
	}))
	defer streams.Close("s1")
	log.waitForStatus(t, apiclient.StatusOpen)

	// The emit is queued, so the refusal surfaces on the pump as an error status rather than as the
	// command's return — which is the shape of every streaming command.
	require.NoError(t, streams.Emit("s1", "mensaje", "{no es json}"))

	failed := log.waitForStatus(t, apiclient.StatusError)
	assert.Contains(t, failed.Detail, "not valid JSON")
}

// ---- the commands ---------------------------------------------------------------------------------

func TestTheWebSocketAndSocketIOCommandsAreRegistered(t *testing.T) {
	registry := bridge.NewRegistry()
	apiclient.RegisterStreams(registry, apiclient.StreamDeps{Streams: apiclient.NewStreams(nil)})
	registry.Seal()

	for _, name := range []string{
		"api_ws_connect", "api_ws_send", "api_socketio_connect", "api_socketio_emit",
		"api_stream_disconnect",
	} {
		_, registered := registry.Lookup(name)
		assert.True(t, registered, "%s", name)
	}
	// The four MQTT commands share this registration and are counted by their own test.
	assert.Equal(t, 9, registry.Len())
}

func TestDisconnectingThroughTheBridgeIsSafeOnAnUnknownID(t *testing.T) {
	registry := bridge.NewRegistry()
	apiclient.RegisterStreams(registry, apiclient.StreamDeps{Streams: apiclient.NewStreams(nil)})
	registry.Seal()

	_, err := invoke(t, registry, "api_stream_disconnect", map[string]any{"id": "nunca-existió"})
	assert.NoError(t, err)
}

func TestConnectingThroughTheBridgeCarriesTheWholeRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		socket, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = socket.CloseNow() }()
		<-r.Context().Done()
	}))
	defer server.Close()

	log := &transcript{}
	streams := apiclient.NewStreams(log.emitter())

	registry := bridge.NewRegistry()
	apiclient.RegisterStreams(registry, apiclient.StreamDeps{Streams: streams})
	registry.Seal()

	_, err := invoke(t, registry, "api_ws_connect", map[string]any{
		"id": "c1",
		"request": map[string]any{
			"url": server.URL, "headers": [][2]string{}, "subprotocols": []string{},
			"ping_interval_ms": 0,
			"options": map[string]any{
				"timeout_ms": 5000, "verify_ssl": true, "follow_redirects": true,
				"max_redirects": 10, "max_response_bytes": 1048576,
			},
		},
	})
	require.NoError(t, err)
	defer streams.Close("c1")

	log.waitForStatus(t, apiclient.StatusOpen)

	_, err = invoke(t, registry, "api_ws_send",
		map[string]any{"id": "c1", "payload": "hola", "binary": false})
	require.NoError(t, err)
}

// The transcript line is what the renderer parses, so its wire shape is the contract.
func TestTheTranscriptLineCrossesInTheRenderersShape(t *testing.T) {
	line := apiclient.StreamMessage{
		ConnectionID: "c1", Direction: apiclient.DirectionReceived, Channel: "mensaje",
		Payload: "hola", Binary: false, At: 1_758_000_000_000,
	}

	encoded, err := json.Marshal(line)
	require.NoError(t, err)

	var wire map[string]any
	require.NoError(t, json.Unmarshal(encoded, &wire))

	assert.Contains(t, wire, "connection_id")
	assert.Contains(t, wire, "direction")
	assert.Contains(t, wire, "binary")
	assert.Contains(t, wire, "at")
	// `qos` and `retain` are MQTT's alone, and absent everywhere else — which is what makes the
	// renderer's `qos?: number` read as undefined rather than as a zero.
	assert.NotContains(t, wire, "qos")
	assert.NotContains(t, wire, "retain")
}
