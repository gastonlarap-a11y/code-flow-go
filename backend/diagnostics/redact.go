package diagnostics

import "regexp"

// The four redaction patterns, transcribed from the 2.x pair that had to agree by hand:
// Diagnostics/ErrorLog.cs (Redact) and shell/src/shell-log.ts (redact). They are applied in this
// order, and the order matters: AUTH_HEADER must run before BARE_BEARER, or the header form
// "Authorization: Bearer abc…" would be half-redacted into "Authorization: Bearer ***" and keep
// the header name's own value shape.
//
// .NET's Regex and JavaScript's are both backtracking engines; Go's is RE2. All four translate
// without change — none uses lookaround or a backreference — and RE2 buys linear time on the
// hostile input these run against (an error body from a remote host).
//
// Deliberately blunt, and copied as such: it replaces rather than detects. A false positive costs
// a line of diagnostics, a false negative costs a credential.
var (
	// A URL carrying user:password, as git prints it back on a failed exchange.
	credentialInURL = regexp.MustCompile(`(\w+)://[^/\s:@]+:[^/\s@]+@`)

	// An auth header echoed inside an error body, scheme and value together.
	//
	// The value stops at the first quote, comma or brace rather than the first space: these arrive
	// embedded in JSON, and a greedy run of non-space would swallow the syntax around it. The
	// header name survives — knowing a request carried an Authorization at all is diagnostic.
	authHeader = regexp.MustCompile(`(?i)\b(authorization|x-api-key|private-token|api-key)\s*:\s*(?:bearer\s+|token\s+|basic\s+)?[^\s"',}\]]+`)

	// A `Bearer …` with no header name in front of it.
	bareBearer = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._-]{8,}`)

	// A token recognisable on its own, by the prefixes the hosts publish.
	tokenLiteral = regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{16,}|github_pat_[A-Za-z0-9_]{20,}|xox[abposr]-[A-Za-z0-9-]{10,}|sk-[A-Za-z0-9-]{20,})`)
)

// Redact blanks out anything in a message that looks like a credential.
//
// Every line written by any of the three logs goes through it. The lines carry a subprocess's
// stderr and a remote host's error bodies, and a failed fetch prints the remote URL it tried —
// which in a great many repositories carries an embedded token. These files exist to be sent to
// somebody else, which is exactly what makes that unacceptable.
func Redact(message string) string {
	if message == "" {
		return message
	}

	message = credentialInURL.ReplaceAllString(message, "${1}://***:***@")
	message = authHeader.ReplaceAllString(message, "${1}: ***")
	message = bareBearer.ReplaceAllString(message, "Bearer ***")
	return tokenLiteral.ReplaceAllString(message, "***")
}
