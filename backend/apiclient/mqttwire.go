package apiclient

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// The MQTT wire format, 3.1.1 and 5.0, written out rather than taken from a library.
//
// The reason is the one the Socket.IO framing gives for itself, and it applies harder here: every Go
// MQTT client reconnects on its own, and `API-038` forbids exactly that — a console that silently
// reconnected would hide the instability it exists to reveal. Disabling that behaviour in a library
// means fighting its state machine on every release; the format below is a few hundred lines and
// does only what it is told.
//
// The two versions are one codec, not two stacks: they differ in a protocol level, an optional
// property block, and the shape of a few acknowledgements. Everything else is byte-identical.

// The control packet types, in the high nibble of the fixed header's first byte.
const (
	mqttConnect     = 1
	mqttConnAck     = 2
	mqttPublish     = 3
	mqttPubAck      = 4
	mqttPubRec      = 5
	mqttPubRel      = 6
	mqttPubComp     = 7
	mqttSubscribe   = 8
	mqttSubAck      = 9
	mqttUnsubscribe = 10
	mqttUnsubAck    = 11
	mqttPingReq     = 12
	mqttPingResp    = 13
	mqttDisconnect  = 14
)

// The two protocol levels this speaks.
const (
	mqttLevel311 = 4
	mqttLevel5   = 5
)

// maxPacketBytes caps both directions on both versions (API-045).
//
// 16 MiB, against a library default of 10 KB each way that would drop the connection on any
// realistic payload — an oversized incoming publish would otherwise surface as a state error rather
// than as a large message.
const maxPacketBytes = 16 * 1024 * 1024

// ErrPacketTooLarge is what a packet past the cap answers, on either side.
var ErrPacketTooLarge = errors.New("MQTT packet exceeds 16 MiB")

// mqttPacket is one control packet, undecoded past its fixed header.
type mqttPacket struct {
	kind  byte
	flags byte
	body  []byte
}

// ---- the variable-length integer ------------------------------------------------------------------

// encodeVarInt writes MQTT's remaining-length form: seven bits per byte, the top bit marking that
// another follows. Four bytes maximum, which is what bounds a packet at 256 MiB before this code's
// own 16 MiB cap applies.
func encodeVarInt(into []byte, value int) []byte {
	for {
		// `value % 128` is 0–127 by construction, so the narrowing cannot lose anything.
		digit := byte(value % 128) //nolint:gosec // G115: bounded by the modulus
		value /= 128
		if value > 0 {
			digit |= 0x80
		}
		into = append(into, digit)
		if value == 0 {
			return into
		}
	}
}

// readVarInt reads one, refusing the five-byte form a malformed stream would otherwise loop on.
func readVarInt(reader *bufio.Reader) (int, error) {
	value, multiplier := 0, 1

	for i := range 4 {
		digit, err := reader.ReadByte()
		if err != nil {
			return 0, fmt.Errorf("read a length byte: %w", err)
		}
		value += int(digit&0x7f) * multiplier
		if digit&0x80 == 0 {
			return value, nil
		}
		multiplier *= 128
		_ = i
	}
	return 0, errMalformedLength
}

var errMalformedLength = errors.New("malformed MQTT remaining length")

// ---- the primitives --------------------------------------------------------------------------------

// appendString writes a length-prefixed UTF-8 string, which is how MQTT carries every topic, client
// id and credential.
func appendString(into []byte, value string) []byte {
	into = binary.BigEndian.AppendUint16(into, uint16(len(value))) //nolint:gosec // G115: callers bound their strings
	return append(into, value...)
}

// appendBinary writes a length-prefixed byte string — a will payload, or a v5 password.
func appendBinary(into []byte, value []byte) []byte {
	into = binary.BigEndian.AppendUint16(into, uint16(len(value))) //nolint:gosec // G115: bounded by the caller
	return append(into, value...)
}

// reader walks a packet body, and every read is bounds-checked: a broker that lies about a length
// must produce an error rather than a panic in a process that also holds the user's terminals.
type reader struct {
	body []byte
	at   int
}

func (r *reader) byteAt() (byte, error) {
	if r.at >= len(r.body) {
		return 0, errTruncatedPacket
	}
	value := r.body[r.at]
	r.at++
	return value, nil
}

func (r *reader) uint16() (uint16, error) {
	if r.at+2 > len(r.body) {
		return 0, errTruncatedPacket
	}
	value := binary.BigEndian.Uint16(r.body[r.at:])
	r.at += 2
	return value, nil
}

func (r *reader) text() (string, error) {
	length, err := r.uint16()
	if err != nil {
		return "", err
	}
	if r.at+int(length) > len(r.body) {
		return "", errTruncatedPacket
	}
	value := string(r.body[r.at : r.at+int(length)])
	r.at += int(length)
	return value, nil
}

// rest is whatever the packet has left, which for a PUBLISH is its payload.
func (r *reader) rest() []byte {
	if r.at >= len(r.body) {
		return nil
	}
	remaining := r.body[r.at:]
	r.at = len(r.body)
	return remaining
}

// skipProperties steps over a v5 property block without interpreting it.
//
// Nothing in this console reads a property, and decoding all forty of them to discard them would be
// forty chances to be wrong about a length. What matters is landing on the byte after the block, so
// the rest of the packet parses.
func (r *reader) skipProperties() error {
	length, err := r.varInt()
	if err != nil {
		return err
	}
	if r.at+length > len(r.body) {
		return errTruncatedPacket
	}
	r.at += length
	return nil
}

func (r *reader) varInt() (int, error) {
	value, multiplier := 0, 1

	for range 4 {
		digit, err := r.byteAt()
		if err != nil {
			return 0, err
		}
		value += int(digit&0x7f) * multiplier
		if digit&0x80 == 0 {
			return value, nil
		}
		multiplier *= 128
	}
	return 0, errMalformedLength
}

var errTruncatedPacket = errors.New("truncated MQTT packet")

// ---- reading and writing packets --------------------------------------------------------------------

// readPacket reads one whole control packet.
func readPacket(source *bufio.Reader) (mqttPacket, error) {
	header, err := source.ReadByte()
	if err != nil {
		return mqttPacket{}, err
	}

	length, err := readVarInt(source)
	if err != nil {
		return mqttPacket{}, err
	}
	if length > maxPacketBytes {
		return mqttPacket{}, ErrPacketTooLarge
	}

	body := make([]byte, length)
	if _, err := io.ReadFull(source, body); err != nil {
		return mqttPacket{}, fmt.Errorf("read a packet body: %w", err)
	}
	return mqttPacket{kind: header >> 4, flags: header & 0x0f, body: body}, nil
}

// encodePacket frames a body with its fixed header.
func encodePacket(kind, flags byte, body []byte) ([]byte, error) {
	if len(body) > maxPacketBytes {
		return nil, ErrPacketTooLarge
	}

	packet := make([]byte, 0, len(body)+5)
	packet = append(packet, kind<<4|flags)
	packet = encodeVarInt(packet, len(body))
	return append(packet, body...), nil
}

// ---- CONNECT -------------------------------------------------------------------------------------

// connectOptions is what a CONNECT needs, already resolved.
type connectOptions struct {
	clientID     string
	username     string
	password     string
	keepAlive    uint16
	cleanSession bool
	level        byte
	will         *MqttLastWill
}

// encodeConnect builds a CONNECT for either version (API-043, API-044).
//
// The v5 property block is written empty: this client asks for no session expiry, no topic-alias
// maximum and no receive maximum, so the broker's own defaults apply — which is what a console
// wants, since anything else would be a setting nobody chose.
func encodeConnect(options connectOptions) ([]byte, error) {
	body := make([]byte, 0, 64)
	body = appendString(body, "MQTT")
	body = append(body, options.level)

	var flags byte
	if options.cleanSession {
		flags |= 0x02
	}
	if options.will != nil {
		flags |= 0x04
		flags |= clampQoS(options.will.QoS) << 3
		if options.will.Retain {
			flags |= 0x20
		}
	}
	if options.password != "" {
		flags |= 0x40
	}
	if options.username != "" {
		flags |= 0x80
	}
	body = append(body, flags)
	body = binary.BigEndian.AppendUint16(body, options.keepAlive)

	if options.level == mqttLevel5 {
		body = append(body, 0) // no connect properties
	}

	body = appendString(body, options.clientID)

	if options.will != nil {
		if options.level == mqttLevel5 {
			body = append(body, 0) // no will properties
		}
		body = appendString(body, options.will.Topic)
		body = appendBinary(body, []byte(options.will.Payload))
	}
	if options.username != "" {
		body = appendString(body, options.username)
	}
	if options.password != "" {
		body = appendBinary(body, []byte(options.password))
	}

	return encodePacket(mqttConnect, 0, body)
}

// connAck is what the broker answered.
type connAck struct {
	sessionPresent bool
	// code is 0 on success, and its meaning differs between versions — which is why the sentence a
	// user reads is built per version rather than from a shared table.
	code byte
}

func decodeConnAck(packet mqttPacket, level byte) (connAck, error) {
	body := &reader{body: packet.body}

	acknowledgeFlags, err := body.byteAt()
	if err != nil {
		return connAck{}, err
	}
	code, err := body.byteAt()
	if err != nil {
		return connAck{}, err
	}
	if level == mqttLevel5 {
		// The properties ride after the reason code and are stepped over.
		if err := body.skipProperties(); err != nil {
			return connAck{}, err
		}
	}
	return connAck{sessionPresent: acknowledgeFlags&0x01 != 0, code: code}, nil
}

// connAckReason is the sentence a refused connection shows.
//
// Two tables, because the two versions assign different meanings to the same byte: a `5` is "not
// authorized" in 3.1.1 and "unspecified error" in 5.0, and showing one for the other sends the
// reader looking at their credentials for a problem that is not there.
func connAckReason(code byte, level byte) string {
	if code == 0 {
		return ""
	}

	if level == mqttLevel5 {
		switch code {
		case 0x81:
			return "malformed packet"
		case 0x82:
			return "protocol error"
		case 0x84:
			return "unsupported protocol version"
		case 0x85:
			return "client id not valid"
		case 0x86:
			return "bad user name or password"
		case 0x87:
			return "not authorized"
		case 0x88:
			return "server unavailable"
		case 0x89:
			return "server busy"
		case 0x8a:
			return "banned"
		case 0x97:
			return "quota exceeded"
		default:
			return fmt.Sprintf("connection refused, reason code 0x%02X", code)
		}
	}

	switch code {
	case 1:
		return "unacceptable protocol version"
	case 2:
		return "client id rejected"
	case 3:
		return "server unavailable"
	case 4:
		return "bad user name or password"
	case 5:
		return "not authorized"
	default:
		return fmt.Sprintf("connection refused, code %d", code)
	}
}

// ---- PUBLISH -------------------------------------------------------------------------------------

// publishedMessage is one incoming PUBLISH.
type publishedMessage struct {
	topic     string
	payload   []byte
	qos       byte
	retain    bool
	packetID  uint16
	duplicate bool
}

func decodePublish(packet mqttPacket, level byte) (publishedMessage, error) {
	message := publishedMessage{
		qos:       (packet.flags >> 1) & 0x03,
		retain:    packet.flags&0x01 != 0,
		duplicate: packet.flags&0x08 != 0,
	}

	body := &reader{body: packet.body}

	topic, err := body.text()
	if err != nil {
		return publishedMessage{}, err
	}
	message.topic = topic

	// A packet id rides only on QoS 1 and 2 — reading one on a QoS 0 publish would eat the first
	// two bytes of the payload.
	if message.qos > 0 {
		id, err := body.uint16()
		if err != nil {
			return publishedMessage{}, err
		}
		message.packetID = id
	}
	if level == mqttLevel5 {
		if err := body.skipProperties(); err != nil {
			return publishedMessage{}, err
		}
	}

	message.payload = body.rest()
	return message, nil
}

func encodePublish(topic string, payload []byte, qos byte, retain bool, packetID uint16, level byte) ([]byte, error) {
	var flags byte
	flags |= clampQoS(qos) << 1
	if retain {
		flags |= 0x01
	}

	body := make([]byte, 0, len(topic)+len(payload)+8)
	body = appendString(body, topic)
	if clampQoS(qos) > 0 {
		body = binary.BigEndian.AppendUint16(body, packetID)
	}
	if level == mqttLevel5 {
		body = append(body, 0) // no publish properties
	}
	body = append(body, payload...)

	return encodePacket(mqttPublish, flags, body)
}

// encodeAck builds the one-line acknowledgements: PUBACK, PUBREC, PUBREL, PUBCOMP.
//
// The body is the packet id alone on both versions. v5 allows a reason code and a property block
// after it, but both are optional when the reason is success — and the two-byte form is what every
// broker accepts, on both versions, without a branch.
func encodeAck(kind byte, packetID uint16) ([]byte, error) {
	body := binary.BigEndian.AppendUint16(make([]byte, 0, 4), packetID)

	var flags byte
	if kind == mqttPubRel {
		// PUBREL's reserved bits are `0010`, and a broker that checks them closes the connection.
		flags = 0x02
	}
	return encodePacket(kind, flags, body)
}

func decodeAckPacketID(packet mqttPacket) (uint16, error) {
	body := &reader{body: packet.body}
	return body.uint16()
}

// ---- SUBSCRIBE and UNSUBSCRIBE ----------------------------------------------------------------------

func encodeSubscribe(packetID uint16, topic string, qos byte, level byte) ([]byte, error) {
	body := binary.BigEndian.AppendUint16(make([]byte, 0, len(topic)+8), packetID)
	if level == mqttLevel5 {
		body = append(body, 0) // no subscribe properties
	}
	body = appendString(body, topic)
	body = append(body, clampQoS(qos))

	// SUBSCRIBE's reserved bits are `0010`; a broker that checks them rejects anything else.
	return encodePacket(mqttSubscribe, 0x02, body)
}

func encodeUnsubscribe(packetID uint16, topic string, level byte) ([]byte, error) {
	body := binary.BigEndian.AppendUint16(make([]byte, 0, len(topic)+8), packetID)
	if level == mqttLevel5 {
		body = append(body, 0) // no unsubscribe properties
	}
	body = appendString(body, topic)

	return encodePacket(mqttUnsubscribe, 0x02, body)
}

// decodeSubAck answers the granted QoS or refusal per topic, which for this client is always one.
func decodeSubAck(packet mqttPacket, level byte) (packetID uint16, codes []byte, err error) {
	body := &reader{body: packet.body}

	packetID, err = body.uint16()
	if err != nil {
		return 0, nil, err
	}
	if level == mqttLevel5 {
		if err := body.skipProperties(); err != nil {
			return 0, nil, err
		}
	}
	return packetID, body.rest(), nil
}

// subAckReason renders one granted code.
//
// v4 spells its refusal as `0x80` and its grants as the QoS itself; v5 has a whole reason-code
// table, of which only the common few are named — the rest render as their hex, which is more use
// to a reader than a wrong guess at a name.
func subAckReason(code byte, level byte) string {
	if level != mqttLevel5 {
		if code == 0x80 {
			return "rejected"
		}
		return fmt.Sprintf("granted QoS %d", code)
	}

	switch code {
	case 0, 1, 2:
		return fmt.Sprintf("granted QoS %d", code)
	case 0x87:
		return "not authorized"
	case 0x8f:
		return "topic filter invalid"
	case 0x91:
		return "packet id in use"
	case 0x97:
		return "quota exceeded"
	case 0x9e:
		return "shared subscriptions not supported"
	case 0xa1:
		return "subscription ids not supported"
	case 0xa2:
		return "wildcard subscriptions not supported"
	default:
		return fmt.Sprintf("reason code 0x%02X", code)
	}
}

// ---- the rest --------------------------------------------------------------------------------------

func encodePingReq() ([]byte, error) { return encodePacket(mqttPingReq, 0, nil) }

// encodeDisconnect is the polite goodbye. v5 carries a reason code; 3.1.1 has an empty body, and a
// broker reading one where the other was expected closes the connection.
func encodeDisconnect(level byte) ([]byte, error) {
	if level == mqttLevel5 {
		return encodePacket(mqttDisconnect, 0, []byte{0x00})
	}
	return encodePacket(mqttDisconnect, 0, nil)
}

// clampQoS silently reduces anything above 2 to 0 (API-042).
//
// Silent on purpose: the value arrives from a stored request, and a corrupt one should degrade to
// the weakest delivery guarantee rather than fail the publish, the subscribe or the will it came
// from.
func clampQoS(qos byte) byte {
	if qos > 2 {
		return 0
	}
	return qos
}
