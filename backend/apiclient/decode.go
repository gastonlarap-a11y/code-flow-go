package apiclient

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// What comes back: deciding whether a body is text, transcoding it, and reading `Set-Cookie`
// (API-021, API-023).

// bodyPreviewLimit bounds the console's "what went out" preview (API-019).
const bodyPreviewLimit = 2048

// binarySniffWindow is how far `looksBinary` reads for a NUL when nothing declared a type.
const binarySniffWindow = 4096

// advertisedEncodings is `VERBATIM`, and duplicated on purpose in `frontend/src/lib/api/send.ts`
// (`buildImplicitHeaders`), where a comment names this constant as the thing it mirrors.
//
// Reconstructed rather than read back: the HTTP client negotiates content encoding below the layer
// that can inspect the request, so the console would otherwise show a header list missing the one
// header every request carries.
const advertisedEncodings = "gzip, br, deflate"

// isTextualType decides from a declared `Content-Type` (API-021). `VERBATIM`: this exact set.
//
// The two suffix rules are the load-bearing part. `application/vnd.github+json` and
// `application/problem+json` are text, and an exhaustive list of vendor types would be out of date
// the day after it was written.
func isTextualType(contentType string) bool {
	media := strings.ToLower(strings.TrimSpace(contentType))
	if index := strings.IndexByte(media, ';'); index >= 0 {
		media = strings.TrimSpace(media[:index])
	}

	if strings.HasPrefix(media, "text/") ||
		strings.HasSuffix(media, "+json") ||
		strings.HasSuffix(media, "+xml") {
		return true
	}

	switch media {
	case "application/json", "application/xml", "application/javascript",
		"application/x-javascript", "application/ecmascript", "application/graphql",
		"application/x-www-form-urlencoded", "application/x-ndjson",
		"application/ld+json", "application/sql", "image/svg+xml":
		return true
	default:
		return false
	}
}

// looksBinary is the fallback when nothing declared a type: a NUL byte in the first 4096 bytes.
//
// Crude and right for the job — a NUL cannot appear in text, and scanning the whole body to be
// surer would cost a pass over a 50 MB response to answer a question the first page settles.
func looksBinary(body []byte) bool {
	window := body
	if len(window) > binarySniffWindow {
		window = window[:binarySniffWindow]
	}
	for _, b := range window {
		if b == 0 {
			return true
		}
	}
	return false
}

// charsetOf reads the `charset=` parameter, lower-cased.
func charsetOf(contentType string) string {
	for _, parameter := range strings.Split(contentType, ";")[1:] {
		key, value, found := strings.Cut(parameter, "=")
		if !found || !strings.EqualFold(strings.TrimSpace(key), "charset") {
			continue
		}
		return strings.ToLower(strings.Trim(strings.TrimSpace(value), `"`))
	}
	return ""
}

// isLatin1 names the charsets transcoded byte-for-byte rather than decoded as UTF-8.
func isLatin1(charset string) bool {
	switch charset {
	case "iso-8859-1", "latin1", "latin-1", "windows-1252", "cp1252":
		return true
	default:
		return false
	}
}

// decodeBody turns raw bytes into the text-or-base64 pair the renderer reads (API-021).
//
// The lossy UTF-8 decode is the decision worth naming: a `text/html; charset=utf-8` page with one
// bad byte in it still renders, with U+FFFD standing in for the byte, instead of the whole page
// falling back to base64 and showing the reader nothing.
func decodeBody(body []byte, contentType string) (text string, encoded *string) {
	binary := looksBinary(body)
	if strings.TrimSpace(contentType) != "" {
		binary = !isTextualType(contentType)
	}

	if binary {
		value := base64.StdEncoding.EncodeToString(body)
		return "", &value
	}

	if isLatin1(charsetOf(contentType)) {
		// A correct transcode, not a guess: Latin-1's code points *are* Unicode 0–255 by
		// definition, so each byte maps to exactly one rune.
		runes := make([]rune, 0, len(body))
		for _, b := range body {
			runes = append(runes, rune(b))
		}
		return string(runes), nil
	}

	return toValidUTF8(body), nil
}

// toValidUTF8 replaces each invalid byte with U+FFFD, one per bad byte.
//
// `strings.ToValidUTF8` would collapse a run of them into a single replacement; the contract here
// is one replacement per byte, which is what the fixture pins.
func toValidUTF8(body []byte) string {
	if utf8.Valid(body) {
		return string(body)
	}

	out := &strings.Builder{}
	out.Grow(len(body))

	for i := 0; i < len(body); {
		r, size := utf8.DecodeRune(body[i:])
		if r == utf8.RuneError && size <= 1 {
			out.WriteRune(utf8.RuneError)
			i++
			continue
		}
		out.Write(body[i : i+size])
		i += size
	}
	return out.String()
}

// previewText truncates a text body for the console (API-019).
//
// Cut backward to a character boundary, so the preview never ends in half a character — which on a
// Spanish body is every other truncation.
func previewText(text string) string {
	if len(text) <= bodyPreviewLimit {
		return text
	}

	cut := bodyPreviewLimit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return fmt.Sprintf("%s… (%d bytes total)", text[:cut], len(text))
}

// previewBytes previews a byte body: as text when it is valid UTF-8, and as a count when it is not.
func previewBytes(body []byte) string {
	if !utf8.Valid(body) {
		return fmt.Sprintf("<%d bytes of binary>", len(body))
	}
	return previewText(string(body))
}

// parseSetCookie reads one `Set-Cookie` value against the URL it came from (API-023).
//
// **`BUG-API-c` is preserved here**: `path` defaults to `"/"` unconditionally. RFC 6265 §5.1.4
// specifies a default-path *algorithm* derived from the request URI — drop the last `/`-segment, and
// only then fall back to `/` — so a response to `/v1/users/42` with no `Path` should default the
// cookie to `/v1/users`, not to the root. Suspected-correct behaviour is that algorithm. Ported
// as-is; not fixed.
func parseSetCookie(raw, requestURL string, now time.Time) (ParsedCookie, bool) {
	segments := strings.Split(raw, ";")
	name, value, found := strings.Cut(segments[0], "=")
	name, value = strings.TrimSpace(name), strings.TrimSpace(value)
	if !found || name == "" {
		return ParsedCookie{}, false
	}

	cookie := ParsedCookie{Name: name, Value: value, Path: "/"}
	if parsed, err := url.Parse(requestURL); err == nil {
		cookie.Domain = parsed.Hostname()
	}

	var maxAge *int64
	var expires string

	for _, segment := range segments[1:] {
		key, attribute, _ := strings.Cut(segment, "=")
		key = strings.ToLower(strings.TrimSpace(key))
		attribute = strings.TrimSpace(attribute)

		switch key {
		case "domain":
			// The leading dot is the pre-RFC 6265 spelling of "and every subdomain". Modern
			// semantics are the same without it, so it is stripped rather than kept.
			cookie.Domain = strings.TrimPrefix(attribute, ".")
		case "path":
			cookie.Path = attribute
		case "secure":
			cookie.Secure = true
		case "httponly":
			cookie.HTTPOnly = true
		case "max-age":
			if seconds, err := strconv.ParseInt(attribute, 10, 64); err == nil {
				maxAge = &seconds
			}
		case "expires":
			expires = attribute
		}
	}

	// Max-Age wins wherever both are present, which is RFC 6265's order.
	switch {
	case maxAge != nil:
		at := now.Add(time.Duration(*maxAge) * time.Second).Format(time.RFC3339)
		cookie.Expires = &at
	case expires != "":
		if at, ok := parseHTTPDate(expires); ok {
			formatted := at.Format(time.RFC3339)
			cookie.Expires = &formatted
		}
		// An unparseable date gives up rather than guessing: the cookie comes back with no expiry,
		// which reads as a session cookie — the conservative end, since it expires sooner.
	}
	return cookie, true
}

// parseHTTPDate tries the formats RFC 6265 requires a client to tolerate, in order.
func parseHTTPDate(value string) (time.Time, bool) {
	for _, layout := range []string{
		time.RFC1123,                     // Wed, 21 Oct 2015 07:28:00 GMT
		time.RFC1123Z,                    // …with a numeric zone
		"Monday, 02-Jan-06 15:04:05 MST", // the obsolete RFC 850 form
		time.ANSIC,                       // asctime
		time.RFC822, time.RFC822Z,
	} {
		if at, err := time.Parse(layout, strings.TrimSpace(value)); err == nil {
			return at.UTC(), true
		}
	}
	return time.Time{}, false
}

// parseSetCookies reads every instance the response carried, independently. One malformed value
// loses that cookie and no other.
func parseSetCookies(values []string, requestURL string, now time.Time) []ParsedCookie {
	cookies := make([]ParsedCookie, 0, len(values))
	for _, value := range values {
		if cookie, ok := parseSetCookie(value, requestURL, now); ok {
			cookies = append(cookies, cookie)
		}
	}
	return cookies
}
