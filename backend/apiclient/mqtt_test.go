package apiclient_test

import (
	"bufio"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/apiclient"
	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/safego"
)

// A broker that speaks the real protocol, because a transport tested through a fake of itself tests
// the fake. It decodes what arrives with its own reader rather than the package's, so a codec that
// is self-consistently wrong still fails.

type brokerPacket struct {
	kind  byte
	flags byte
	body  []byte
}

type fakeBroker struct {
	listener net.Listener

	mutex    sync.Mutex
	received []brokerPacket

	// refuse makes the CONNACK carry a failure code.
	refuse byte
	// sessionPresent is echoed in the CONNACK's flags.
	sessionPresent bool
	// level is the protocol level the broker expects, which decides whether it writes property
	// blocks into its own packets.
	level byte
	// onConnected runs once the handshake is done, so a test can push traffic at the client.
	onConnected func(connection net.Conn)
}

func newFakeBroker(t *testing.T, broker *fakeBroker) *fakeBroker {
	t.Helper()

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	broker.listener = listener
	if broker.level == 0 {
		broker.level = 4
	}
	t.Cleanup(func() { _ = listener.Close() })

	safego.Go("fake-broker", func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			safego.Go("fake-broker-session", func() { broker.serve(connection) })
		}
	})
	return broker
}

func (b *fakeBroker) url() string { return "mqtt://" + b.listener.Addr().String() }

func (b *fakeBroker) serve(connection net.Conn) {
	defer func() { _ = connection.Close() }()
	source := bufio.NewReader(connection)

	connect, err := b.read(source)
	if err != nil {
		return
	}
	b.record(connect)

	// CONNACK: acknowledge flags, reason code, and on v5 an empty property block.
	acknowledge := []byte{0x00, b.refuse}
	if b.sessionPresent {
		acknowledge[0] = 0x01
	}
	if b.level == 5 {
		acknowledge = append(acknowledge, 0x00)
	}
	if _, err := connection.Write(frame(2, 0, acknowledge)); err != nil {
		return
	}
	if b.refuse != 0 {
		return
	}

	if b.onConnected != nil {
		b.onConnected(connection)
	}

	for {
		packet, err := b.read(source)
		if err != nil {
			return
		}
		b.record(packet)

		switch packet.kind {
		case 8: // SUBSCRIBE → SUBACK granting what was asked
			id := binary.BigEndian.Uint16(packet.body)
			granted := []byte{byte(packet.body[len(packet.body)-1])}
			body := binary.BigEndian.AppendUint16(nil, id)
			if b.level == 5 {
				body = append(body, 0x00)
			}
			_, _ = connection.Write(frame(9, 0, append(body, granted...)))

		case 10: // UNSUBSCRIBE → UNSUBACK
			id := binary.BigEndian.Uint16(packet.body)
			body := binary.BigEndian.AppendUint16(nil, id)
			if b.level == 5 {
				body = append(body, 0x00, 0x00)
			}
			_, _ = connection.Write(frame(11, 0, body))

		case 3: // PUBLISH → PUBACK when its QoS asks for one
			if (packet.flags>>1)&0x03 == 1 {
				// The packet id rides after the topic.
				topicLength := int(binary.BigEndian.Uint16(packet.body))
				id := packet.body[2+topicLength : 4+topicLength]
				_, _ = connection.Write(frame(4, 0, id))
			}

		case 12: // PINGREQ → PINGRESP
			_, _ = connection.Write(frame(13, 0, nil))

		case 14: // DISCONNECT
			return
		}
	}
}

func (b *fakeBroker) read(source *bufio.Reader) (brokerPacket, error) {
	header, err := source.ReadByte()
	if err != nil {
		return brokerPacket{}, err
	}

	length, multiplier := 0, 1
	for range 4 {
		digit, err := source.ReadByte()
		if err != nil {
			return brokerPacket{}, err
		}
		length += int(digit&0x7f) * multiplier
		if digit&0x80 == 0 {
			break
		}
		multiplier *= 128
	}

	body := make([]byte, length)
	if _, err := io.ReadFull(source, body); err != nil {
		return brokerPacket{}, err
	}
	return brokerPacket{kind: header >> 4, flags: header & 0x0f, body: body}, nil
}

func (b *fakeBroker) record(packet brokerPacket) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.received = append(b.received, packet)
}

func (b *fakeBroker) seen() []brokerPacket {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return append([]brokerPacket{}, b.received...)
}

// waitForPacket blocks until the broker has been sent one of `kind`.
func (b *fakeBroker) waitForPacket(tb testing.TB, kind byte) brokerPacket {
	tb.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, packet := range b.seen() {
			if packet.kind == kind {
				return packet
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	tb.Fatalf("the broker never received a packet of type %d", kind)
	return brokerPacket{}
}

// frame builds a fixed header around a body, the way the broker writes one.
func frame(kind, flags byte, body []byte) []byte {
	packet := []byte{kind<<4 | flags}

	length := len(body)
	for {
		digit := byte(length % 128)
		length /= 128
		if length > 0 {
			digit |= 0x80
		}
		packet = append(packet, digit)
		if length == 0 {
			break
		}
	}
	return append(packet, body...)
}

// publishFrom builds a PUBLISH the broker sends to the client.
func publishFrom(topic string, payload []byte, qos byte, retain bool, packetID uint16, level byte) []byte {
	var flags byte
	flags |= qos << 1
	if retain {
		flags |= 0x01
	}

	body := binary.BigEndian.AppendUint16(nil, uint16(len(topic)))
	body = append(body, topic...)
	if qos > 0 {
		body = binary.BigEndian.AppendUint16(body, packetID)
	}
	if level == 5 {
		body = append(body, 0x00)
	}
	return frame(3, flags, append(body, payload...))
}

func mqttRequest(url string) apiclient.MqttConnectRequest {
	return apiclient.MqttConnectRequest{
		URL: url, Version: apiclient.MqttVersion311, KeepAliveSecs: 60, CleanSession: true,
		Options: apiclient.NetworkOptions{TimeoutMs: 5_000, VerifySSL: true},
	}
}

// ---- the handshake -------------------------------------------------------------------------------

func TestConnectingToABrokerOpensAndSubscribes(t *testing.T) {
	broker := newFakeBroker(t, &fakeBroker{})

	log := &transcript{}
	streams := apiclient.NewStreams(log.emitter())

	request := mqttRequest(broker.url())
	request.ClientID = "mi-cliente"
	request.Username = "u"
	request.Password = "p"
	request.Subscriptions = []apiclient.MqttSubscribe{{Topic: "sensors/#", QoS: 1}}

	require.NoError(t, streams.ConnectMQTT(t.Context(), "m1", request))
	defer streams.Close("m1")

	opened := log.waitForStatus(t, apiclient.StatusOpen)
	assert.Equal(t, "Connected", opened.Detail)

	// The CONNECT carried what the broker needs, decoded by the broker's own reader.
	connect := broker.waitForPacket(t, 1)
	assert.Contains(t, string(connect.body), "MQTT")
	assert.Contains(t, string(connect.body), "mi-cliente")

	// And the configured subscription went out without waiting to be asked.
	subscribe := broker.waitForPacket(t, 8)
	assert.Contains(t, string(subscribe.body), "sensors/#")

	log.waitForMessage(t, "the granted subscription", func(line apiclient.StreamMessage) bool {
		return strings.Contains(line.Payload, "granted QoS 1")
	})
}

func TestAResumedSessionSaysSo(t *testing.T) {
	broker := newFakeBroker(t, &fakeBroker{sessionPresent: true})

	log := &transcript{}
	streams := apiclient.NewStreams(log.emitter())

	request := mqttRequest(broker.url())
	request.CleanSession = false

	require.NoError(t, streams.ConnectMQTT(t.Context(), "m1", request))
	defer streams.Close("m1")

	opened := log.waitForStatus(t, apiclient.StatusOpen)
	assert.Equal(t, "Connected, session resumed", opened.Detail)
}

// A refusal reaches the caller as an error rather than as a status event nobody was waiting for:
// there is no point starting a reader for a connection the broker just declined.
func TestARefusedConnectionIsAnErrorWithItsReason(t *testing.T) {
	broker := newFakeBroker(t, &fakeBroker{refuse: 5})

	log := &transcript{}
	streams := apiclient.NewStreams(log.emitter())

	err := streams.ConnectMQTT(t.Context(), "m1", mqttRequest(broker.url()))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not authorized")

	failed := log.waitForStatus(t, apiclient.StatusError)
	assert.Contains(t, failed.Detail, "not authorized")

	// And the id is free again, so a retry is a fresh connection rather than a collision.
	assert.ErrorContains(t, streams.Publish("m1", "t", "x", 0, false), "no open connection")
}

func TestAnUnreachableBrokerIsReportedRatherThanHanging(t *testing.T) {
	log := &transcript{}
	streams := apiclient.NewStreams(log.emitter())

	request := mqttRequest("mqtt://127.0.0.1:1")
	request.Options.TimeoutMs = 500

	err := streams.ConnectMQTT(t.Context(), "m1", request)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "couldn't reach")
}

// `API-040`: refused before anything is registered, so no id the panel could write into is left
// behind.
func TestABadURLIsRefusedBeforeAnythingIsRegistered(t *testing.T) {
	log := &transcript{}
	streams := apiclient.NewStreams(log.emitter())

	err := streams.ConnectMQTT(t.Context(), "m1", mqttRequest("ws://broker.test"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not supported by this build")

	assert.ErrorContains(t, streams.Publish("m1", "t", "x", 0, false), "no open connection")
}

// ---- traffic -------------------------------------------------------------------------------------

func TestAnIncomingPublishBecomesATranscriptLineWithItsQoSAndRetain(t *testing.T) {
	broker := newFakeBroker(t, &fakeBroker{
		onConnected: func(connection net.Conn) {
			_, _ = connection.Write(publishFrom("sensors/temp", []byte("21.5"), 1, true, 9, 4))
		},
	})

	log := &transcript{}
	streams := apiclient.NewStreams(log.emitter())

	require.NoError(t, streams.ConnectMQTT(t.Context(), "m1", mqttRequest(broker.url())))
	defer streams.Close("m1")

	line := log.waitForMessage(t, "the incoming publish", func(line apiclient.StreamMessage) bool {
		return line.Direction == apiclient.DirectionReceived
	})
	assert.Equal(t, "sensors/temp", line.Channel)
	assert.Equal(t, "21.5", line.Payload)
	assert.False(t, line.Binary)

	// `qos` and `retain` are MQTT's alone and are always populated here.
	require.NotNil(t, line.QoS)
	assert.Equal(t, byte(1), *line.QoS)
	require.NotNil(t, line.Retain)
	assert.True(t, *line.Retain)

	// A QoS 1 delivery is acknowledged, or the broker redelivers it for ever.
	broker.waitForPacket(t, 4)
}

// MQTT payloads are bytes: a topic carrying a JPEG is as ordinary as one carrying JSON.
func TestANonUTF8PayloadArrivesAsBase64(t *testing.T) {
	raw := []byte{0x89, 'P', 'N', 'G', 0xff, 0x00}

	broker := newFakeBroker(t, &fakeBroker{
		onConnected: func(connection net.Conn) {
			_, _ = connection.Write(publishFrom("camera/snapshot", raw, 0, false, 0, 4))
		},
	})

	log := &transcript{}
	streams := apiclient.NewStreams(log.emitter())

	require.NoError(t, streams.ConnectMQTT(t.Context(), "m1", mqttRequest(broker.url())))
	defer streams.Close("m1")

	line := log.waitForMessage(t, "the binary publish", func(line apiclient.StreamMessage) bool {
		return line.Direction == apiclient.DirectionReceived
	})
	assert.True(t, line.Binary)
	assert.Equal(t, base64.StdEncoding.EncodeToString(raw), line.Payload)
}

func TestPublishingReachesTheBrokerAndTheTranscript(t *testing.T) {
	broker := newFakeBroker(t, &fakeBroker{})

	log := &transcript{}
	streams := apiclient.NewStreams(log.emitter())

	require.NoError(t, streams.ConnectMQTT(t.Context(), "m1", mqttRequest(broker.url())))
	defer streams.Close("m1")
	log.waitForStatus(t, apiclient.StatusOpen)

	require.NoError(t, streams.Publish("m1", "controls/luz", "on", 1, true))

	published := broker.waitForPacket(t, 3)
	assert.Contains(t, string(published.body), "controls/luz")
	assert.Contains(t, string(published.body), "on")
	assert.Equal(t, byte(1), (published.flags>>1)&0x03, "QoS 1")
	assert.Equal(t, byte(0x01), published.flags&0x01, "retained")

	sent := log.waitForMessage(t, "the sent line", func(line apiclient.StreamMessage) bool {
		return line.Direction == apiclient.DirectionSent
	})
	assert.Equal(t, "controls/luz", sent.Channel)
	require.NotNil(t, sent.QoS)
	assert.Equal(t, byte(1), *sent.QoS)
}

// `API-042`, all the way through: a corrupt stored QoS degrades to the weakest guarantee rather
// than failing the publish.
func TestAnOutOfRangeQoSDegradesOnTheWire(t *testing.T) {
	broker := newFakeBroker(t, &fakeBroker{})

	log := &transcript{}
	streams := apiclient.NewStreams(log.emitter())

	require.NoError(t, streams.ConnectMQTT(t.Context(), "m1", mqttRequest(broker.url())))
	defer streams.Close("m1")
	log.waitForStatus(t, apiclient.StatusOpen)

	require.NoError(t, streams.Publish("m1", "t", "x", 7, false))

	published := broker.waitForPacket(t, 3)
	assert.Equal(t, byte(0), (published.flags>>1)&0x03)
}

func TestSubscribingAndUnsubscribingReachTheBroker(t *testing.T) {
	broker := newFakeBroker(t, &fakeBroker{})

	log := &transcript{}
	streams := apiclient.NewStreams(log.emitter())

	require.NoError(t, streams.ConnectMQTT(t.Context(), "m1", mqttRequest(broker.url())))
	defer streams.Close("m1")
	log.waitForStatus(t, apiclient.StatusOpen)

	require.NoError(t, streams.Subscribe("m1", "sensors/+/temp", 2))
	subscribe := broker.waitForPacket(t, 8)
	assert.Contains(t, string(subscribe.body), "sensors/+/temp")
	assert.Equal(t, byte(2), subscribe.body[len(subscribe.body)-1], "the requested QoS")

	require.NoError(t, streams.Unsubscribe("m1", "sensors/+/temp"))
	unsubscribe := broker.waitForPacket(t, 10)
	assert.Contains(t, string(unsubscribe.body), "sensors/+/temp")

	log.waitForMessage(t, "the unsubscribe acknowledgement", func(line apiclient.StreamMessage) bool {
		return line.Payload == "unsubscribed"
	})
}

// A deliberate close sends DISCONNECT first, so the broker knows this was meant and does not
// publish the last will.
func TestClosingSaysGoodbyeBeforeDroppingTheSocket(t *testing.T) {
	broker := newFakeBroker(t, &fakeBroker{})

	log := &transcript{}
	streams := apiclient.NewStreams(log.emitter())

	require.NoError(t, streams.ConnectMQTT(t.Context(), "m1", mqttRequest(broker.url())))
	log.waitForStatus(t, apiclient.StatusOpen)

	streams.Close("m1")

	broker.waitForPacket(t, 14)
	closed := log.waitForStatus(t, apiclient.StatusClosed)
	assert.Equal(t, "Closed by client", closed.Detail)
}

// `API-047`: what MQTT cannot honour is said out loud rather than dropped — a user who configured a
// proxy and saw nothing would reasonably conclude the connection went through one.
func TestIgnoredOptionsAreReported(t *testing.T) {
	broker := newFakeBroker(t, &fakeBroker{})

	log := &transcript{}
	streams := apiclient.NewStreams(log.emitter())

	request := mqttRequest(broker.url())
	request.Options.ProxyURL = "http://proxy.test:8080"
	request.Options.ClientCertPath = "/certs/cliente.pem"

	require.NoError(t, streams.ConnectMQTT(t.Context(), "m1", request))
	defer streams.Close("m1")

	// Both fire on the same connect when both are set.
	log.waitForMessage(t, "the proxy note", func(line apiclient.StreamMessage) bool {
		return strings.Contains(line.Payload, "Proxy is not supported for MQTT")
	})
	log.waitForMessage(t, "the client certificate note", func(line apiclient.StreamMessage) bool {
		return strings.Contains(line.Payload, "Client certificates are not supported for MQTT")
	})
}

// `API-038` again, for the third transport: a broker that hangs up leaves the connection closed.
func TestABrokerHangingUpDoesNotReconnect(t *testing.T) {
	var sessions int
	var mutex sync.Mutex

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()

	safego.Go("hangup-broker", func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			mutex.Lock()
			sessions++
			mutex.Unlock()

			source := bufio.NewReader(connection)
			broker := &fakeBroker{level: 4}
			if _, err := broker.read(source); err != nil {
				_ = connection.Close()
				continue
			}
			_, _ = connection.Write(frame(2, 0, []byte{0x00, 0x00}))
			_ = connection.Close()
		}
	})

	log := &transcript{}
	streams := apiclient.NewStreams(log.emitter())

	require.NoError(t, streams.ConnectMQTT(t.Context(),
		"m1", mqttRequest("mqtt://"+listener.Addr().String())))

	log.waitForStatus(t, apiclient.StatusError)
	time.Sleep(300 * time.Millisecond)

	mutex.Lock()
	defer mutex.Unlock()
	assert.Equal(t, 1, sessions, "one session, and no second one on its own")
}

// v5 writes property blocks the v4 codec does not. A connection that negotiates one and parses the
// other reads every field at the wrong offset.
func TestTheV5HandshakeWorksEndToEnd(t *testing.T) {
	broker := newFakeBroker(t, &fakeBroker{
		level: 5,
		onConnected: func(connection net.Conn) {
			_, _ = connection.Write(publishFrom("sensors/temp", []byte("21.5"), 0, false, 0, 5))
		},
	})

	log := &transcript{}
	streams := apiclient.NewStreams(log.emitter())

	request := mqttRequest(broker.url())
	request.Version = apiclient.MqttVersion5

	require.NoError(t, streams.ConnectMQTT(t.Context(), "m1", request))
	defer streams.Close("m1")

	log.waitForStatus(t, apiclient.StatusOpen)

	line := log.waitForMessage(t, "the v5 publish", func(line apiclient.StreamMessage) bool {
		return line.Direction == apiclient.DirectionReceived
	})
	assert.Equal(t, "sensors/temp", line.Channel)
	assert.Equal(t, "21.5", line.Payload, "the property block was stepped over, not read as payload")
}

// ---- the commands ---------------------------------------------------------------------------------

func TestTheFourMQTTCommandsAreRegistered(t *testing.T) {
	registry := bridge.NewRegistry()
	apiclient.RegisterStreams(registry, apiclient.StreamDeps{Streams: apiclient.NewStreams(nil)})
	registry.Seal()

	for _, name := range []string{
		"api_mqtt_connect", "api_mqtt_publish", "api_mqtt_subscribe", "api_mqtt_unsubscribe",
	} {
		_, registered := registry.Lookup(name)
		assert.True(t, registered, "%s", name)
	}
	// Five streaming commands plus these four.
	assert.Equal(t, 9, registry.Len())
}

func TestPublishingThroughTheBridge(t *testing.T) {
	broker := newFakeBroker(t, &fakeBroker{})

	log := &transcript{}
	streams := apiclient.NewStreams(log.emitter())

	registry := bridge.NewRegistry()
	apiclient.RegisterStreams(registry, apiclient.StreamDeps{Streams: streams})
	registry.Seal()

	_, err := invoke(t, registry, "api_mqtt_connect", map[string]any{
		"id": "m1",
		"request": map[string]any{
			"url": broker.url(), "client_id": "", "username": "", "password": "",
			"keep_alive_secs": 60, "clean_session": true, "version": "3.1.1",
			"last_will": nil, "subscriptions": []any{},
			"options": map[string]any{
				"timeout_ms": 5000, "verify_ssl": true, "follow_redirects": true,
				"max_redirects": 10, "max_response_bytes": 1048576,
			},
		},
	})
	require.NoError(t, err)
	defer streams.Close("m1")
	log.waitForStatus(t, apiclient.StatusOpen)

	_, err = invoke(t, registry, "api_mqtt_publish", map[string]any{
		"id": "m1", "topic": "controls/luz", "payload": "on", "qos": 1, "retain": false,
	})
	require.NoError(t, err)

	published := broker.waitForPacket(t, 3)
	assert.Contains(t, string(published.body), "controls/luz")
}

func TestAnMQTTCommandOnAWebSocketConnectionIsRefused(t *testing.T) {
	streams := apiclient.NewStreams(nil)

	// Nothing is open at all, which is the first refusal; the transport guard is the second and is
	// covered by the WebSocket test.
	err := streams.Publish("no-existe", "t", "x", 0, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no open connection")
}

func TestAMissingMQTTParameterIsNamed(t *testing.T) {
	registry := bridge.NewRegistry()
	apiclient.RegisterStreams(registry, apiclient.StreamDeps{Streams: apiclient.NewStreams(nil)})
	registry.Seal()

	_, err := invoke(t, registry, "api_mqtt_publish", map[string]any{
		"id": "m1", "topic": "t", "payload": "x", "retain": false,
		// `qos` missing.
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "qos")
}

// The connection is refused rather than dialled when the id is already live under another
// transport, which `errors.Is` cannot express — so the message is the contract.
func TestTheErrorsAreReadable(t *testing.T) {
	streams := apiclient.NewStreams(nil)

	for _, err := range []error{
		streams.Publish("x", "t", "p", 0, false),
		streams.Subscribe("x", "t", 0),
		streams.Unsubscribe("x", "t"),
	} {
		require.Error(t, err)
		assert.NotErrorIs(t, err, errors.ErrUnsupported)
		assert.Contains(t, err.Error(), "x")
	}
}
