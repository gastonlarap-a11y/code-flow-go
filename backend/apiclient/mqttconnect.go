package apiclient

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/safego"
)

// A live MQTT connection is **two goroutines**, not one (API-046).
//
// One drains the command channel the panel writes into; the other reads the socket. They are split
// because a read is not cancel-safe: selecting over both in one loop would mean abandoning a
// half-read packet whenever the other arm won a race, and MQTT's framing has no way to resynchronise
// after that — the next read would start mid-packet and every packet after it would be garbage.
//
// The writer is shared between them, behind a mutex, because both sides write: the command
// goroutine publishes and subscribes, and the reader answers PUBLISH with PUBACK and PING with
// PINGRESP.

// mqttSession is one connection's shared state.
type mqttSession struct {
	connection net.Conn
	// source is the buffered reader the handshake already started on. Handed to the read loop
	// rather than rebuilt, because whatever it pulled off the socket past the CONNACK would
	// otherwise be lost — a broker that sends CONNACK and a retained PUBLISH in one segment is
	// ordinary, and the second one would vanish.
	source *bufio.Reader
	level  byte
	report reporter

	// writes serialises the two goroutines that put packets on the wire. A partially written packet
	// interleaved with another is a stream neither side can parse.
	writes sync.Mutex
	// nextPacketID is the rolling id for QoS>0 publishes, subscribes and unsubscribes. MQTT
	// reserves 0, so it starts at 1 and wraps past 65535.
	nextPacketID atomic.Uint32
	// closing marks a deliberate shutdown, so a read error that races it is not reported as a
	// failure — the socket dying **because** we closed it is not news.
	closing atomic.Bool
}

// ConnectMQTT opens a broker connection and starts its two goroutines (API-040…047).
func (s *Streams) ConnectMQTT(ctx context.Context, id string, request MqttConnectRequest) error {
	request.Options = request.Options.withDefaults()
	report := s.reporterFor(id)

	endpoint, err := parseEndpoint(request.URL)
	if err != nil {
		// Refused before anything is registered: a connection id the panel could write into but
		// that never dialled is worse than a plain error.
		report.status(StatusError, err.Error())
		return err
	}

	running, entry := s.register(ctx, id, transportMQTT)
	report.status(StatusConnecting, "")

	connection, err := dialBroker(running, endpoint, request.Options, report)
	if err != nil {
		s.unregister(id, entry)
		entry.cancel()
		report.failure(err.Error())
		report.status(StatusError, err.Error())
		return err
	}

	level := protocolLevel(request.Version)
	session := &mqttSession{connection: connection, level: level, report: report}
	session.nextPacketID.Store(1)

	if err := session.handshake(request); err != nil {
		_ = connection.Close()
		s.unregister(id, entry)
		entry.cancel()
		report.failure(err.Error())
		report.status(StatusError, err.Error())
		return err
	}

	safego.Go("mqtt-reader-"+id, func() { s.readBroker(running, id, entry, session) })
	safego.Go("mqtt-commands-"+id, func() {
		session.drainCommands(running, entry, keepAliveFor(request.KeepAliveSecs, level))
	})
	return nil
}

// handshake sends CONNECT, waits for CONNACK and sends the initial subscriptions.
//
// Synchronous, unlike everything after it: there is no point starting a reader for a connection the
// broker is about to refuse, and a refusal has to reach the caller as an error rather than as a
// status event nobody was waiting for.
func (session *mqttSession) handshake(request MqttConnectRequest) error {
	packet, err := encodeConnect(connectOptions{
		clientID:     resolveClientID(request.ClientID),
		username:     request.Username,
		password:     request.Password,
		keepAlive:    keepAliveFor(request.KeepAliveSecs, session.level),
		cleanSession: request.CleanSession,
		level:        session.level,
		will:         lastWillFor(request.LastWill),
	})
	if err != nil {
		return err
	}
	if err := session.write(packet); err != nil {
		return err
	}

	// The handshake gets the request's own deadline; the socket that comes out of it does not,
	// because a broker with nothing to say is not a failure.
	if err := session.connection.SetReadDeadline(time.Now().Add(millis(request.Options.TimeoutMs))); err != nil {
		return fmt.Errorf("set the handshake deadline: %w", err)
	}

	source := bufio.NewReaderSize(session.connection, 32*1024)
	answer, err := readPacket(source)
	if err != nil {
		return fmt.Errorf("the broker did not answer the connect: %w", err)
	}
	if err := session.connection.SetReadDeadline(time.Time{}); err != nil {
		return fmt.Errorf("clear the handshake deadline: %w", err)
	}
	if answer.kind != mqttConnAck {
		return fmt.Errorf("the broker answered packet type %d instead of a connack", answer.kind) //nolint:err113 // names what arrived
	}

	acknowledged, err := decodeConnAck(answer, session.level)
	if err != nil {
		return err
	}
	if acknowledged.code != 0 {
		return errors.New(connAckReason(acknowledged.code, session.level)) //nolint:err113 // the message is what the panel shows
	}

	detail := "Connected"
	if acknowledged.sessionPresent {
		detail = "Connected, session resumed"
	}
	session.report.status(StatusOpen, detail)

	// The reader takes over from this buffered source, so whatever it already pulled off the socket
	// is not lost — a broker that sent CONNACK and a retained PUBLISH in one segment is ordinary.
	session.source = source

	session.subscribeInitial(request.Subscriptions)
	return nil
}

// subscribeInitial sends the configured subscriptions.
//
// Sent from here rather than from the read loop, which is the same separation the original draws:
// a subscribe waits for room in the outgoing queue, and the only thing that drains that queue is the
// loop that would be blocked waiting for it.
func (session *mqttSession) subscribeInitial(subscriptions []MqttSubscribe) {
	for _, subscription := range subscriptions {
		if err := session.subscribe(subscription.Topic, subscription.QoS); err != nil {
			session.report.failure(err.Error())
			return
		}
	}
}

// readBroker is the reading goroutine.
func (s *Streams) readBroker(ctx context.Context, id string, entry *connection, session *mqttSession) {
	status, detail := StatusClosed, "Closed by client"
	defer func() {
		_ = session.connection.Close()
		// Only if it is still this connection's entry (API-048): a reconnect under the same id that
		// already replaced it must not be cleared by this teardown.
		s.unregister(id, entry)
		// The reader owns the teardown, and it reaches here **after** the command goroutine wrote
		// the DISCONNECT and closed the socket — so marking the connection finished here is what
		// makes a deliberate close wait for its goodbye rather than race it.
		entry.finished()
		entry.cancel()
		session.report.status(status, detail)
	}()

	// Closing the socket is what unblocks the read, since a read has no context to cancel.
	safego.Go("mqtt-closer-"+id, func() {
		<-ctx.Done()
		session.closing.Store(true)
		_ = session.connection.Close()
	})

	for {
		packet, err := readPacket(session.source)
		if err != nil {
			if session.closing.Load() || errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				// The socket died because we closed it, which is not news.
				return
			}
			status, detail = StatusError, fmt.Sprintf("connection lost: %v", err)
			session.report.failure(detail)
			return
		}

		if keepGoing, packetStatus, packetDetail := session.onPacket(packet); !keepGoing {
			status, detail = packetStatus, packetDetail
			return
		}
	}
}

// onPacket maps one broker packet onto the transcript.
func (session *mqttSession) onPacket(packet mqttPacket) (keepGoing bool, status, detail string) {
	switch packet.kind {
	case mqttPublish:
		return session.onPublish(packet)

	case mqttPubAck, mqttPubComp:
		id, _ := decodeAckPacketID(packet)
		session.report.system(fmt.Sprintf("delivery %d acknowledged", id))
		return true, "", ""

	case mqttPubRec:
		// QoS 2, step two: the broker has the message and wants a release.
		id, err := decodeAckPacketID(packet)
		if err != nil {
			return true, "", ""
		}
		if release, err := encodeAck(mqttPubRel, id); err == nil {
			_ = session.write(release)
		}
		return true, "", ""

	case mqttPubRel:
		// QoS 2 the other way: the broker released an incoming message and wants it completed.
		id, err := decodeAckPacketID(packet)
		if err != nil {
			return true, "", ""
		}
		if complete, err := encodeAck(mqttPubComp, id); err == nil {
			_ = session.write(complete)
		}
		return true, "", ""

	case mqttSubAck:
		_, codes, err := decodeSubAck(packet, session.level)
		if err != nil {
			return true, "", ""
		}
		for _, code := range codes {
			session.report.system("subscription " + subAckReason(code, session.level))
		}
		return true, "", ""

	case mqttUnsubAck:
		session.report.system("unsubscribed")
		return true, "", ""

	case mqttPingResp:
		session.report.system("pong received")
		return true, "", ""

	case mqttDisconnect:
		// v5 only: the broker saying goodbye rather than dropping the socket.
		return false, StatusClosed, "Disconnected by broker"

	default:
		session.report.system(fmt.Sprintf("packet type %d ignored", packet.kind))
		return true, "", ""
	}
}

// onPublish turns an incoming message into a transcript line, acknowledging it if its QoS asks.
func (session *mqttSession) onPublish(packet mqttPacket) (keepGoing bool, status, detail string) {
	message, err := decodePublish(packet, session.level)
	if err != nil {
		session.report.failure("undecodable publish: " + err.Error())
		return true, "", ""
	}

	// UTF-8 when it is valid, base64 when it is not: MQTT payloads are bytes, and a topic carrying
	// a JPEG is as ordinary as one carrying JSON.
	payload, binary := string(message.payload), false
	if !utf8.Valid(message.payload) {
		payload, binary = base64.StdEncoding.EncodeToString(message.payload), true
	}

	qos, retain := message.qos, message.retain
	session.report.emitter.Emit(EventStreamMessage, StreamMessage{
		ConnectionID: session.report.id,
		Direction:    DirectionReceived,
		Channel:      message.topic,
		Payload:      payload,
		Binary:       binary,
		At:           session.report.now().UnixMilli(),
		QoS:          &qos,
		Retain:       &retain,
	})

	switch message.qos {
	case 1:
		if acknowledge, err := encodeAck(mqttPubAck, message.packetID); err == nil {
			_ = session.write(acknowledge)
		}
	case 2:
		if received, err := encodeAck(mqttPubRec, message.packetID); err == nil {
			_ = session.write(received)
		}
	}
	return true, "", ""
}

// drainCommands is the writing goroutine: the panel's commands, and the keepalive.
func (session *mqttSession) drainCommands(ctx context.Context, entry *connection, keepAlive uint16) {
	var ping *time.Ticker
	if keepAlive > 0 {
		// Pinged at half the declared interval, which is the usual margin: a ping that left exactly
		// on the deadline arrives after it on any latency at all, and the broker disconnects.
		ping = time.NewTicker(time.Duration(keepAlive) * time.Second / 2)
		defer ping.Stop()
	}

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
				session.closing.Store(true)
				// A goodbye first, so the broker knows this was deliberate and does not publish the
				// last will.
				if goodbye, err := encodeDisconnect(session.level); err == nil {
					_ = session.write(goodbye)
				}
				_ = session.connection.Close()
				return
			}
			if err := session.run(command); err != nil {
				session.report.failure(err.Error())
			}

		case <-tick:
			request, err := encodePingReq()
			if err != nil {
				continue
			}
			if err := session.write(request); err != nil {
				session.report.failure(err.Error())
				return
			}
		}
	}
}

// run performs one command from the panel.
func (session *mqttSession) run(command streamCommand) error {
	switch command.kind {
	case commandPublish:
		return session.publish(command)
	case commandSubscribe:
		return session.subscribe(command.channel, command.qos)
	case commandUnsubscribe:
		return session.unsubscribe(command.channel)
	default:
		return fmt.Errorf("an MQTT connection cannot %s", command.kind) //nolint:err113 // names the command
	}
}

func (session *mqttSession) publish(command streamCommand) error {
	payload := []byte(command.payload)
	if command.binary {
		decoded, err := base64.StdEncoding.DecodeString(command.payload)
		if err != nil {
			return fmt.Errorf("the binary payload is not base64: %w", err)
		}
		payload = decoded
	}

	qos := clampQoS(command.qos)
	packet, err := encodePublish(command.channel, payload, qos, command.retain,
		session.packetID(), session.level)
	if err != nil {
		return err
	}
	if err := session.write(packet); err != nil {
		return err
	}

	retain := command.retain
	session.report.emitter.Emit(EventStreamMessage, StreamMessage{
		ConnectionID: session.report.id,
		Direction:    DirectionSent,
		Channel:      command.channel,
		Payload:      command.payload,
		Binary:       command.binary,
		At:           session.report.now().UnixMilli(),
		QoS:          &qos,
		Retain:       &retain,
	})
	return nil
}

func (session *mqttSession) subscribe(topic string, qos byte) error {
	packet, err := encodeSubscribe(session.packetID(), topic, qos, session.level)
	if err != nil {
		return err
	}
	if err := session.write(packet); err != nil {
		return err
	}
	session.report.system(fmt.Sprintf("subscribing to %s at QoS %d", topic, clampQoS(qos)))
	return nil
}

func (session *mqttSession) unsubscribe(topic string) error {
	packet, err := encodeUnsubscribe(session.packetID(), topic, session.level)
	if err != nil {
		return err
	}
	if err := session.write(packet); err != nil {
		return err
	}
	session.report.system("unsubscribing from " + topic)
	return nil
}

// packetID hands out the next id, skipping 0 — which MQTT reserves.
func (session *mqttSession) packetID() uint16 {
	for {
		next := session.nextPacketID.Add(1)
		if id := uint16(next); id != 0 { //nolint:gosec // G115: the truncation is the wrap this wants
			return id
		}
	}
}

// write puts one whole packet on the wire, under the lock the two goroutines share.
func (session *mqttSession) write(packet []byte) error {
	session.writes.Lock()
	defer session.writes.Unlock()

	if _, err := session.connection.Write(packet); err != nil {
		return fmt.Errorf("write to the broker: %w", err)
	}
	return nil
}
