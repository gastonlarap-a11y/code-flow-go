// Package sentinel holds the error-string prefixes the renderer matches on.
//
// These are not messages. They are a typed-error channel that happens to travel as text, because
// the bridge carries an error as its `Error()` string and nothing else. The renderer branches on
// them to tell a recoverable state from a failure: a blocked checkout offers to stash instead of
// showing a red banner, a refused credential offers to reconnect, an empty working tree renders an
// empty state rather than an error.
//
// Every constant here is VERBATIM (docs/business-rules/13-cross-language-contracts.md). The
// trailing space is part of the contract — the renderer's own test fixtures contain
// "CREDENTIAL_REFUSED:no space here" precisely to pin that a missing space must NOT match.
// Rewording one, or dropping its space, turns a recoverable state back into an unhandled failure.
//
// Two rules govern their use, both inherited from the C# code:
//
//   - They are applied at the command boundary, never at the throw site (XLANG-012). Inside the
//     process, callers branch on typed errors with errors.As; the handler translates the typed
//     error into a sentinel-bearing one on its way out.
//   - A sentinel-bearing error is never wrapped with fmt.Errorf("...: %w", err) before it leaves
//     the handler. The renderer matches the ": " family with startsWith, so anything prepended
//     moves the marker off position 0 and the match fails silently.
package sentinel

// The startsWith family. Each is matched at position 0 of the error message by the renderer, so
// the whole "never wrap" rule above exists to protect these eight strings.
const (
	// CheckoutConflict prefixes a checkout blocked by local changes (XLANG-002).
	// renderer: src/state/repoStore.ts
	CheckoutConflict = "CHECKOUT_CONFLICT: "

	// CredentialRefused prefixes a stored credential the remote rejected (XLANG-012).
	//
	// gosec G101 flags this as a hardcoded credential on the strength of the word. It is the
	// opposite: an error prefix that says a credential was refused, and the one thing this file
	// guarantees is that no value ever follows it.
	CredentialRefused = "CREDENTIAL_REFUSED: " //nolint:gosec

	// DBConnectionRefused prefixes a database that refused the connection (XLANG-018). The
	// remainder is the driver's own sentence and must never contain the connection string, which
	// holds the password.
	DBConnectionRefused = "DB_CONNECTION_REFUSED: "

	// SelfApproval prefixes GitHub refusing an approval of one's own pull request (XLANG-013).
	SelfApproval = "SELF_APPROVAL: "

	// StaleReview prefixes a review whose head moved before it could be posted (XLANG-014).
	StaleReview = "STALE_REVIEW: "

	// NothingToAnalyze prefixes a refusal to analyse an empty working tree (XLANG-015).
	NothingToAnalyze = "NOTHING_TO_ANALYZE: "

	// TicketNotLinked and TicketSyncFailed prefix the two ticket-review refusals (XLANG-017).
	// The renderer treats them differently on purpose: it renders a calm empty state for
	// TicketNotLinked and a real error for TicketSyncFailed, because hiding a sync failure behind
	// an empty state is how a review silently stops running.
	TicketNotLinked  = "TICKET_NOT_LINKED: "
	TicketSyncFailed = "TICKET_SYNC_FAILED: "
)

// The AI run markers (XLANG-003). The renderer matches these with `includes`, not `startsWith`,
// so their position in the string is not load-bearing — but their bytes are.
const (
	// QuotaExceeded tags a limit or billing refusal. AiOperations is careful not to double-prefix
	// an error that already carries it.
	QuotaExceeded = "QUOTA_EXCEEDED::"

	// AuthExpired tags a CLI engine that lost its own login. Applied inside each engine's
	// interpret, after the quota test and only on a failure path.
	AuthExpired = "AUTH_EXPIRED::"

	// RunCancelled is bare: the user stopped the run.
	RunCancelled = "RUN_CANCELLED::"

	// RunTimedOut carries the silence window in whole minutes after the marker, or nothing when
	// the deadline is under a minute. It is kept apart from RunCancelled because the two say
	// opposite things to the person reading the panel.
	RunTimedOut = "RUN_TIMED_OUT::"
)
