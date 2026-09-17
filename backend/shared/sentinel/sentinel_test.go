package sentinel_test

import (
	"strings"
	"testing"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/sentinel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The literals are repeated here on purpose rather than referenced. A test that compares a
// constant to itself proves nothing; these strings were read out of the renderer's own source
// (frontend/src), which is the side that does the matching.
func TestPrefixesAreByteExact(t *testing.T) {
	for name, tc := range map[string]struct{ got, want string }{
		"checkout conflict":     {sentinel.CheckoutConflict, "CHECKOUT_CONFLICT: "},
		"credential refused":    {sentinel.CredentialRefused, "CREDENTIAL_REFUSED: "},
		"db connection refused": {sentinel.DBConnectionRefused, "DB_CONNECTION_REFUSED: "},
		"self approval":         {sentinel.SelfApproval, "SELF_APPROVAL: "},
		"stale review":          {sentinel.StaleReview, "STALE_REVIEW: "},
		"nothing to analyze":    {sentinel.NothingToAnalyze, "NOTHING_TO_ANALYZE: "},
		"ticket not linked":     {sentinel.TicketNotLinked, "TICKET_NOT_LINKED: "},
		"ticket sync failed":    {sentinel.TicketSyncFailed, "TICKET_SYNC_FAILED: "},
		"quota exceeded":        {sentinel.QuotaExceeded, "QUOTA_EXCEEDED::"},
		"auth expired":          {sentinel.AuthExpired, "AUTH_EXPIRED::"},
		"run cancelled":         {sentinel.RunCancelled, "RUN_CANCELLED::"},
		"run timed out":         {sentinel.RunTimedOut, "RUN_TIMED_OUT::"},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.got)
		})
	}
}

// The trailing space is the part most likely to be "tidied" by a well-meaning editor or a
// formatter, and losing it is silent: the renderer's startsWith simply stops matching and the raw
// sentinel is rendered on screen as a red banner.
func TestStartsWithFamilyKeepsItsTrailingSpace(t *testing.T) {
	for _, s := range []string{
		sentinel.CheckoutConflict,
		sentinel.CredentialRefused,
		sentinel.DBConnectionRefused,
		sentinel.SelfApproval,
		sentinel.StaleReview,
		sentinel.NothingToAnalyze,
		sentinel.TicketNotLinked,
		sentinel.TicketSyncFailed,
	} {
		require.True(t, strings.HasSuffix(s, ": "), "%q must end with a colon and a space", s)
		require.NotContains(t, strings.TrimSuffix(s, ": "), " ", "%q must not contain other spaces", s)
	}
}

// The :: markers are matched with `includes`, so a trailing space would not break the match — it
// would leak into the message the banner shows instead.
func TestMarkerFamilyHasNoTrailingSpace(t *testing.T) {
	for _, s := range []string{
		sentinel.QuotaExceeded,
		sentinel.AuthExpired,
		sentinel.RunCancelled,
		sentinel.RunTimedOut,
	} {
		require.True(t, strings.HasSuffix(s, "::"), "%q must end with a double colon", s)
		require.NotContains(t, s, " ", "%q must not contain a space", s)
	}
}
