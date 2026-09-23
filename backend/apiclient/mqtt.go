package apiclient

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// Connecting to an MQTT broker (API-040…045, API-047).

// The two versions the renderer can ask for, `VERBATIM`.
const (
	MqttVersion311 = "3.1.1"
	MqttVersion5   = "5.0"
)

// MqttLastWill is the message the broker publishes if this client disappears without saying
// goodbye.
type MqttLastWill struct {
	Topic   string `json:"topic"`
	Payload string `json:"payload"`
	QoS     byte   `json:"qos"`
	Retain  bool   `json:"retain"`
}

// MqttSubscribe is one topic filter to subscribe to on connect.
type MqttSubscribe struct {
	Topic string `json:"topic"`
	QoS   byte   `json:"qos"`
}

// MqttConnectRequest opens a broker connection.
type MqttConnectRequest struct {
	URL           string `json:"url"`
	ClientID      string `json:"client_id"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	KeepAliveSecs uint64 `json:"keep_alive_secs"`
	CleanSession  bool   `json:"clean_session"`
	// Version is `3.1.1` or `5.0`.
	Version       string          `json:"version"`
	LastWill      *MqttLastWill   `json:"last_will"`
	Subscriptions []MqttSubscribe `json:"subscriptions"`
	Options       NetworkOptions  `json:"options"`
}

// mqttEndpoint is a broker URL reduced to what a dial needs.
type mqttEndpoint struct {
	host string
	port int
	tls  bool
}

func (e mqttEndpoint) address() string { return net.JoinHostPort(e.host, strconv.Itoa(e.port)) }

// parseEndpoint reads a broker URL (API-040).
//
// Written by hand rather than through `net/url`, because half of what arrives is not a URL: a bare
// `broker.test:1883` is what most brokers publish as their address, and a parser that insisted on a
// scheme would reject the common case.
//
// Two rejections are explicit rather than silent. `ws://` is refused by name — falling back to plain
// TCP would dial the wrong port and look like a hang — and credentials in the URL are **stripped and
// discarded**, because they come from the request's own fields and a URL that carried a different
// password would silently win.
func parseEndpoint(rawURL string) (mqttEndpoint, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return mqttEndpoint{}, errNoBrokerURL
	}

	scheme := ""
	rest := trimmed
	if before, after, ok := strings.Cut(trimmed, "://"); ok {
		scheme, rest = strings.ToLower(before), after
	}

	useTLS := false
	defaultPort := 1883
	switch scheme {
	case "", "mqtt", "tcp":
	case "mqtts", "ssl", "tls":
		useTLS, defaultPort = true, 8883
	case "ws", "wss":
		return mqttEndpoint{}, fmt.Errorf( //nolint:err113 // names the scheme the user typed
			"MQTT over WebSocket (%s://) is not supported by this build — use mqtt:// or mqtts://",
			scheme)
	default:
		return mqttEndpoint{}, fmt.Errorf("Unsupported MQTT URL scheme '%s://'", scheme) //nolint:staticcheck,err113 // ST1005: VERBATIM
	}

	// A path or a query is tolerated and dropped: MQTT has no URL-path semantics, and a broker
	// address pasted from a dashboard often carries one.
	if cut := strings.IndexAny(rest, "/?"); cut >= 0 {
		rest = rest[:cut]
	}
	// Credentials pasted into the URL are discarded here, at the last `@` so a password containing
	// one survives being found.
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		rest = rest[at+1:]
	}

	host, port, err := splitHostPort(rest, defaultPort)
	if err != nil {
		return mqttEndpoint{}, err
	}
	return mqttEndpoint{host: host, port: port, tls: useTLS}, nil
}

var errNoBrokerURL = fmt.Errorf("the broker URL is empty") //nolint:err113 // a leaf state

// splitHostPort handles the bracketed IPv6 form as well as `host:port`.
func splitHostPort(value string, defaultPort int) (host string, port int, err error) {
	if strings.HasPrefix(value, "[") {
		closing := strings.Index(value, "]")
		if closing < 0 {
			return "", 0, fmt.Errorf("%q has no closing bracket", value) //nolint:err113 // names what was typed
		}
		host = value[1:closing]
		remainder := value[closing+1:]
		if remainder == "" {
			return requireHost(host, defaultPort)
		}
		if !strings.HasPrefix(remainder, ":") {
			return "", 0, fmt.Errorf("%q is not a host and port", value) //nolint:err113 // names what was typed
		}
		port, err = parsePort(remainder[1:])
		if err != nil {
			return "", 0, err
		}
		return requireHost(host, port)
	}

	// An unbracketed value with several colons is a bare IPv6 address, which carries no port.
	if strings.Count(value, ":") > 1 {
		return requireHost(value, defaultPort)
	}
	if colon := strings.LastIndex(value, ":"); colon >= 0 {
		port, err = parsePort(value[colon+1:])
		if err != nil {
			return "", 0, err
		}
		return requireHost(value[:colon], port)
	}
	return requireHost(value, defaultPort)
}

func requireHost(host string, port int) (string, int, error) {
	if strings.TrimSpace(host) == "" {
		return "", 0, errNoBrokerHost
	}
	return host, port, nil
}

var errNoBrokerHost = fmt.Errorf("the broker URL names no host") //nolint:err113 // a leaf state

func parsePort(value string) (int, error) {
	port, err := strconv.Atoi(value)
	if err != nil || port <= 0 || port > 65535 {
		return 0, fmt.Errorf("%q is not a port number", value) //nolint:err113 // names what was typed
	}
	return port, nil
}

// resolveClientID answers the caller's id, or mints one (API-041).
//
// Generated because brokers routinely reject an empty client id outright — and the shape is fixed at
// `codeflow-` plus eight hex digits, seventeen characters, because some brokers cap the id's length
// and a longer one would be refused for a reason nobody could see.
func resolveClientID(requested string) string {
	if trimmed := strings.TrimSpace(requested); trimmed != "" {
		return trimmed
	}

	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		// Unreachable in practice; a fixed id beats refusing to connect, and a collision only
		// matters against another client that also failed to read randomness.
		return "codeflow-00000000"
	}
	return "codeflow-" + hex.EncodeToString(suffix)
}

// protocolLevel maps the requested version onto its wire byte. Anything unrecognised is 3.1.1,
// which every broker speaks.
func protocolLevel(version string) byte {
	if strings.TrimSpace(version) == MqttVersion5 {
		return mqttLevel5
	}
	return mqttLevel311
}

// keepAliveFor applies the v5 floor and leaves v4 alone (API-044).
//
// The asymmetry is inherited rather than chosen: the original's v5 library asserts on anything under
// five seconds, so its caller raised the value before handing it over. 3.1.1 defines `0` as
// "keepalive disabled" and it passes through untouched. Do not tidy this into symmetry — a v4
// connection with `0` is a configuration a user can have made on purpose.
func keepAliveFor(seconds uint64, level byte) uint16 {
	if level == mqttLevel5 && seconds < 5 {
		seconds = 5
	}
	if seconds > 65535 {
		// The field is two bytes on the wire; a larger value would wrap to something small and
		// disconnect a connection the user thought was long-lived.
		seconds = 65535
	}
	return uint16(seconds) //nolint:gosec // G115: bounded just above
}

// lastWillFor drops a will with no topic (API-043).
//
// Only the topic decides: a will with a payload, a QoS and a retain flag but no topic addresses
// nothing, and the rest of it is not inspected.
func lastWillFor(will *MqttLastWill) *MqttLastWill {
	if will == nil || strings.TrimSpace(will.Topic) == "" {
		return nil
	}
	return will
}

// dialBroker opens the TCP or TLS connection.
//
// Both paths take the context, so a connect to a broker that is merely slow can be abandoned by
// closing the panel rather than waiting out the timeout.
func dialBroker(ctx context.Context, endpoint mqttEndpoint, options NetworkOptions, report reporter) (net.Conn, error) {
	// Reported rather than dropped (API-047): a user who configured a proxy and saw nothing about
	// it would reasonably conclude the connection went through one.
	noteIgnoredOptions(options, report)

	dialer := &net.Dialer{Timeout: millis(options.TimeoutMs)}

	if !endpoint.tls {
		connection, err := dialer.DialContext(ctx, "tcp", endpoint.address())
		if err != nil {
			return nil, fmt.Errorf("couldn't reach %s: %w", endpoint.address(), err)
		}
		return connection, nil
	}

	config, err := brokerTLS(endpoint, options)
	if err != nil {
		return nil, err
	}

	secure := &tls.Dialer{NetDialer: dialer, Config: config}
	connection, err := secure.DialContext(ctx, "tcp", endpoint.address())
	if err != nil {
		return nil, fmt.Errorf("couldn't reach %s: %w", endpoint.address(), err)
	}
	return connection, nil
}

// brokerTLS builds the TLS configuration.
//
// A configured CA **replaces** the system roots rather than adding to them, which is what the
// original does and what a private broker with its own CA needs. Unlike the HTTP transport, which
// adds one on top — the difference is preserved rather than reconciled, because a user who pointed
// this at a private CA is saying that is the only one that should be trusted here.
func brokerTLS(endpoint mqttEndpoint, options NetworkOptions) (*tls.Config, error) {
	config := &tls.Config{
		ServerName: endpoint.host,
		//nolint:gosec // G402: the user asked for it, per connection, and the transcript says so
		InsecureSkipVerify: !options.VerifySSL,
		MinVersion:         tls.VersionTLS12,
	}

	if path := strings.TrimSpace(options.CACertPath); path != "" {
		pem, err := os.ReadFile(path) //nolint:gosec // G304: a path the user picked in a dialog
		if err != nil {
			return nil, fmt.Errorf("read the CA bundle %s: %w", path, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("%s holds no PEM certificate this client could read", path) //nolint:err113 // names the file
		}
		config.RootCAs = pool
	}
	return config, nil
}

// noteIgnoredOptions says out loud what MQTT cannot honour (API-047).
func noteIgnoredOptions(options NetworkOptions, report reporter) {
	if strings.TrimSpace(options.ProxyURL) != "" {
		report.system("Proxy is not supported for MQTT — connecting directly")
	}
	if strings.TrimSpace(options.ClientCertPath) != "" {
		report.system("Client certificates are not supported for MQTT — connecting without one")
	}
}
