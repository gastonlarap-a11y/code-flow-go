package apiclient

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// Socket.IO and Engine.IO framing (API-030…037).
//
// Hand-rolled on top of the raw WebSocket, and deliberately: the two wire formats are a dozen lines
// each, and adopting a client library would mean adopting its reconnect, backoff and ack state
// machine — none of which an API console wants, because the whole point is to show the user what
// the socket actually did rather than paper over it.
//
// Two framing layers ride in every text frame:
//
//	4        2       /chat,   ["message",{"a":1}]
//	^        ^       ^        ^
//	Engine.IO|       |        Socket.IO payload, event name first
//	         Socket.IO type   namespace, omitted for the root one

// The Engine.IO packet types (API-030).
const (
	engineOpen    = '0'
	engineClose   = '1'
	enginePing    = '2'
	enginePong    = '3'
	engineMessage = '4'
)

// The Socket.IO packet types (API-031).
const (
	sioConnect      = '0'
	sioDisconnect   = '1'
	sioEvent        = '2'
	sioAck          = '3'
	sioConnectError = '4'
	sioBinaryEvent  = '5'
	sioBinaryAck    = '6'
)

// defaultPingInterval is what a v3 OPEN packet with no `pingInterval` is read as.
const defaultPingInterval = 25_000

// enginePacket is one decoded Engine.IO frame.
type enginePacket struct {
	kind byte
	body string
}

// decodeEngine reads the outer layer. An empty frame decodes to nothing and is ignored.
func decodeEngine(frame string) (enginePacket, bool) {
	if frame == "" {
		return enginePacket{}, false
	}
	return enginePacket{kind: frame[0], body: frame[1:]}, true
}

// sioPacket is one decoded Socket.IO packet.
type sioPacket struct {
	kind byte
	// attachments is the `<n>-` count a binary packet announces.
	attachments int
	namespace   string
	// ackID is the correlation id, and -1 when the packet carried none.
	ackID int
	data  string
}

// decodePacket reads `<kind>[<n>-][<namespace>,][<ack id>]<data>` (API-031).
//
// The order of the four optional parts is the format, and each one's test is narrow on purpose: an
// attachment count is recognised only when **every** character before the `-` is a digit, so a
// payload that merely contains a `-` is data rather than a malformed count.
func decodePacket(body string) (sioPacket, error) {
	if body == "" {
		return sioPacket{}, errEmptySocketIOPacket
	}

	packet := sioPacket{kind: body[0], namespace: "/", ackID: -1}
	switch packet.kind {
	case sioConnect, sioDisconnect, sioEvent, sioAck, sioConnectError, sioBinaryEvent, sioBinaryAck:
	default:
		return sioPacket{}, fmt.Errorf("unrecognised Socket.IO packet type %q", string(packet.kind)) //nolint:err113 // names what arrived
	}
	rest := body[1:]

	if dash := strings.IndexByte(rest, '-'); dash > 0 && allDigits(rest[:dash]) {
		packet.attachments = atoi(rest[:dash])
		rest = rest[dash+1:]
	}

	// A namespace segment exists only when what follows starts with `/`. It runs to the next comma,
	// or to the end for a namespace with no payload at all — `0/chat` is a CONNECT to `/chat`.
	if strings.HasPrefix(rest, "/") {
		if comma := strings.IndexByte(rest, ','); comma >= 0 {
			packet.namespace, rest = rest[:comma], rest[comma+1:]
		} else {
			packet.namespace, rest = rest, ""
		}
	}

	digits := 0
	for digits < len(rest) && rest[digits] >= '0' && rest[digits] <= '9' {
		digits++
	}
	if digits > 0 {
		packet.ackID = atoi(rest[:digits])
		rest = rest[digits:]
	}

	packet.data = rest
	return packet, nil
}

// messageFrame encodes `4<kind>[<ns>,]<body>` (API-032).
//
// **The root namespace has no segment at all** — never `/,`, which is what a naive "always emit the
// namespace" encoder produces and what every server rejects. A named one is prefixed with `/` when
// the caller left it out, and is always comma-terminated.
func messageFrame(kind byte, namespace, body string) string {
	frame := &strings.Builder{}
	frame.WriteByte(engineMessage)
	frame.WriteByte(kind)

	if segment := namespaceSegment(namespace); segment != "" {
		frame.WriteString(segment)
		frame.WriteByte(',')
	}
	frame.WriteString(body)
	return frame.String()
}

func namespaceSegment(namespace string) string {
	trimmed := strings.TrimSpace(namespace)
	if trimmed == "" || trimmed == "/" {
		return ""
	}
	if !strings.HasPrefix(trimmed, "/") {
		return "/" + trimmed
	}
	return trimmed
}

// eventArgs builds an outgoing event's `[name, payload]` array (API-033).
//
// The payload is **spliced in as raw JSON**, not re-encoded, so an object stays an object rather
// than becoming a string containing an object. It is validated first: a non-JSON payload errors
// without anything going out, because the far side would otherwise drop the whole frame and the
// user would see a send that appeared to succeed.
func eventArgs(name, payloadJSON string) (string, error) {
	encodedName, err := json.Marshal(name)
	if err != nil {
		return "", fmt.Errorf("encode the event name: %w", err)
	}

	trimmed := strings.TrimSpace(payloadJSON)
	if trimmed == "" {
		return "[" + string(encodedName) + "]", nil
	}
	if !json.Valid([]byte(trimmed)) {
		return "", fmt.Errorf("the payload for %q is not valid JSON", name) //nolint:err113 // names the event
	}
	return "[" + string(encodedName) + "," + trimmed + "]", nil
}

// splitEvent inverts eventArgs for an incoming packet (API-033).
//
// A lone remaining argument **keeps its own JSON shape**: `["msg","hi"]` yields the payload `"hi"`,
// a JSON string, and not `["hi"]`. Several arguments become an array, because there is nothing else
// they could be.
func splitEvent(data string) (name, payload string) {
	var args []json.RawMessage
	if err := json.Unmarshal([]byte(data), &args); err != nil || len(args) == 0 {
		// Not the shape an event takes: shown as-is rather than dropped, which is the console's job.
		return "", data
	}

	if err := json.Unmarshal(args[0], &name); err != nil {
		name = strings.Trim(string(args[0]), `"`)
	}

	switch len(args) {
	case 1:
		return name, ""
	case 2:
		return name, string(args[1])
	default:
		rest, err := json.Marshal(args[1:])
		if err != nil {
			return name, ""
		}
		return name, string(rest)
	}
}

// connectBody is the CONNECT packet's payload (API-034).
//
// Sent **only** on v4 and **only** when the JSON parses as a non-empty object. v3 has no
// handshake-auth payload at all, and `{}` is the renderer's "nothing configured" default rather
// than something worth putting on the wire.
//
// Malformed JSON reads the same as no auth: this is a console, and refusing to connect over a
// settings field would hide the socket the user actually wanted to look at.
func connectBody(authJSON string, v4 bool) string {
	if !v4 || !hasAuth(authJSON) {
		return ""
	}
	return strings.TrimSpace(authJSON)
}

func hasAuth(authJSON string) bool {
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimSpace(authJSON)), &object); err != nil {
		return false
	}
	return len(object) > 0
}

// handshakeURL builds the Engine.IO endpoint (API-037).
//
// The path is a **mount point, not a suffix**: it replaces whatever path the original URL carried,
// which is what `socket.io-client` does and what a user pasting `https://api.test/v2/thing` into the
// URL field expects when they also set a path.
//
// Only `transport=websocket` is ever asked for. There is no long-polling fallback and no upgrade
// sequence anywhere here — a console that silently fell back would be hiding the one fact worth
// reporting.
func handshakeURL(rawURL, path string, v4 bool, query [][2]string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("%q is not a URL: %w", rawURL, err)
	}

	switch strings.ToLower(parsed.Scheme) {
	case "http", "ws":
		parsed.Scheme = "ws"
	case "https", "wss":
		parsed.Scheme = "wss"
	default:
		return "", fmt.Errorf("Socket.IO needs an http(s) or ws(s) URL, not %q", parsed.Scheme) //nolint:staticcheck,err113 // ST1005: names what the user typed
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("%q names no host", rawURL) //nolint:err113 // names what the user typed
	}

	mount := strings.TrimSpace(path)
	if mount == "" {
		mount = "/socket.io"
	}
	mount = strings.TrimSuffix(mount, "/")
	if !strings.HasPrefix(mount, "/") {
		mount = "/" + mount
	}
	parsed.Path = mount + "/"

	// The caller's own query survives first, then the two Engine.IO parameters, then the caller's
	// separate `query` list — the order the original builds them in, and the one a server that
	// cares about duplicate keys would see.
	values := parsed.Query()
	eio := "3"
	if v4 {
		eio = "4"
	}
	values.Set("EIO", eio)
	values.Set("transport", "websocket")
	for _, pair := range query {
		values.Add(pair[0], pair[1])
	}
	parsed.RawQuery = values.Encode()

	return parsed.String(), nil
}

// ---- small helpers -----------------------------------------------------------------------------

func allDigits(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return len(value) > 0
}

// atoi is deliberately total: every caller has already checked the string is all digits, and a
// length that overflows an int is a frame no server sent.
func atoi(value string) int {
	number := 0
	for i := 0; i < len(value); i++ {
		number = number*10 + int(value[i]-'0')
		if number > 1<<30 {
			return 1 << 30
		}
	}
	return number
}
