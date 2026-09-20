package apiclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/safego"
)

// The Socket.IO session on top of the raw WebSocket (API-034…036).
//
// The sequencing is the interesting part, and it is `API-035`: the transport being up is **not** the
// session being ready. An `emit` sent between the WebSocket upgrade and the server's own CONNECT
// reply is dropped server-side, so `open` waits for the reply rather than claiming readiness the
// moment bytes can flow.

// ConnectSocketIO opens the session (API-035, API-037).
func (s *Streams) ConnectSocketIO(ctx context.Context, id string, request SocketIoConnectRequest) error {
	request.Options = request.Options.withDefaults()
	v4 := !strings.EqualFold(strings.TrimSpace(request.Version), "v3")

	// The URL is built before anything is registered: a scheme this cannot read is a typo, and
	// registering a connection that never dialled would leave an id the panel could write into.
	target, err := handshakeURL(request.URL, request.Path, v4, request.Query)
	if err != nil {
		return err
	}

	running, entry := s.register(ctx, id, transportSocketIO)
	report := s.reporterFor(id)
	report.status(StatusConnecting, "")

	options := dialOptions(WsConnectRequest{Headers: request.Headers, Options: request.Options})

	handshake, cancelHandshake := context.WithTimeout(running, millis(request.Options.TimeoutMs))
	socket, response, err := websocket.Dial(handshake, target, options)
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

	// The transport is up and the session is not: said out loud as a system line, and deliberately
	// **not** as an `open` status.
	report.system("websocket upgraded")

	safego.Go("socketio-pump-"+id, func() {
		session := &socketIOSession{
			id: id, namespace: request.Namespace, authJSON: request.AuthJSON, v4: v4,
			report: report, socket: socket,
		}
		s.pumpSocketIO(running, entry, session)
	})
	return nil
}

// socketIOSession is one live session's own state, apart from the socket.
type socketIOSession struct {
	id        string
	namespace string
	authJSON  string
	v4        bool
	report    reporter
	socket    *websocket.Conn
}

// pumpSocketIO drives the session until one side stops.
func (s *Streams) pumpSocketIO(ctx context.Context, entry *connection, session *socketIOSession) {
	status, detail := StatusClosed, "Closed by client"
	defer func() {
		_ = session.socket.Close(websocket.StatusNormalClosure, "")
		s.unregister(session.id, entry)
		entry.finished()
		entry.cancel()
		session.report.status(status, detail)
	}()

	frames := readFrames(ctx, session.socket)

	// v3's heartbeat is client-initiated and armed from the OPEN packet; v4's is server-initiated
	// and this timer stays permanently disarmed (API-036).
	var ping *time.Ticker
	defer func() {
		if ping != nil {
			ping.Stop()
		}
	}()

	for {
		var tick <-chan time.Time
		if ping != nil {
			tick = ping.C
		}

		select {
		case <-ctx.Done():
			return

		case command, open := <-entry.commands:
			if !open || command.kind == commandClose {
				// A Socket.IO goodbye first, then the WebSocket close: a server that only saw the
				// socket drop cannot tell a deliberate disconnect from a dead client.
				_ = session.write(ctx, messageFrame(sioDisconnect, session.namespace, ""))
				return
			}
			if err := session.emit(ctx, command); err != nil {
				status, detail = StatusError, err.Error()
				session.report.failure(detail)
				return
			}

		case frame, open := <-frames:
			if !open {
				return
			}
			if frame.err != nil {
				status, detail = closeReason(frame.err)
				return
			}
			if frame.kind == websocket.MessageBinary {
				// A binary attachment is shown as its own line and **never spliced back** into the
				// placeholder it belongs to (`DIVERGENCE-API-b`): reassembling it is more than the
				// console needs, and showing it beats dropping it.
				session.report.message(DirectionSystem, "",
					base64.StdEncoding.EncodeToString(frame.data), true)
				continue
			}

			keepGoing, frameStatus, frameDetail, armed := s.onEngineFrame(ctx, session, string(frame.data))
			if armed > 0 && ping == nil {
				ping = time.NewTicker(millis(armed))
			}
			if !keepGoing {
				status, detail = frameStatus, frameDetail
				return
			}

		case <-tick:
			// v3 only: the client's own heartbeat.
			if err := session.write(ctx, string(enginePing)); err != nil {
				status, detail = StatusError, err.Error()
				session.report.failure(detail)
				return
			}
		}
	}
}

// onEngineFrame handles the outer layer (API-030, API-036).
//
// `armed` is the v3 ping interval the OPEN packet announced, and zero every other time.
func (s *Streams) onEngineFrame(ctx context.Context, session *socketIOSession, frame string) (keepGoing bool, status, detail string, armed uint64) {
	packet, ok := decodeEngine(frame)
	if !ok {
		session.report.system("empty frame ignored")
		return true, "", "", 0
	}

	switch packet.kind {
	case engineOpen:
		return true, "", "", session.onOpen(ctx, packet.body)

	case engineClose:
		return false, StatusClosed, "Closed by server", 0

	case enginePing:
		// Server-initiated heartbeat (v4). Answered at once: an unanswered ping is a dropped
		// connection one ping-timeout later.
		if err := session.write(ctx, string(enginePong)); err != nil {
			return false, StatusError, err.Error(), 0
		}
		session.report.system("ping received — pong sent")
		return true, "", "", 0

	case enginePong:
		// The reply to v3's own client-initiated ping. Logged and nothing else.
		session.report.system("pong received")
		return true, "", "", 0

	case engineMessage:
		keepGoing, status, detail = session.onMessage(ctx, packet.body)
		return keepGoing, status, detail, 0

	default:
		session.report.system(fmt.Sprintf("engine.io packet %q ignored", string(packet.kind)))
		return true, "", "", 0
	}
}

// onOpen reads the handshake JSON and sends the client's own CONNECT (API-035, API-036).
func (session *socketIOSession) onOpen(ctx context.Context, body string) (armed uint64) {
	var handshake struct {
		SID          string `json:"sid"`
		PingInterval uint64 `json:"pingInterval"`
	}
	_ = json.Unmarshal([]byte(body), &handshake) // a handshake this cannot read is still a session

	session.report.system("engine.io open, sid " + handshake.SID)

	if err := session.write(ctx,
		messageFrame(sioConnect, session.namespace, connectBody(session.authJSON, session.v4))); err != nil {
		session.report.failure(err.Error())
		return 0
	}
	// Said out loud rather than dropped silently: a user who configured auth and sees nothing about
	// it would reasonably conclude it was sent.
	if !session.v4 && strings.TrimSpace(session.authJSON) != "" {
		session.report.system("auth payload ignored: Socket.IO v3 has no handshake auth")
	}

	if session.v4 {
		// v4's heartbeat is the server's to start; arming a client timer here would add a second,
		// unasked-for one.
		return 0
	}
	if handshake.PingInterval == 0 {
		return defaultPingInterval
	}
	return handshake.PingInterval
}

// onMessage handles the inner Socket.IO layer (API-031, API-035).
func (session *socketIOSession) onMessage(ctx context.Context, body string) (keepGoing bool, status, detail string) {
	_ = ctx

	packet, err := decodePacket(body)
	if err != nil {
		session.report.system("undecodable Socket.IO packet: " + body)
		return true, "", ""
	}

	switch packet.kind {
	case sioConnect:
		// **This** is readiness, not the WebSocket upgrade: an emit before it is dropped
		// server-side, so the status waits for the server to say the session exists.
		var reply struct {
			SID string `json:"sid"`
		}
		_ = json.Unmarshal([]byte(packet.data), &reply)
		session.report.status(StatusOpen, reply.SID)
		return true, "", ""

	case sioDisconnect:
		return false, StatusClosed, "Disconnected by server"

	case sioConnectError:
		message := strings.TrimSpace(packet.data)
		if message == "" {
			message = "Server refused the connection"
		}
		session.report.failure(message)
		return false, StatusError, message

	case sioEvent:
		name, payload := splitEvent(packet.data)
		session.report.message(DirectionReceived, name, payload, false)
		return true, "", ""

	case sioAck:
		// Parsed and shown, never correlated back to the emit that asked for it — there is no
		// pending-ack map anywhere in this transport (`AMBIGUOUS-API-a`), and the console shows the
		// ack rather than awaiting it programmatically.
		session.report.system(fmt.Sprintf("ack %d %s", packet.ackID, packet.data))
		return true, "", ""

	case sioBinaryEvent, sioBinaryAck:
		session.report.system(fmt.Sprintf(
			"binary packet with %d attachment(s): %s", packet.attachments, packet.data))
		return true, "", ""

	default:
		session.report.system("unhandled Socket.IO packet " + body)
		return true, "", ""
	}
}

// emit puts one outgoing event on the wire.
func (session *socketIOSession) emit(ctx context.Context, command streamCommand) error {
	args, err := eventArgs(command.channel, command.payload)
	if err != nil {
		return err
	}

	frame := messageFrame(sioEvent, session.namespace, args)
	if err := session.write(ctx, frame); err != nil {
		return err
	}
	session.report.message(DirectionSent, command.channel, command.payload, false)
	return nil
}

func (session *socketIOSession) write(ctx context.Context, frame string) error {
	if err := session.socket.Write(ctx, websocket.MessageText, []byte(frame)); err != nil {
		return fmt.Errorf("write a Socket.IO frame: %w", err)
	}
	return nil
}
