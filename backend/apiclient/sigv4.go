package apiclient

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// AWS Signature Version 4 (API-011…015).
//
// Byte-identical against Amazon's published `get-vanilla` vector, which is the only way to know this
// is right: every part of it is a string built to be hashed, so "nearly correct" and "correct" look
// the same until a server answers 403 with nothing useful in the body.

// sigv4Algorithm is the only one implemented.
const sigv4Algorithm = "AWS4-HMAC-SHA256"

// unsignedPayload is what a multipart body signs as. AWS accepts it explicitly for exactly this
// case: the boundary and the assembled bytes are produced inside the HTTP client and are not
// knowable at signing time.
const unsignedPayload = "UNSIGNED-PAYLOAD"

// unsignableHeaders are excluded from the signed set entirely (API-012).
//
// The same set the AWS SDKs exclude, and for the same reason: the transport rewrites or adds every
// one of them after signing, so a signature over them would be a signature over a guess.
var unsignableHeaders = map[string]bool{
	"accept-encoding":     true,
	"authorization":       true,
	"connection":          true,
	"content-length":      true,
	"expect":              true,
	"keep-alive":          true,
	"proxy-authorization": true,
	"te":                  true,
	"transfer-encoding":   true,
	"user-agent":          true,
}

// ErrIncompleteAWSCredentials refuses before any network access: a request signed with a blank key
// or an unnamed region fails at the far end with a message about the signature, which sends the
// reader looking in the wrong place.
var ErrIncompleteAWSCredentials = errors.New("AWS SigV4 needs an access key, a secret key, a region and a service") //nolint:staticcheck // ST1005: VERBATIM

// sigv4Sign builds the canonical request, the string to sign and the signature (API-011).
//
// The canonical request's shape is exact, and the blank line before the signed-header list is
// literal rather than a join artefact: the header block already ends in a `\n` per header.
func sigv4Sign(method, canonicalURI, canonicalQuery string, headers [][2]string, payloadHash, secretKey, region, service, amzDate string) (signedHeaders, signature string) {
	names, canonicalHeaders := canonicalHeaderBlock(headers)
	signedHeaders = strings.Join(names, ";")

	canonicalRequest := strings.Join([]string{
		method,
		canonicalURI,
		canonicalQuery,
		canonicalHeaders + "\n",
		signedHeaders,
		payloadHash,
	}, "\n")

	date := amzDate[:8]
	scope := strings.Join([]string{date, region, service, "aws4_request"}, "/")

	stringToSign := strings.Join([]string{
		sigv4Algorithm,
		amzDate,
		scope,
		hex.EncodeToString(sha256Of([]byte(canonicalRequest))),
	}, "\n")

	// The four-step derivation. The access key never enters it — only the header — so it cannot
	// drift into the maths.
	key := hmacSHA256([]byte("AWS4"+secretKey), date)
	key = hmacSHA256(key, region)
	key = hmacSHA256(key, service)
	key = hmacSHA256(key, "aws4_request")

	return signedHeaders, hex.EncodeToString(hmacSHA256(key, stringToSign))
}

// canonicalHeaderBlock lower-cases the names, merges repeats and collapses each value's whitespace.
//
// Repeated names merge into one comma-joined value **in send order**: only the names are sorted, not
// the values within one. Getting that backwards produces a signature that is stable, plausible and
// wrong.
func canonicalHeaderBlock(headers [][2]string) (names []string, block string) {
	merged := make(map[string][]string, len(headers))
	order := make([]string, 0, len(headers))

	for _, header := range headers {
		name := strings.ToLower(strings.TrimSpace(header[0]))
		if name == "" {
			continue
		}
		if _, seen := merged[name]; !seen {
			order = append(order, name)
		}
		merged[name] = append(merged[name], normalizeHeaderValue(header[1]))
	}

	sort.Strings(order)

	lines := make([]string, 0, len(order))
	for _, name := range order {
		lines = append(lines, name+":"+strings.Join(merged[name], ","))
	}
	return order, strings.Join(lines, "\n")
}

// normalizeHeaderValue collapses internal whitespace runs to a single space and trims the ends.
func normalizeHeaderValue(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

// sigv4Headers signs a request and answers the headers to add (API-012).
//
// `host` is signed first and always, then the caller's signable headers, then the two `x-amz-*` the
// signature itself needs, then the session token only when there is one.
func sigv4Headers(method, rawURL string, headers [][2]string, payloadHash string, auth BackendAuth, amzDate string) ([][2]string, error) {
	if auth.AccessKey == "" || auth.SecretKey == "" || auth.Region == "" || auth.Service == "" {
		return nil, ErrIncompleteAWSCredentials
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse the URL to sign: %w", err)
	}

	signable := make([][2]string, 0, len(headers)+4)
	signable = append(signable, [2]string{"host", parsed.Host})
	for _, header := range headers {
		if unsignableHeaders[strings.ToLower(strings.TrimSpace(header[0]))] {
			continue
		}
		signable = append(signable, header)
	}
	signable = append(signable,
		[2]string{"x-amz-date", amzDate},
		[2]string{"x-amz-content-sha256", payloadHash})
	if auth.SessionToken != "" {
		signable = append(signable, [2]string{"x-amz-security-token", auth.SessionToken})
	}

	signedHeaders, signature := sigv4Sign(method,
		canonicalURI(parsed, auth.Service), canonicalQuery(parsed), signable,
		payloadHash, auth.SecretKey, auth.Region, auth.Service, amzDate)

	authorization := fmt.Sprintf(
		"%s Credential=%s/%s/%s/%s/aws4_request, SignedHeaders=%s, Signature=%s",
		sigv4Algorithm, auth.AccessKey, amzDate[:8], auth.Region, auth.Service,
		signedHeaders, signature)

	added := [][2]string{
		{"x-amz-date", amzDate},
		{"x-amz-content-sha256", payloadHash},
		{"Authorization", authorization},
	}
	if auth.SessionToken != "" {
		added = append(added, [2]string{"x-amz-security-token", auth.SessionToken})
	}
	return added, nil
}

// canonicalURI is the path, encoded once more for every service **except** S3 (API-013).
//
// `EscapedPath` and not `Path`, and the difference is the rule: AWS requires each path segment to be
// URI-encoded **twice** for every service but S3, so the encoder has to run over the form that is
// still percent-encoded — a wire path of `/a%20b` signs as `/a%2520b`, which is what the `% → %25`
// in the specification's own note describes. Running it over Go's decoded `Path` would produce
// `/a%20b`, a single encoding: plausible, stable, and rejected with a signature mismatch.
//
// S3 is signed exactly as it appears on the wire — AWS's own carve-out for it, not an oversight
// here. An empty path signs as `/`.
func canonicalURI(parsed *url.URL, service string) string {
	path := parsed.EscapedPath()
	if path == "" {
		return "/"
	}
	if strings.EqualFold(service, "s3") {
		return path
	}
	return percentEncode(path, true)
}

// canonicalQuery encodes every pair, then sorts by the **encoded** tuple (API-014).
//
// Encode-then-sort, not sort-then-encode: the two differ wherever an encoded character sorts
// differently from its raw form, and only one of them matches what AWS computes. Repeated keys are
// not merged — each keeps its own place in the sorted order.
func canonicalQuery(parsed *url.URL) string {
	raw := parsed.RawQuery
	if raw == "" {
		return ""
	}

	pairs := make([]string, 0, 8)
	for _, pair := range strings.Split(raw, "&") {
		if pair == "" {
			continue
		}
		key, value, _ := strings.Cut(pair, "=")
		// The values arrive percent-encoded in RawQuery; they are decoded and re-encoded with
		// SigV4's own unreserved set, which is not the same as the URL parser's.
		pairs = append(pairs,
			percentEncode(decodeQueryComponent(key), false)+"="+
				percentEncode(decodeQueryComponent(value), false))
	}

	sort.Strings(pairs)
	return strings.Join(pairs, "&")
}

// decodeQueryComponent undoes one round of percent-encoding, leaving a stray `%` alone rather than
// failing: a URL the user typed is not required to be well-formed for a signature to be attempted.
func decodeQueryComponent(value string) string {
	decoded, err := url.QueryUnescape(value)
	if err != nil {
		return value
	}
	return decoded
}

// percentEncode encodes with SigV4's unreserved set: `A-Za-z0-9-_.~` pass through, and `/` too when
// `keepSlash` — which is the one difference between the path's set and the query's.
func percentEncode(value string, keepSlash bool) string {
	out := &strings.Builder{}
	out.Grow(len(value))

	for i := 0; i < len(value); i++ {
		b := value[i]
		switch {
		case b >= 'A' && b <= 'Z', b >= 'a' && b <= 'z', b >= '0' && b <= '9',
			b == '-', b == '_', b == '.', b == '~':
			out.WriteByte(b)
		case b == '/' && keepSlash:
			out.WriteByte('/')
		default:
			fmt.Fprintf(out, "%%%02X", b)
		}
	}
	return out.String()
}

func sha256Of(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}
