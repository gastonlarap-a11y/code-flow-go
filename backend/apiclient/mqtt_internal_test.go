package apiclient

// Internal: the wire codec and the small resolution rules are not part of the package's API, and
// exporting them to be tested would widen it to suit its tests rather than its callers.

import (
	"bufio"
	"bytes"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- the URL (API-040) ---------------------------------------------------------------------------

func TestParsesSchemesAndDefaultPorts(t *testing.T) {
	tests := []struct {
		url  string
		host string
		port int
		tls  bool
	}{
		{url: "broker.test", host: "broker.test", port: 1883},
		{url: "mqtt://broker.test", host: "broker.test", port: 1883},
		{url: "tcp://broker.test", host: "broker.test", port: 1883},
		{url: "mqtt://broker.test:1885", host: "broker.test", port: 1885},
		{url: "mqtts://broker.test", host: "broker.test", port: 8883, tls: true},
		{url: "ssl://broker.test", host: "broker.test", port: 8883, tls: true},
		{url: "tls://broker.test:9000", host: "broker.test", port: 9000, tls: true},
		// A path or a query is tolerated and dropped: MQTT has no URL-path semantics.
		{url: "mqtt://broker.test:1885/algo?x=1", host: "broker.test", port: 1885},
		// Credentials in the URL are stripped and **discarded** — they come from the request's own
		// fields, and a URL that carried a different password would silently win.
		{url: "mqtt://usuario:clave@broker.test", host: "broker.test", port: 1883},
		{url: "mqtt://usuario:cla@ve@broker.test:1885", host: "broker.test", port: 1885},
		// The bracketed IPv6 form is unwrapped.
		{url: "mqtt://[::1]:1885", host: "::1", port: 1885},
		{url: "mqtt://[2001:db8::1]", host: "2001:db8::1", port: 1883},
		// And a bare IPv6 address carries no port of its own.
		{url: "mqtt://2001:db8::1", host: "2001:db8::1", port: 1883},
	}

	for _, test := range tests {
		t.Run(test.url, func(t *testing.T) {
			endpoint, err := parseEndpoint(test.url)
			require.NoError(t, err)

			assert.Equal(t, test.host, endpoint.host)
			assert.Equal(t, test.port, endpoint.port)
			assert.Equal(t, test.tls, endpoint.tls)
		})
	}
}

func TestRejectsWhatItCannotDo(t *testing.T) {
	tests := []struct {
		url      string
		contains string
	}{
		// Refused by name rather than silently falling back to plain TCP, which would dial the
		// wrong port and look like a hang.
		{url: "ws://broker.test", contains: "not supported by this build"},
		{url: "wss://broker.test", contains: "not supported by this build"},
		{url: "http://broker.test", contains: "Unsupported MQTT URL scheme"},
		{url: "", contains: "empty"},
		{url: "   ", contains: "empty"},
		{url: "mqtt://", contains: "names no host"},
		{url: "mqtt://broker.test:no-es-un-puerto", contains: "is not a port number"},
		{url: "mqtt://broker.test:0", contains: "is not a port number"},
		{url: "mqtt://broker.test:99999", contains: "is not a port number"},
	}

	for _, test := range tests {
		t.Run(test.url, func(t *testing.T) {
			_, err := parseEndpoint(test.url)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.contains)
		})
	}
}

// ---- client id, QoS, will, keepalive ---------------------------------------------------------------

func TestGeneratesAClientIDOnlyWhenMissing(t *testing.T) {
	assert.Equal(t, "mi-cliente", resolveClientID("  mi-cliente  "))

	// Pattern-checked rather than literal: the suffix is random, which is the point.
	generated := regexp.MustCompile(`^codeflow-[0-9a-f]{8}$`)
	for _, requested := range []string{"", "   ", "\t"} {
		id := resolveClientID(requested)
		assert.Regexp(t, generated, id)
		// Seventeen characters, because some brokers cap the id's length and a longer one would be
		// refused for a reason nobody could see.
		assert.Len(t, id, 17)
	}

	assert.NotEqual(t, resolveClientID(""), resolveClientID(""), "fresh every time")
}

func TestClampsOutOfRangeQoS(t *testing.T) {
	// 0, 1 and 2 pass through; anything above degrades to the weakest guarantee rather than
	// failing the publish, the subscribe or the will it came from.
	assert.Equal(t, byte(0), clampQoS(0))
	assert.Equal(t, byte(1), clampQoS(1))
	assert.Equal(t, byte(2), clampQoS(2))
	assert.Equal(t, byte(0), clampQoS(3))
	assert.Equal(t, byte(0), clampQoS(255))
}

// Only the topic decides: a will with a payload, a QoS and a retain flag but no topic addresses
// nothing.
func TestALastWillWithNoTopicIsNoWill(t *testing.T) {
	assert.Nil(t, lastWillFor(nil))
	assert.Nil(t, lastWillFor(&MqttLastWill{Payload: "adiós", QoS: 1, Retain: true}))
	assert.Nil(t, lastWillFor(&MqttLastWill{Topic: "   ", Payload: "adiós"}))

	will := &MqttLastWill{Topic: "estado/cliente", Payload: "offline"}
	assert.Equal(t, will, lastWillFor(will))
}

// The asymmetry is inherited from the original's v5 library precondition, not chosen — do not tidy
// it into symmetry.
func TestTheKeepAliveFloorAppliesToV5Only(t *testing.T) {
	assert.Equal(t, uint16(5), keepAliveFor(0, mqttLevel5))
	assert.Equal(t, uint16(5), keepAliveFor(3, mqttLevel5))
	assert.Equal(t, uint16(30), keepAliveFor(30, mqttLevel5))

	// 3.1.1 defines `0` as "keepalive disabled", and a user can have meant it.
	assert.Equal(t, uint16(0), keepAliveFor(0, mqttLevel311))
	assert.Equal(t, uint16(3), keepAliveFor(3, mqttLevel311))

	// The field is two bytes on the wire; a larger value would wrap to something small and
	// disconnect a connection the user thought was long-lived.
	assert.Equal(t, uint16(65535), keepAliveFor(1_000_000, mqttLevel311))
}

func TestProtocolLevel(t *testing.T) {
	assert.Equal(t, byte(mqttLevel5), protocolLevel("5.0"))
	assert.Equal(t, byte(mqttLevel311), protocolLevel("3.1.1"))
	// Anything unrecognised is 3.1.1, which every broker speaks.
	assert.Equal(t, byte(mqttLevel311), protocolLevel(""))
	assert.Equal(t, byte(mqttLevel311), protocolLevel("v5"))
}

// ---- the wire format ---------------------------------------------------------------------------

func TestTheVariableLengthInteger(t *testing.T) {
	tests := []struct {
		value   int
		encoded []byte
	}{
		{value: 0, encoded: []byte{0x00}},
		{value: 127, encoded: []byte{0x7f}},
		{value: 128, encoded: []byte{0x80, 0x01}},
		{value: 16_383, encoded: []byte{0xff, 0x7f}},
		{value: 16_384, encoded: []byte{0x80, 0x80, 0x01}},
		{value: 2_097_151, encoded: []byte{0xff, 0xff, 0x7f}},
		{value: 268_435_455, encoded: []byte{0xff, 0xff, 0xff, 0x7f}},
	}

	for _, test := range tests {
		t.Run(string(rune(test.value)), func(t *testing.T) {
			assert.Equal(t, test.encoded, encodeVarInt(nil, test.value))

			decoded, err := readVarInt(bufio.NewReader(bytes.NewReader(test.encoded)))
			require.NoError(t, err)
			assert.Equal(t, test.value, decoded)
		})
	}
}

// A malformed length must error rather than loop: the five-byte form has no terminator and a
// stream of `0xff` would otherwise spin for ever.
func TestAMalformedLengthIsRefused(t *testing.T) {
	_, err := readVarInt(bufio.NewReader(bytes.NewReader([]byte{0xff, 0xff, 0xff, 0xff, 0x7f})))
	assert.ErrorIs(t, err, errMalformedLength)
}

func TestConnectCarriesWhatTheBrokerNeeds(t *testing.T) {
	packet, err := encodeConnect(connectOptions{
		clientID: "mi-cliente", username: "u", password: "p",
		keepAlive: 30, cleanSession: true, level: mqttLevel311,
		will: &MqttLastWill{Topic: "estado", Payload: "offline", QoS: 1, Retain: true},
	})
	require.NoError(t, err)

	assert.Equal(t, byte(mqttConnect<<4), packet[0])

	body := &reader{body: packet[2:]} // past the fixed header and its one length byte
	protocol, err := body.text()
	require.NoError(t, err)
	assert.Equal(t, "MQTT", protocol)

	level, err := body.byteAt()
	require.NoError(t, err)
	assert.Equal(t, byte(mqttLevel311), level)

	flags, err := body.byteAt()
	require.NoError(t, err)
	assert.Equal(t, byte(0x02), flags&0x02, "clean session")
	assert.Equal(t, byte(0x04), flags&0x04, "will present")
	assert.Equal(t, byte(1<<3), flags&0x18, "will QoS 1")
	assert.Equal(t, byte(0x20), flags&0x20, "will retained")
	assert.Equal(t, byte(0x40), flags&0x40, "password present")
	assert.Equal(t, byte(0x80), flags&0x80, "username present")

	keepAlive, err := body.uint16()
	require.NoError(t, err)
	assert.Equal(t, uint16(30), keepAlive)

	clientID, err := body.text()
	require.NoError(t, err)
	assert.Equal(t, "mi-cliente", clientID)
}

// v5 adds a property block after the keep-alive and another before the will topic. Getting either
// wrong shifts everything after it, and the broker reads the client id as a protocol error.
func TestTheV5ConnectCarriesItsEmptyPropertyBlocks(t *testing.T) {
	packet, err := encodeConnect(connectOptions{
		clientID: "c", keepAlive: 10, level: mqttLevel5,
		will: &MqttLastWill{Topic: "estado", Payload: "offline"},
	})
	require.NoError(t, err)

	body := &reader{body: packet[2:]}
	_, err = body.text() // "MQTT"
	require.NoError(t, err)
	_, err = body.byteAt() // level
	require.NoError(t, err)
	_, err = body.byteAt() // flags
	require.NoError(t, err)
	_, err = body.uint16() // keep alive
	require.NoError(t, err)

	properties, err := body.byteAt()
	require.NoError(t, err)
	assert.Equal(t, byte(0), properties, "an empty connect property block")

	clientID, err := body.text()
	require.NoError(t, err)
	assert.Equal(t, "c", clientID)

	willProperties, err := body.byteAt()
	require.NoError(t, err)
	assert.Equal(t, byte(0), willProperties, "an empty will property block")

	topic, err := body.text()
	require.NoError(t, err)
	assert.Equal(t, "estado", topic)
}

func TestPublishRoundTrips(t *testing.T) {
	for _, level := range []byte{mqttLevel311, mqttLevel5} {
		t.Run(string(rune('0'+level)), func(t *testing.T) {
			encoded, err := encodePublish("sensors/temp", []byte("21.5"), 1, true, 7, level)
			require.NoError(t, err)

			read, err := readPacket(bufio.NewReader(bytes.NewReader(encoded)))
			require.NoError(t, err)
			assert.Equal(t, byte(mqttPublish), read.kind)

			message, err := decodePublish(read, level)
			require.NoError(t, err)
			assert.Equal(t, "sensors/temp", message.topic)
			assert.Equal(t, "21.5", string(message.payload))
			assert.Equal(t, byte(1), message.qos)
			assert.True(t, message.retain)
			assert.Equal(t, uint16(7), message.packetID)
		})
	}
}

// A packet id rides only on QoS 1 and 2. Reading one on a QoS 0 publish eats the first two bytes of
// the payload, which is a corruption no test of the happy path would catch.
func TestAQoSZeroPublishCarriesNoPacketID(t *testing.T) {
	encoded, err := encodePublish("t", []byte("ab"), 0, false, 99, mqttLevel311)
	require.NoError(t, err)

	read, err := readPacket(bufio.NewReader(bytes.NewReader(encoded)))
	require.NoError(t, err)

	message, err := decodePublish(read, mqttLevel311)
	require.NoError(t, err)
	assert.Equal(t, "ab", string(message.payload), "the payload is intact")
	assert.Equal(t, uint16(0), message.packetID)
}

func TestSubscribeAndUnsubscribeCarryTheReservedBits(t *testing.T) {
	subscribe, err := encodeSubscribe(1, "sensors/#", 2, mqttLevel311)
	require.NoError(t, err)
	// A broker that checks the reserved bits rejects anything but `0010`.
	assert.Equal(t, byte(mqttSubscribe<<4|0x02), subscribe[0])

	unsubscribe, err := encodeUnsubscribe(2, "sensors/#", mqttLevel311)
	require.NoError(t, err)
	assert.Equal(t, byte(mqttUnsubscribe<<4|0x02), unsubscribe[0])

	release, err := encodeAck(mqttPubRel, 3)
	require.NoError(t, err)
	assert.Equal(t, byte(mqttPubRel<<4|0x02), release[0])

	// And the acknowledgements that have no reserved bits carry none.
	acknowledge, err := encodeAck(mqttPubAck, 4)
	require.NoError(t, err)
	assert.Equal(t, byte(mqttPubAck<<4), acknowledge[0])
}

func TestTheConnAckReasonsDifferByVersion(t *testing.T) {
	// The same byte means different things in the two versions, and showing one for the other
	// sends the reader looking at their credentials for a problem that is not there.
	assert.Equal(t, "not authorized", connAckReason(5, mqttLevel311))
	assert.Equal(t, "not authorized", connAckReason(0x87, mqttLevel5))

	assert.Equal(t, "bad user name or password", connAckReason(4, mqttLevel311))
	assert.Equal(t, "bad user name or password", connAckReason(0x86, mqttLevel5))

	// `5` is not a CONNACK reason code in v5 at all — the valid set is 0x00 and 0x80–0x9F — so it
	// renders as its own hex rather than borrowing v4's meaning for the same byte.
	assert.Equal(t, "connection refused, reason code 0x05", connAckReason(5, mqttLevel5))

	assert.Empty(t, connAckReason(0, mqttLevel311), "success has no reason")
	assert.Empty(t, connAckReason(0, mqttLevel5))
}

func TestSubAckReasons(t *testing.T) {
	assert.Equal(t, "granted QoS 1", subAckReason(1, mqttLevel311))
	assert.Equal(t, "rejected", subAckReason(0x80, mqttLevel311))

	assert.Equal(t, "granted QoS 2", subAckReason(2, mqttLevel5))
	assert.Equal(t, "not authorized", subAckReason(0x87, mqttLevel5))
	// The rest render as their hex, which is more use to a reader than a wrong guess at a name.
	assert.Equal(t, "reason code 0xB1", subAckReason(0xb1, mqttLevel5))
}

// A truncated packet must error rather than panic, in a process that also holds the user's
// terminals.
func TestATruncatedPacketIsRefusedRatherThanPanicking(t *testing.T) {
	assert.NotPanics(t, func() {
		_, err := decodePublish(mqttPacket{kind: mqttPublish, flags: 0x02, body: []byte{0x00}}, mqttLevel311)
		assert.ErrorIs(t, err, errTruncatedPacket)
	})

	assert.NotPanics(t, func() {
		// A length that claims more than the body holds.
		_, err := decodePublish(mqttPacket{
			kind: mqttPublish, body: []byte{0x00, 0xff, 'a'},
		}, mqttLevel311)
		assert.ErrorIs(t, err, errTruncatedPacket)
	})
}

func TestAPacketPastTheCapIsRefused(t *testing.T) {
	_, err := encodePacket(mqttPublish, 0, make([]byte, maxPacketBytes+1))
	assert.ErrorIs(t, err, ErrPacketTooLarge)
}

func TestTheDisconnectDiffersByVersion(t *testing.T) {
	v4, err := encodeDisconnect(mqttLevel311)
	require.NoError(t, err)
	assert.Equal(t, []byte{mqttDisconnect << 4, 0x00}, v4, "an empty body")

	v5, err := encodeDisconnect(mqttLevel5)
	require.NoError(t, err)
	assert.Equal(t, []byte{mqttDisconnect << 4, 0x01, 0x00}, v5, "a reason code")
}
