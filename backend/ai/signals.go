package ai

import (
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/sentinel"
)

// Recognising two states a CLI reports in prose (AI-014, AI-056).
//
// Neither is a status code, an exit value or a structured field. Each of these CLIs authenticates
// outside CodeFlow — Claude's own OAuth session, a ChatGPT login, opencode's configured provider,
// a Google account — and none of those credentials is this app's to renew. All it can do is
// recognise the sentence the CLI printed and hand the renderer a marker it knows how to present.

// quotaPhrases are matched, lower-cased, against the engine's output. VERBATIM, all eleven.
//
// Each was taken from a real failing run rather than invented, which is why the list reads
// unevenly: `resets at` and `try again in` are how one CLI phrases it, `insufficient balance` how
// another does.
var quotaPhrases = []string{
	"usage limit",
	"rate limit",
	"quota exceeded",
	"resets at",
	"try again in",
	"limit reached",
	"insufficient balance",
	"insufficient credit",
	"out of credit",
	"payment required",
	"billing",
}

// authPhrases are matched the same way. VERBATIM, all seven.
//
// agy has none of its own yet and rides on whatever wording it shares with the other three.
var authPhrases = []string{
	"failed to authenticate",
	"oauth session expired",
	"session expired",
	"not logged in",
	"auth required",
	"authentication failed",
	"unauthorized",
}

// QuotaSignal reports whether text reads like an exhausted quota.
//
// **BUG-AI-b, preserved on purpose**: the match runs over the engine's *whole* output, so a
// successful review whose content discusses rate limiting is classified as a quota failure and
// discarded. Observed live on 2026-08-01, when a finding explaining backoff cost a complete and
// correct review. It is listed as an open bug rather than fixed here because the renderer and the
// review pipeline both branch on the marker, and narrowing the match is a named change with its
// own testing — not something to slip into a port.
func QuotaSignal(text string) bool { return containsAny(text, quotaPhrases) }

// AuthSignal reports whether text reads like a lost login.
//
// **Consulted only on a failure path**, and that placement is the whole difference from
// QuotaSignal. The dictionary cannot tell a lost login from a review discussing one, and "returns
// 401 Unauthorized" is an ordinary finding — checking it over successful output would discard
// correct reviews exactly the way BUG-AI-b does.
func AuthSignal(text string) bool { return containsAny(text, authPhrases) }

func containsAny(text string, phrases []string) bool {
	lowered := strings.ToLower(text)
	for _, phrase := range phrases {
		if strings.Contains(lowered, phrase) {
			return true
		}
	}
	return false
}

// MarkQuota prefixes a message with the quota marker, once.
//
// Never double-prefixed: an engine that already tagged its own result keeps the one marker, and
// the renderer matches with startsWith at position 0.
func MarkQuota(message string) string {
	if strings.HasPrefix(message, sentinel.QuotaExceeded) {
		return message
	}
	return sentinel.QuotaExceeded + message
}

// MarkAuth prefixes a message with the auth marker, once.
//
// It **replaces** the "{binary} exited with an error (…)" wrapper rather than nesting inside it:
// the CLI's own sentence is the reason, and the exit status adds nothing a user can act on.
func MarkAuth(message string) string {
	if strings.HasPrefix(message, sentinel.AuthExpired) {
		return message
	}
	return sentinel.AuthExpired + message
}

// Classify tags a failed run's message with whichever marker applies.
//
// Quota first, then auth, and only ever one — the order is what AI-056 specifies and it matters:
// an exhausted quota often prints a 401 alongside, and telling the user to log in again when they
// simply ran out of credit sends them somewhere that cannot help.
func Classify(message string, succeeded bool) string {
	if QuotaSignal(message) {
		return MarkQuota(message)
	}
	if !succeeded && AuthSignal(message) {
		return MarkAuth(message)
	}
	return message
}
