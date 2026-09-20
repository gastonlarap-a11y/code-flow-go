package apiclient

// Internal: the framing is a dozen pure functions that are not part of the wire surface, and
// exporting them to be tested would widen the package's API to suit its tests rather than its
// callers.

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- Engine.IO (API-030) -----------------------------------------------------------------------

func TestEngineOpcodesMapToTheirPackets(t *testing.T) {
	tests := []struct {
		frame string
		kind  byte
		body  string
	}{
		{frame: "0{\"sid\":\"abc\"}", kind: engineOpen, body: `{"sid":"abc"}`},
		{frame: "1", kind: engineClose, body: ""},
		{frame: "2", kind: enginePing, body: ""},
		{frame: "3", kind: enginePong, body: ""},
		{frame: `42["msg","hi"]`, kind: engineMessage, body: `2["msg","hi"]`},
		{frame: "9algo", kind: '9', body: "algo"},
	}

	for _, test := range tests {
		t.Run(test.frame, func(t *testing.T) {
			packet, ok := decodeEngine(test.frame)
			require.True(t, ok)
			assert.Equal(t, test.kind, packet.kind)
			assert.Equal(t, test.body, packet.body)
		})
	}

	_, ok := decodeEngine("")
	assert.False(t, ok, "an empty frame decodes to nothing and is ignored")
}

// ---- Socket.IO decode (API-031) ------------------------------------------------------------------

func TestPacketDecode(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		kind        byte
		attachments int
		namespace   string
		ackID       int
		data        string
	}{
		{
			name: "an event in the root namespace",
			body: `2["message",{"a":1}]`,
			kind: sioEvent, namespace: "/", ackID: -1, data: `["message",{"a":1}]`,
		},
		{
			name: "an event in a named namespace",
			body: `2/chat,["message","hola"]`,
			kind: sioEvent, namespace: "/chat", ackID: -1, data: `["message","hola"]`,
		},
		{
			name: "a namespace with no payload is still a namespace",
			body: "0/chat",
			kind: sioConnect, namespace: "/chat", ackID: -1, data: "",
		},
		{
			name: "an ack keeps its id",
			body: `3/chat,17["ok"]`,
			kind: sioAck, namespace: "/chat", ackID: 17, data: `["ok"]`,
		},
		{
			name: "a binary packet keeps its attachment count",
			body: `51-/chat,["file",{"_placeholder":true,"num":0}]`,
			kind: sioBinaryEvent, attachments: 1, namespace: "/chat", ackID: -1,
			data: `["file",{"_placeholder":true,"num":0}]`,
		},
		{
			name: "a dash in the data is not an attachment count",
			// The count is recognised only when **every** character before the `-` is a digit, so
			// a payload that merely contains one stays data.
			body: `2["msg","a-b"]`,
			kind: sioEvent, namespace: "/", ackID: -1, data: `["msg","a-b"]`,
		},
		{
			name: "a connect error",
			body: `4{"message":"Not authorized"}`,
			kind: sioConnectError, namespace: "/", ackID: -1, data: `{"message":"Not authorized"}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			packet, err := decodePacket(test.body)
			require.NoError(t, err)

			assert.Equal(t, test.kind, packet.kind)
			assert.Equal(t, test.attachments, packet.attachments)
			assert.Equal(t, test.namespace, packet.namespace)
			assert.Equal(t, test.ackID, packet.ackID)
			assert.Equal(t, test.data, packet.data)
		})
	}
}

func TestAnUnrecognisedPacketTypeIsRefused(t *testing.T) {
	_, err := decodePacket("9algo")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unrecognised Socket.IO packet type")

	_, err = decodePacket("")
	assert.ErrorIs(t, err, errEmptySocketIOPacket)
}

// ---- Socket.IO encode (API-032) ------------------------------------------------------------------

// The root namespace has **no segment at all** — never `/,`, which is what a naive encoder emits
// and what every server rejects.
func TestRootNamespaceHasNoNamespaceSegment(t *testing.T) {
	for _, namespace := range []string{"", "/", "   "} {
		t.Run(namespace, func(t *testing.T) {
			assert.Equal(t, `42["message","hola"]`,
				messageFrame(sioEvent, namespace, `["message","hola"]`))
		})
	}
}

func TestNamedNamespaceIsCommaTerminated(t *testing.T) {
	assert.Equal(t, `42/chat,["message","hola"]`,
		messageFrame(sioEvent, "/chat", `["message","hola"]`))

	// Prefixed with `/` when the caller left it out.
	assert.Equal(t, `42/chat,["message","hola"]`,
		messageFrame(sioEvent, "chat", `["message","hola"]`))

	// And a CONNECT with no body is still comma-terminated.
	assert.Equal(t, "40/chat,", messageFrame(sioConnect, "/chat", ""))
}

// ---- event args (API-033) -------------------------------------------------------------------------

func TestEventArgsSplicesThePayloadRatherThanReEncodingIt(t *testing.T) {
	args, err := eventArgs("message", `{"a":1}`)
	require.NoError(t, err)
	// An object stays an object. Re-encoding would make it a string *containing* an object, which
	// the far side would deliver as a string.
	assert.Equal(t, `["message",{"a":1}]`, args)

	args, err = eventArgs("message", `  "hola"  `)
	require.NoError(t, err)
	assert.Equal(t, `["message","hola"]`, args)
}

func TestAnEventWithNoPayloadIsANameAlone(t *testing.T) {
	for _, payload := range []string{"", "   ", "\n"} {
		args, err := eventArgs("ping", payload)
		require.NoError(t, err)
		assert.Equal(t, `["ping"]`, args)
	}
}

func TestANonJSONPayloadIsRefusedBeforeAnythingGoesOut(t *testing.T) {
	_, err := eventArgs("message", "{not json}")
	require.Error(t, err)
	// The far side would drop the whole frame, and the user would see a send that appeared to work.
	assert.Contains(t, err.Error(), "not valid JSON")
}

func TestAnEventNameWithAQuoteIsEncoded(t *testing.T) {
	args, err := eventArgs(`el "evento"`, "")
	require.NoError(t, err)
	assert.Equal(t, `["el \"evento\""]`, args)
}

func TestSplitEventKeepsALoneArgumentsOwnShape(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		event   string
		payload string
	}{
		{
			name: "one argument keeps its JSON shape",
			data: `["msg","hi"]`,
			// A JSON string, not an array containing one.
			event: "msg", payload: `"hi"`,
		},
		{
			name:  "an object argument stays an object",
			data:  `["msg",{"a":1}]`,
			event: "msg", payload: `{"a":1}`,
		},
		{
			name:  "several arguments become an array",
			data:  `["msg","a","b"]`,
			event: "msg", payload: `["a","b"]`,
		},
		{
			name:  "a name alone has no payload",
			data:  `["ping"]`,
			event: "ping", payload: "",
		},
		{
			name: "something that is not an event array is shown as it arrived",
			data: `{"not":"an array"}`,
			// Shown rather than dropped: the console's job is to report what came.
			event: "", payload: `{"not":"an array"}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event, payload := splitEvent(test.data)
			assert.Equal(t, test.event, event)
			assert.Equal(t, test.payload, payload)
		})
	}
}

// ---- CONNECT auth (API-034) -------------------------------------------------------------------

func TestConnectCarriesAuthOnlyOnV4AndOnlyWhenItSaysSomething(t *testing.T) {
	tests := []struct {
		name     string
		authJSON string
		v4       bool
		expected string
	}{
		{name: "v4 with a real object", authJSON: `{"token":"abc"}`, v4: true, expected: `{"token":"abc"}`},
		{name: "v4 with the renderer's empty default", authJSON: `{}`, v4: true, expected: ""},
		{name: "v4 with nothing", authJSON: "", v4: true, expected: ""},
		{name: "v4 with malformed JSON", authJSON: "{no", v4: true, expected: ""},
		{name: "v4 with a non-object", authJSON: `"abc"`, v4: true, expected: ""},
		// v3 has no handshake-auth payload at all, whatever was configured.
		{name: "v3 never sends auth", authJSON: `{"token":"abc"}`, v4: false, expected: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, connectBody(test.authJSON, test.v4))
		})
	}
}

// ---- the handshake URL (API-037) ---------------------------------------------------------------

func TestHandshakeURLUpgradesTheSchemeAndKeepsExistingQuery(t *testing.T) {
	built, err := handshakeURL("https://api.test/algo?token=abc", "", true,
		[][2]string{{"room", "general"}})
	require.NoError(t, err)

	parsed, err := url.Parse(built)
	require.NoError(t, err)

	assert.Equal(t, "wss", parsed.Scheme)
	assert.Equal(t, "api.test", parsed.Host)
	// The path is a **mount point, not a suffix**: it replaces whatever the URL carried.
	assert.Equal(t, "/socket.io/", parsed.Path)

	query := parsed.Query()
	assert.Equal(t, "4", query.Get("EIO"))
	assert.Equal(t, "websocket", query.Get("transport"))
	assert.Equal(t, "abc", query.Get("token"), "the caller's own query survives")
	assert.Equal(t, "general", query.Get("room"))
}

func TestTheSchemeMapping(t *testing.T) {
	tests := map[string]string{
		"http://api.test/":  "ws",
		"https://api.test/": "wss",
		"ws://api.test/":    "ws",
		"wss://api.test/":   "wss",
	}

	for input, scheme := range tests {
		t.Run(input, func(t *testing.T) {
			built, err := handshakeURL(input, "", true, nil)
			require.NoError(t, err)

			parsed, err := url.Parse(built)
			require.NoError(t, err)
			assert.Equal(t, scheme, parsed.Scheme)
		})
	}

	// A bare `ftp://` errors before any socket is opened.
	_, err := handshakeURL("ftp://api.test/", "", true, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http(s) or ws(s)")

	_, err = handshakeURL("https://", "", true, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "names no host")
}

func TestThePathIsNormalisedAndAlwaysAbsolute(t *testing.T) {
	tests := map[string]string{
		"":             "/socket.io/",
		"/socket.io":   "/socket.io/",
		"/socket.io/":  "/socket.io/",
		"mi-socket":    "/mi-socket/",
		"/api/socket/": "/api/socket/",
	}

	for path, expected := range tests {
		t.Run(path, func(t *testing.T) {
			built, err := handshakeURL("https://api.test/lo-que-sea", path, true, nil)
			require.NoError(t, err)

			parsed, err := url.Parse(built)
			require.NoError(t, err)
			assert.Equal(t, expected, parsed.Path)
		})
	}
}

func TestTheEIOVersionFollowsTheTransportVersion(t *testing.T) {
	v4, err := handshakeURL("https://api.test/", "", true, nil)
	require.NoError(t, err)
	assert.Contains(t, v4, "EIO=4")

	v3, err := handshakeURL("https://api.test/", "", false, nil)
	require.NoError(t, err)
	assert.Contains(t, v3, "EIO=3")
}

// ---- WebSocket helpers (API-025, API-026) ---------------------------------------------------------

func TestNormalizesHTTPSchemesPreservingCaseOfTheRest(t *testing.T) {
	tests := map[string]string{
		"https://API.Test/Path?Q=1": "wss://API.Test/Path?Q=1",
		"http://API.Test/Path":      "ws://API.Test/Path",
		"HTTPS://api.test/":         "wss://api.test/",
		"HtTp://api.test/":          "ws://api.test/",
		// Already a WebSocket scheme, or something else entirely: untouched, and a bad one surfaces
		// as a dial error rather than being guessed at here.
		"wss://api.test/":       "wss://api.test/",
		"ws://api.test/":        "ws://api.test/",
		"ftp://api.test/":       "ftp://api.test/",
		"  https://api.test/  ": "wss://api.test/",
	}

	for input, expected := range tests {
		t.Run(input, func(t *testing.T) {
			assert.Equal(t, expected, normalizeScheme(input))
		})
	}
}

// The asymmetry is the rule: the first row overrides what the handshake generated, a repeat adds.
func TestFirstHeaderRowReplacesTheGeneratedOneAndRepeatsAppend(t *testing.T) {
	header := upgradeHeaders([][2]string{
		{"Origin", "https://primero.test"},
		{"X-Trace", "uno"},
		{"x-trace", "dos"},
		{"  ", "descartada"},
	}, nil)

	assert.Equal(t, []string{"https://primero.test"}, header.Values("Origin"))
	// Typed twice on purpose, so both travel.
	assert.Equal(t, []string{"uno", "dos"}, header.Values("X-Trace"))
	assert.Len(t, header, 2, "a blank name is skipped silently")
}

func TestSubprotocolsAreTrimmedAndEmptiesDropped(t *testing.T) {
	assert.Equal(t, []string{"graphql-ws", "json"},
		cleanSubprotocols([]string{" graphql-ws ", "", "json", "   "}))
	assert.Empty(t, cleanSubprotocols([]string{"", "  "}))
}

// `ping_interval_ms == 0` disables keepalive entirely.
func TestKeepaliveIsOffAtZero(t *testing.T) {
	assert.Nil(t, keepalive(0))

	ticker := keepalive(50)
	require.NotNil(t, ticker)
	ticker.Stop()
}
