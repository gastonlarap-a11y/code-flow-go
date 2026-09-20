package apiclient

// Internal, unlike every other test in this package: the twelve published vectors exercise pure
// functions that are not part of the wire surface — a canonical query string, a digest response, a
// charset transcode — and exporting them to be tested would widen the package's API to suit its
// tests rather than its callers.

import (
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/testvectors"
)

// The twelve cases of `http.vectors.json`, consumed unchanged.
//
// They are the reason this slice is tractable. SigV4 against Amazon's own `get-vanilla` vector and
// Digest against RFC 2617's worked example are the two places where "nearly right" and "right" are
// indistinguishable until a server answers 403 or 401 with nothing useful in it — and both are
// pure string-building, so a test can settle them exactly.
func loadCase(t *testing.T, id string) testvectors.Case {
	t.Helper()

	fixtures, err := testvectors.Load("http.vectors.json")
	require.NoError(t, err)

	for _, fixture := range fixtures {
		for _, one := range fixture.Cases {
			if one.ID == id {
				return one
			}
		}
	}
	t.Fatalf("no case %q in http.vectors.json", id)
	return testvectors.Case{}
}

func decodeInto(t *testing.T, raw json.RawMessage, into any) {
	t.Helper()
	require.NoError(t, json.Unmarshal(raw, into))
}

// ---- AWS SigV4 ---------------------------------------------------------------------------------

func TestSigV4MatchesTheGetVanillaVector(t *testing.T) {
	one := loadCase(t, "sigv4-get-vanilla")

	var input struct {
		Method         string      `json:"method"`
		CanonicalURI   string      `json:"canonical_uri"`
		CanonicalQuery string      `json:"canonical_query"`
		Headers        [][2]string `json:"headers"`
		PayloadHash    string      `json:"payload_hash"`
		SecretKey      string      `json:"secret_key"`
		Region         string      `json:"region"`
		Service        string      `json:"service"`
		AmzDate        string      `json:"amz_date"`
	}
	var expected struct {
		SignedHeaders string `json:"signed_headers"`
		Signature     string `json:"signature"`
	}
	decodeInto(t, one.Input, &input)
	decodeInto(t, one.Expected, &expected)

	signedHeaders, signature := sigv4Sign(input.Method, input.CanonicalURI, input.CanonicalQuery,
		input.Headers, input.PayloadHash, input.SecretKey, input.Region, input.Service, input.AmzDate)

	assert.Equal(t, expected.SignedHeaders, signedHeaders)
	// Byte-identical against what Amazon published, which is the only evidence worth having here.
	assert.Equal(t, expected.Signature, signature)
}

func TestSigV4HeadersSignHostDateAndPayloadHash(t *testing.T) {
	one := loadCase(t, "sigv4-headers-host-date-payload-hash")

	var input struct {
		Method       string      `json:"method"`
		URL          string      `json:"url"`
		Headers      [][2]string `json:"headers"`
		PayloadHash  string      `json:"payload_hash"`
		AccessKey    string      `json:"access_key"`
		SecretKey    string      `json:"secret_key"`
		SessionToken string      `json:"session_token"`
		Region       string      `json:"region"`
		Service      string      `json:"service"`
		AmzDate      string      `json:"amz_date"`
	}
	var expected struct {
		HeadersContains map[string]string `json:"headers_contains"`
	}
	decodeInto(t, one.Input, &input)
	decodeInto(t, one.Expected, &expected)

	added, err := sigv4Headers(input.Method, input.URL, input.Headers, input.PayloadHash,
		BackendAuth{
			Kind: AuthAWSV4, AccessKey: input.AccessKey, SecretKey: input.SecretKey,
			SessionToken: input.SessionToken, Region: input.Region, Service: input.Service,
		}, input.AmzDate)
	require.NoError(t, err)

	got := map[string]string{}
	for _, header := range added {
		got[lower(header[0])] = header[1]
	}

	for name, value := range expected.HeadersContains {
		assert.Equal(t, value, got[lower(name)], "%s", name)
	}

	// `accept-encoding` was in the caller's headers and must not be signed: the transport rewrites
	// it after signing, so a signature over it would be a signature over a guess.
	assert.NotContains(t, got["authorization"], "accept-encoding")
}

func TestCanonicalQuerySortsAndEncodes(t *testing.T) {
	one := loadCase(t, "canonical-query-sort-and-encode")

	var input struct {
		URL string `json:"url"`
	}
	var expected struct {
		CanonicalQuery string `json:"canonical_query"`
	}
	decodeInto(t, one.Input, &input)
	decodeInto(t, one.Expected, &expected)

	parsed, err := parseRequestURL(input.URL)
	require.NoError(t, err)

	// Encode **then** sort: the two orders differ wherever an encoded character sorts differently
	// from its raw form, and only one of them is what AWS computes. Repeated keys keep both entries.
	assert.Equal(t, expected.CanonicalQuery, canonicalQuery(parsed))
}

func TestCanonicalURIDoubleEncodesEverythingButS3(t *testing.T) {
	parsed, err := parseRequestURL("https://example.test/a%20b/c+d")
	require.NoError(t, err)

	// Twice for everything but S3: the wire path `/a%20b` signs as `/a%2520b`. Encoding the
	// *decoded* path instead gives `/a%20b` — one encoding, and a signature mismatch the server
	// reports as a plain 403.
	assert.Equal(t, "/a%2520b/c%2Bd", canonicalURI(parsed, "execute-api"))
	// S3 is signed exactly as it appears on the wire — AWS's own carve-out, not an oversight.
	assert.Equal(t, "/a%20b/c+d", canonicalURI(parsed, "s3"))
	assert.Equal(t, "/a%20b/c+d", canonicalURI(parsed, "S3"), "matched case-insensitively")

	empty, err := parseRequestURL("https://example.test")
	require.NoError(t, err)
	assert.Equal(t, "/", canonicalURI(empty, "service"))
}

func TestSigV4RefusesIncompleteCredentialsBeforeAnyNetworkAccess(t *testing.T) {
	for _, auth := range []BackendAuth{
		{Kind: AuthAWSV4, SecretKey: "s", Region: "r", Service: "v"},
		{Kind: AuthAWSV4, AccessKey: "a", Region: "r", Service: "v"},
		{Kind: AuthAWSV4, AccessKey: "a", SecretKey: "s", Service: "v"},
		{Kind: AuthAWSV4, AccessKey: "a", SecretKey: "s", Region: "r"},
	} {
		_, err := sigv4Headers("GET", "https://x.test/", nil, "hash", auth, "20150830T123600Z")
		assert.ErrorIs(t, err, ErrIncompleteAWSCredentials)
	}
}

func TestRepeatedHeadersMergeInSendOrder(t *testing.T) {
	// Only the header *names* are sorted; the values within one keep the order they were sent in.
	// Getting that backwards produces a signature that is stable, plausible and wrong.
	names, block := canonicalHeaderBlock([][2]string{
		{"X-Amz-Meta", "  segundo   valor "},
		{"Host", "example.test"},
		{"x-amz-meta", "primero"},
	})

	assert.Equal(t, []string{"host", "x-amz-meta"}, names)
	assert.Equal(t, "host:example.test\nx-amz-meta:segundo valor,primero", block)
}

// ---- Digest ------------------------------------------------------------------------------------

func TestDigestMatchesTheRFC2617Example(t *testing.T) {
	one := loadCase(t, "digest-rfc2617-worked-example")

	var input struct {
		Hash     string  `json:"hash"`
		Session  bool    `json:"session"`
		Username string  `json:"username"`
		Password string  `json:"password"`
		Realm    string  `json:"realm"`
		Nonce    string  `json:"nonce"`
		Cnonce   string  `json:"cnonce"`
		NC       string  `json:"nc"`
		Qop      *string `json:"qop"`
		Method   string  `json:"method"`
		URI      string  `json:"uri"`
	}
	var expected struct {
		Response string `json:"response"`
	}
	decodeInto(t, one.Input, &input)
	decodeInto(t, one.Expected, &expected)

	qop := ""
	if input.Qop != nil {
		qop = *input.Qop
	}

	assert.Equal(t, expected.Response, digestResponse(hashMD5, input.Session,
		input.Username, input.Password, input.Realm, input.Nonce, input.Cnonce,
		input.NC, qop, input.Method, input.URI))
}

func TestDigestFallsBackToTheRFC2069Form(t *testing.T) {
	one := loadCase(t, "digest-rfc2069-fallback")

	var input struct {
		Username string  `json:"username"`
		Password string  `json:"password"`
		Realm    string  `json:"realm"`
		Nonce    string  `json:"nonce"`
		Cnonce   string  `json:"cnonce"`
		NC       string  `json:"nc"`
		Qop      *string `json:"qop"`
		Method   string  `json:"method"`
		URI      string  `json:"uri"`
	}
	var expected struct {
		Response string `json:"response"`
	}
	decodeInto(t, one.Input, &input)
	decodeInto(t, one.Expected, &expected)

	require.Nil(t, input.Qop, "the fixture is the no-qop case")

	// No qop at all drops `nc`, `cnonce` and `qop` from the hash entirely: H(HA1:nonce:HA2).
	assert.Equal(t, expected.Response, digestResponse(hashMD5, false,
		input.Username, input.Password, input.Realm, input.Nonce, input.Cnonce,
		input.NC, "", input.Method, input.URI))
}

func TestAuthParamsSurviveCommasInsideQuotes(t *testing.T) {
	one := loadCase(t, "auth-params-quoted-commas")

	var input struct {
		Raw string `json:"raw"`
	}
	var expected map[string]string
	decodeInto(t, one.Input, &input)
	decodeInto(t, one.Expected, &expected)

	// `qop="auth,auth-int"` carries a comma **inside** a quoted value. Splitting on commas first
	// turns one parameter into two malformed ones.
	assert.Equal(t, expected, parseAuthParams(input.Raw))
}

func TestTheSessionVariantsFoldTheNoncesIntoHA1(t *testing.T) {
	plain := digestResponse(hashMD5, false, "u", "p", "r", "nonce", "cnonce", "00000001", "auth", "GET", "/")
	session := digestResponse(hashMD5, true, "u", "p", "r", "nonce", "cnonce", "00000001", "auth", "GET", "/")

	// A leaked HA1 then expires with the nonce rather than lasting as long as the password does.
	assert.NotEqual(t, plain, session)

	// And SHA-256 is a different hash, not a different encoding of the same one.
	assert.NotEqual(t, plain,
		digestResponse(hashSHA256, false, "u", "p", "r", "nonce", "cnonce", "00000001", "auth", "GET", "/"))
	assert.Len(t, digestResponse(hashSHA256, false, "u", "p", "r", "n", "c", "00000001", "auth", "GET", "/"), 64)
	assert.Len(t, plain, 32)
}

func TestDigestHashSelection(t *testing.T) {
	tests := []struct {
		algorithm string
		hash      string
		session   bool
	}{
		{algorithm: "", hash: hashMD5, session: false},
		{algorithm: "MD5", hash: hashMD5, session: false},
		{algorithm: "MD5-sess", hash: hashMD5, session: true},
		{algorithm: "SHA-256", hash: hashSHA256, session: false},
		{algorithm: "sha-256-sess", hash: hashSHA256, session: true},
	}

	for _, test := range tests {
		t.Run(test.algorithm, func(t *testing.T) {
			hash, session, err := digestHash(test.algorithm)
			require.NoError(t, err)
			assert.Equal(t, test.hash, hash)
			assert.Equal(t, test.session, session)
		})
	}

	_, _, err := digestHash("SHA-512")
	require.Error(t, err)
	// The message names the four it does implement, rather than only what it refused.
	assert.Contains(t, err.Error(), "MD5, MD5-sess, SHA-256 and SHA-256-sess")
}

func TestOnlyQopAuthIsImplemented(t *testing.T) {
	negotiated, err := negotiateQop("auth-int,auth")
	require.NoError(t, err)
	assert.Equal(t, "auth", negotiated, "picked out of a list that offers both")

	// `auth-int` hashes the body into the digest and is never attempted, even when it is all the
	// server offers — a request that claimed it and computed `auth` would be rejected in a way that
	// reads as a wrong password.
	_, err = negotiateQop("auth-int")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "this client implements qop=auth")

	// No qop at all is the RFC 2069 fallback, not a failure.
	fallback, err := negotiateQop("  ")
	require.NoError(t, err)
	assert.Empty(t, fallback)
}

// `BUG-API-b`, preserved and pinned: a combined multi-scheme header value has its Digest challenge
// missed unless Digest happens to be first.
func TestTheDigestChallengeParserMissesACombinedHeaderValue(t *testing.T) {
	header := map[string][]string{
		"Www-Authenticate": {`Basic realm="x", Digest realm="y", nonce="abc"`},
	}

	_, found := digestChallenge(header)
	assert.False(t, found,
		"RFC 7235 allows this and the parser misses it — BUG-API-b, ported as-is")

	// Digest first in the same value is recognised, which is what makes the bug intermittent
	// rather than total.
	first := map[string][]string{
		"Www-Authenticate": {`Digest realm="y", nonce="abc"`},
	}
	challenge, found := digestChallenge(first)
	require.True(t, found)
	assert.Equal(t, "y", challenge["realm"])
}

func TestEachChallengeHeaderInstanceIsCheckedIndependently(t *testing.T) {
	// Two separate header instances is the shape that does work, and the common one.
	header := map[string][]string{
		"Www-Authenticate": {`Basic realm="x"`, `Digest realm="y", nonce="abc", qop="auth"`},
	}

	challenge, found := digestChallenge(header)
	require.True(t, found)
	assert.Equal(t, "abc", challenge["nonce"])
	assert.Equal(t, "auth", challenge["qop"])
}

func TestTheAuthorizationHeaderEchoesOnlyWhatTheChallengeCarried(t *testing.T) {
	auth := BackendAuth{Kind: AuthDigest, Username: "Mufasa", Password: "Circle Of Life"}

	t.Run("no algorithm in the challenge means none in the header", func(t *testing.T) {
		value, err := digestAuthorization(auth,
			map[string]string{"realm": "r", "nonce": "n", "qop": "auth"}, "GET", "/dir/index.html")
		require.NoError(t, err)

		// Never invented: a server that omitted it is not promising to accept it back.
		assert.NotContains(t, value, "algorithm=")
		assert.Contains(t, value, "qop=auth")
		assert.Contains(t, value, "nc="+digestNonceCount)
		assert.Contains(t, value, `cnonce="`)
	})

	t.Run("no qop means no nc or cnonce either", func(t *testing.T) {
		value, err := digestAuthorization(auth,
			map[string]string{"realm": "r", "nonce": "n", "algorithm": "MD5"}, "GET", "/")
		require.NoError(t, err)

		assert.Contains(t, value, "algorithm=MD5")
		assert.NotContains(t, value, "qop=")
		assert.NotContains(t, value, "nc=")
		assert.NotContains(t, value, "cnonce=")
	})

	t.Run("an opaque is echoed when there is one", func(t *testing.T) {
		value, err := digestAuthorization(auth,
			map[string]string{"realm": "r", "nonce": "n", "opaque": "abc123"}, "GET", "/")
		require.NoError(t, err)
		assert.Contains(t, value, `opaque="abc123"`)
	})
}

func TestTheClientNonceIsFreshEverySend(t *testing.T) {
	first, err := freshCnonce()
	require.NoError(t, err)
	second, err := freshCnonce()
	require.NoError(t, err)

	assert.NotEqual(t, first, second)
	// 16 random bytes, hex-encoded. `nc` can stay at 1 precisely because this is never reused.
	assert.Len(t, first, 32)
	_, err = hex.DecodeString(first)
	assert.NoError(t, err)
}

func TestDigestURIIsTheRequestTarget(t *testing.T) {
	assert.Equal(t, "/dir/index.html", digestURI("https://host.test/dir/index.html"))
	assert.Equal(t, "/dir?a=1&b=2", digestURI("https://host.test/dir?a=1&b=2"))
	assert.Equal(t, "/", digestURI("https://host.test"))
}

// ---- response decoding -------------------------------------------------------------------------

func TestDeclaredTextWithAnInvalidByteIsStillText(t *testing.T) {
	one := loadCase(t, "decode-invalid-byte-still-text")

	var input struct {
		Prefix      string `json:"bytes_utf8_prefix"`
		TrailingHex string `json:"extra_trailing_byte_hex"`
		ContentType string `json:"content_type"`
	}
	var expected struct {
		StartsWith string  `json:"body_text_starts_with"`
		EndsWith   string  `json:"body_text_ends_with"`
		BodyBase64 *string `json:"body_base64"`
	}
	decodeInto(t, one.Input, &input)
	decodeInto(t, one.Expected, &expected)

	trailing, err := hex.DecodeString(input.TrailingHex)
	require.NoError(t, err)

	text, encoded := decodeBody(append([]byte(input.Prefix), trailing...), input.ContentType)

	// The whole page still renders, with U+FFFD standing in for the one bad byte, instead of
	// falling back to base64 and showing the reader nothing.
	assert.Contains(t, text, expected.StartsWith)
	assert.Equal(t, expected.EndsWith, text[len(text)-len(expected.EndsWith):])
	assert.Nil(t, encoded)
	assert.Nil(t, expected.BodyBase64)
}

func TestBinaryDecodesToBase64(t *testing.T) {
	one := loadCase(t, "decode-binary-to-base64")

	var input struct {
		BytesHex string `json:"bytes_hex"`
		Cases    []struct {
			ContentType *string `json:"content_type"`
			Reason      string  `json:"reason"`
		} `json:"cases"`
	}
	var expected struct {
		BodyText   string `json:"body_text"`
		BodyBase64 string `json:"body_base64"`
	}
	decodeInto(t, one.Input, &input)
	decodeInto(t, one.Expected, &expected)

	raw, err := hex.DecodeString(input.BytesHex)
	require.NoError(t, err)

	for _, variant := range input.Cases {
		t.Run(variant.Reason, func(t *testing.T) {
			contentType := ""
			if variant.ContentType != nil {
				contentType = *variant.ContentType
			}

			text, encoded := decodeBody(raw, contentType)
			assert.Equal(t, expected.BodyText, text)
			require.NotNil(t, encoded)
			assert.Equal(t, expected.BodyBase64, *encoded)
		})
	}
}

func TestUndeclaredTextAndVendorJSON(t *testing.T) {
	one := loadCase(t, "decode-undeclared-text-and-vendor-json")

	var input struct {
		DecodeBody struct {
			BytesUTF8   string  `json:"bytes_utf8"`
			ContentType *string `json:"content_type"`
		} `json:"decode_body"`
		Checks []string `json:"is_textual_type_checks"`
	}
	var expected struct {
		DecodeBody struct {
			BodyText   string  `json:"body_text"`
			BodyBase64 *string `json:"body_base64"`
		} `json:"decode_body"`
		Results map[string]bool `json:"is_textual_type_results"`
	}
	decodeInto(t, one.Input, &input)
	decodeInto(t, one.Expected, &expected)

	text, encoded := decodeBody([]byte(input.DecodeBody.BytesUTF8), "")
	assert.Equal(t, expected.DecodeBody.BodyText, text)
	assert.Nil(t, encoded)

	for _, media := range input.Checks {
		t.Run(media, func(t *testing.T) {
			// The `+json`/`+xml` suffix rules are what keep this from being an exhaustive list of
			// vendor types that would be out of date the day after it was written.
			assert.Equal(t, expected.Results[media], isTextualType(media))
		})
	}
}

func TestLatin1IsTranscodedRatherThanDecoded(t *testing.T) {
	one := loadCase(t, "decode-latin1-transcode")

	var input struct {
		BytesHex    string `json:"bytes_hex"`
		ContentType string `json:"content_type"`
	}
	var expected struct {
		BodyText   string  `json:"body_text"`
		BodyBase64 *string `json:"body_base64"`
	}
	decodeInto(t, one.Input, &input)
	decodeInto(t, one.Expected, &expected)

	raw, err := hex.DecodeString(input.BytesHex)
	require.NoError(t, err)

	text, encoded := decodeBody(raw, input.ContentType)

	// A correct transcode and not a guess: Latin-1's code points *are* Unicode 0–255, so `0xF1`
	// is `ñ` by definition rather than by inference.
	assert.Equal(t, expected.BodyText, text)
	assert.Nil(t, encoded)
	assert.Nil(t, expected.BodyBase64)
}

// ---- Set-Cookie --------------------------------------------------------------------------------

func TestSetCookieDefaultsToTheRequestHostAndRootPath(t *testing.T) {
	one := loadCase(t, "cookie-defaults-host-and-root-path")

	var input struct {
		URL string `json:"url"`
		Raw string `json:"raw_set_cookie"`
	}
	var expected ParsedCookie
	decodeInto(t, one.Input, &input)
	decodeInto(t, one.Expected, &expected)

	cookie, ok := parseSetCookie(input.Raw, input.URL, time.Now())
	require.True(t, ok)

	assert.Equal(t, expected.Name, cookie.Name)
	assert.Equal(t, expected.Value, cookie.Value)
	assert.Equal(t, expected.Domain, cookie.Domain)
	assert.Equal(t, expected.Path, cookie.Path)
	assert.Equal(t, expected.Secure, cookie.Secure)
	assert.Equal(t, expected.HTTPOnly, cookie.HTTPOnly)
	assert.Nil(t, cookie.Expires)
}

// `BUG-API-c`, preserved and pinned: the default path is a flat `/` rather than RFC 6265 §5.1.4's
// algorithm. The published fixture only exercises a root-path request and so cannot tell the two
// apart — this is the case that can.
func TestTheDefaultCookiePathIsAlwaysRoot(t *testing.T) {
	cookie, ok := parseSetCookie("sid=abc", "https://api.test/v1/users/42", time.Now())
	require.True(t, ok)

	assert.Equal(t, "/", cookie.Path,
		"RFC 6265 says /v1/users; this implementation says / — BUG-API-c, ported as-is")
}

func TestSetCookieReadsDomainPathAndExpiry(t *testing.T) {
	one := loadCase(t, "cookie-domain-path-expiry")

	var input struct {
		URL string `json:"url"`
		Raw string `json:"raw_set_cookie"`
	}
	var expected struct {
		Domain  string `json:"domain"`
		Path    string `json:"path"`
		Expires string `json:"expires"`
	}
	decodeInto(t, one.Input, &input)
	decodeInto(t, one.Expected, &expected)

	cookie, ok := parseSetCookie(input.Raw, input.URL, time.Now())
	require.True(t, ok)

	// The leading dot is the pre-RFC 6265 spelling of "and every subdomain"; modern semantics are
	// the same without it, so it is stripped.
	assert.Equal(t, expected.Domain, cookie.Domain)
	assert.Equal(t, expected.Path, cookie.Path)

	require.NotNil(t, cookie.Expires)
	wanted, err := time.Parse(time.RFC3339, expected.Expires)
	require.NoError(t, err)
	got, err := time.Parse(time.RFC3339, *cookie.Expires)
	require.NoError(t, err)
	assert.True(t, wanted.Equal(got), "%s vs %s", expected.Expires, *cookie.Expires)
}

func TestMaxAgeWinsOverExpires(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

	cookie, ok := parseSetCookie(
		"sid=abc; Expires=Wed, 21 Oct 2015 07:28:00 GMT; Max-Age=60", "https://api.test/", now)
	require.True(t, ok)
	require.NotNil(t, cookie.Expires)

	// RFC 6265's own order. Taking Expires would have dated this cookie to 2015 — already gone.
	assert.Equal(t, now.Add(time.Minute).Format(time.RFC3339), *cookie.Expires)
}

func TestAnUnparseableExpiryReadsAsASessionCookie(t *testing.T) {
	cookie, ok := parseSetCookie("sid=abc; Expires=nunca", "https://api.test/", time.Now())
	require.True(t, ok)

	// Giving up rather than guessing: an invented expiry is a claim the server never made, and the
	// conservative end of the mistake is the one that expires sooner.
	assert.Nil(t, cookie.Expires)
}

func TestAMalformedSetCookieIsDroppedAndNoOther(t *testing.T) {
	cookies := parseSetCookies([]string{
		"sin-igual",
		"=sin-nombre",
		"sid=abc; Path=/app",
	}, "https://api.test/", time.Now())

	require.Len(t, cookies, 1)
	assert.Equal(t, "sid", cookies[0].Name)
}

func lower(value string) string {
	out := make([]byte, len(value))
	for i := 0; i < len(value); i++ {
		b := value[i]
		if b >= 'A' && b <= 'Z' {
			b += 'a' - 'A'
		}
		out[i] = b
	}
	return string(out)
}
