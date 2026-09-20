package apiclient

import (
	"crypto/md5" //nolint:gosec // G501: RFC 7616 names MD5 as a digest algorithm; the choice is the server's
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// HTTP Digest access authentication, RFC 7616 (API-007…010).
//
// The flow is one round trip: send unauthenticated, and only on a 401 carrying a Digest challenge
// compute the response and send again. The body therefore goes out twice — there is no
// 100-continue dance — which is a cost the scheme imposes rather than one this port chose.

// The four hashes, and nothing else. An unrecognised `algorithm` fails naming these.
const (
	hashMD5        = "MD5"
	hashMD5Sess    = "MD5-SESS"
	hashSHA256     = "SHA-256"
	hashSHA256Sess = "SHA-256-SESS"
)

// digestNonceCount is always this literal (API-010).
//
// Valid precisely because there is no nonce cache: every Digest request performs its own fresh 401
// round trip, so the nonce it answers is always being used for the first time. The "reuse a nonce
// with an incrementing count" optimisation the RFC allows is never exercised.
const digestNonceCount = "00000001"

// ErrNoDigestChallenge is a 401 with nothing to answer.
var ErrNoDigestChallenge = errors.New("no 'WWW-Authenticate: Digest' challenge") //nolint:staticcheck // ST1005: VERBATIM

// digestChallenge finds and parses the Digest challenge in a response's headers (API-008).
//
// **`BUG-API-b` is preserved here.** Each `WWW-Authenticate` header *instance* is checked
// independently, and one is recognised only when `Digest` occupies its first six characters. RFC
// 7235 allows several challenges combined in one comma-separated value — `Basic realm="x", Digest
// realm="y"` — and a server that does that, which is legal and seen in the wild, has its Digest
// challenge missed unless Digest happens to be first. Suspected-correct behaviour is to split each
// value on scheme boundaries before matching. Ported as-is; not fixed.
func digestChallenge(header http.Header) (map[string]string, bool) {
	for _, value := range header.Values("WWW-Authenticate") {
		if len(value) < 6 || !strings.EqualFold(value[:6], "Digest") {
			continue
		}
		return parseAuthParams(strings.TrimSpace(value[6:])), true
	}
	return nil, false
}

// parseAuthParams reads `key=value` pairs, honouring quotes.
//
// The quote handling is the whole point: `qop="auth,auth-int"` carries a comma **inside** a quoted
// value, and splitting on commas first turns one parameter into two malformed ones.
func parseAuthParams(raw string) map[string]string {
	params := make(map[string]string, 8)

	key := &strings.Builder{}
	value := &strings.Builder{}
	inValue, inQuotes := false, false

	flush := func() {
		name := strings.ToLower(strings.TrimSpace(key.String()))
		if name != "" {
			params[name] = strings.TrimSpace(value.String())
		}
		key.Reset()
		value.Reset()
		inValue = false
	}

	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case c == '"':
			inQuotes = !inQuotes
		case c == '=' && !inValue && !inQuotes:
			inValue = true
		case c == ',' && !inQuotes:
			flush()
		case inValue:
			value.WriteByte(c)
		default:
			key.WriteByte(c)
		}
	}
	flush()

	return params
}

// digestHash resolves the challenge's `algorithm` to one of the four, defaulting to MD5.
func digestHash(algorithm string) (name string, session bool, err error) {
	normalised := strings.ToUpper(strings.TrimSpace(algorithm))
	if normalised == "" {
		return hashMD5, false, nil
	}

	switch normalised {
	case hashMD5:
		return hashMD5, false, nil
	case hashMD5Sess:
		return hashMD5, true, nil
	case hashSHA256:
		return hashSHA256, false, nil
	case hashSHA256Sess:
		return hashSHA256, true, nil
	default:
		return "", false, fmt.Errorf(
			"unsupported digest algorithm %q; this client implements MD5, MD5-sess, SHA-256 and SHA-256-sess",
			algorithm)
	}
}

func digestSum(hash, value string) string {
	if hash == hashSHA256 {
		sum := sha256.Sum256([]byte(value))
		return hex.EncodeToString(sum[:])
	}
	sum := md5.Sum([]byte(value)) //nolint:gosec // G401: the server chose MD5; refusing it breaks the handshake
	return hex.EncodeToString(sum[:])
}

// digestResponse computes the `response` value (API-009).
//
// Two shapes, and which one applies is the challenge's decision:
//
//	qop=auth  → H(HA1:nonce:nc:cnonce:qop:HA2)
//	no qop    → H(HA1:nonce:HA2)              — the RFC 2069 fallback
//
// `-sess` folds the two nonces into HA1, so a leaked HA1 expires with the nonce rather than lasting
// as long as the password does.
func digestResponse(hash string, session bool, username, password, realm, nonce, cnonce, nc, qop, method, uri string) string {
	ha1 := digestSum(hash, username+":"+realm+":"+password)
	if session {
		ha1 = digestSum(hash, ha1+":"+nonce+":"+cnonce)
	}
	ha2 := digestSum(hash, method+":"+uri)

	if qop == "" {
		return digestSum(hash, ha1+":"+nonce+":"+ha2)
	}
	return digestSum(hash, strings.Join([]string{ha1, nonce, nc, cnonce, qop, ha2}, ":"))
}

// digestAuthorization builds the whole header value for a challenge (API-007, API-009).
//
// Only `qop=auth` is implemented. `auth-int` hashes the body into the digest, and it is never
// attempted even when the server offers it and prefers it — a request that claimed `auth-int` and
// computed `auth` would be rejected in a way that reads as a wrong password.
func digestAuthorization(auth BackendAuth, challenge map[string]string, method, uri string) (string, error) {
	hash, session, err := digestHash(challenge["algorithm"])
	if err != nil {
		return "", err
	}

	qop, err := negotiateQop(challenge["qop"])
	if err != nil {
		return "", err
	}

	cnonce, err := freshCnonce()
	if err != nil {
		return "", err
	}

	realm, nonce := challenge["realm"], challenge["nonce"]
	response := digestResponse(hash, session, auth.Username, auth.Password,
		realm, nonce, cnonce, digestNonceCount, qop, method, uri)

	// Assembled in this order, and `algorithm` is echoed **only if the challenge carried one** —
	// never invented, because a server that omitted it is not promising to accept it back.
	parts := []string{
		fmt.Sprintf("username=%q", auth.Username),
		fmt.Sprintf("realm=%q", realm),
		fmt.Sprintf("nonce=%q", nonce),
		fmt.Sprintf("uri=%q", uri),
		fmt.Sprintf("response=%q", response),
	}
	if algorithm := strings.TrimSpace(challenge["algorithm"]); algorithm != "" {
		parts = append(parts, "algorithm="+algorithm)
	}
	if opaque, found := challenge["opaque"]; found {
		parts = append(parts, fmt.Sprintf("opaque=%q", opaque))
	}
	if qop != "" {
		parts = append(parts,
			"qop="+qop,
			"nc="+digestNonceCount,
			fmt.Sprintf("cnonce=%q", cnonce))
	}
	return "Digest " + strings.Join(parts, ", "), nil
}

// negotiateQop picks `auth` out of the offered list, or answers "" for a challenge with no qop at
// all — which is the RFC 2069 fallback rather than a failure.
func negotiateQop(offered string) (string, error) {
	trimmed := strings.TrimSpace(offered)
	if trimmed == "" {
		return "", nil
	}

	for _, candidate := range strings.Split(trimmed, ",") {
		if strings.EqualFold(strings.TrimSpace(candidate), "auth") {
			return "auth", nil
		}
	}
	return "", fmt.Errorf(
		"the server only offers digest qop=%q; this client implements qop=auth", trimmed)
}

// freshCnonce is 16 random bytes, hex-encoded, regenerated for every send (API-010).
func freshCnonce() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate a client nonce: %w", err)
	}
	return hex.EncodeToString(buffer), nil
}

// digestURI is the `request-target` the signature covers: `path[?query]` of the URL the challenge
// was issued from, **not** the originally-typed one if a redirect intervened.
func digestURI(rawURL string) string {
	parsed, err := parseRequestURL(rawURL)
	if err != nil {
		return "/"
	}

	uri := parsed.EscapedPath()
	if uri == "" {
		uri = "/"
	}
	if parsed.RawQuery != "" {
		uri += "?" + parsed.RawQuery
	}
	return uri
}
