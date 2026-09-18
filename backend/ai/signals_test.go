package ai_test

import (
	"testing"

	"github.com/gastonlarap-a11y/code-flow/backend/ai"
	"github.com/stretchr/testify/assert"
)

// Every phrase is matched lower-cased and as a substring, so these tests use the wording a CLI
// actually printed rather than the dictionary entry itself.

func TestTheElevenQuotaPhrases(t *testing.T) {
	for _, printed := range []string{
		"Claude usage limit reached",
		"You have hit the rate limit for this model",
		"Error: quota exceeded for organisation",
		"Your limit resets at 3pm",
		"Please try again in 4 hours",
		"Monthly limit reached",
		"insufficient balance on your account",
		"insufficient credit",
		"You are out of credit",
		"402 Payment Required",
		"A billing issue prevented this request",
	} {
		t.Run(printed, func(t *testing.T) {
			assert.True(t, ai.QuotaSignal(printed))
		})
	}
}

func TestTheSevenAuthPhrases(t *testing.T) {
	for _, printed := range []string{
		"Failed to authenticate with the provider",
		"Your OAuth session expired",
		"session expired, please log in",
		"You are not logged in",
		"auth required",
		"Authentication failed (401)",
		"401 Unauthorized",
	} {
		t.Run(printed, func(t *testing.T) {
			assert.True(t, ai.AuthSignal(printed))
		})
	}
}

func TestOrdinaryOutputIsNeitherSignal(t *testing.T) {
	const review = "The function returns early and the caller ignores the error."

	assert.False(t, ai.QuotaSignal(review))
	assert.False(t, ai.AuthSignal(review))
}

// BUG-AI-b, stated as a test so nobody closes it by accident. The match runs over the whole
// output, so a correct review that happens to discuss rate limiting is classified as a quota
// failure and discarded. Observed live on 2026-08-01; preserved, not fixed.
func TestAReviewDiscussingRateLimitingIsStillMisclassified(t *testing.T) {
	const finding = "Add exponential backoff here: the API applies rate limiting per client."

	assert.True(t, ai.QuotaSignal(finding),
		"BUG-AI-b — narrowing this match is a named change with its own testing, not a port detail")
}

// The placement is the whole difference between the two. The dictionary cannot tell a lost login
// from a review discussing one, so auth is consulted only where the run already failed.
func TestAuthIsOnlyConsultedOnAFailurePath(t *testing.T) {
	const finding = "This endpoint returns 401 Unauthorized when the token is stale."

	assert.Equal(t, finding, ai.Classify(finding, true),
		"a successful run's content is never auth-classified, or correct reviews are discarded")
	assert.Equal(t, "AUTH_EXPIRED::"+finding, ai.Classify(finding, false))
}

// An exhausted quota often prints a 401 alongside, and telling the user to log in again when they
// simply ran out of credit sends them somewhere that cannot help.
func TestQuotaIsTestedBeforeAuth(t *testing.T) {
	const both = "401 Unauthorized: usage limit reached for this account"

	assert.Equal(t, "QUOTA_EXCEEDED::"+both, ai.Classify(both, false))
}

// The renderer matches with startsWith at position 0, so a second marker would hide the first.
func TestAMarkerIsNeverAppliedTwice(t *testing.T) {
	already := ai.MarkQuota("out of credit")

	assert.Equal(t, already, ai.MarkQuota(already))
	assert.Equal(t, "QUOTA_EXCEEDED::out of credit", already)

	auth := ai.MarkAuth("session expired")
	assert.Equal(t, auth, ai.MarkAuth(auth))
	assert.Equal(t, "AUTH_EXPIRED::session expired", auth)
}

func TestAnUnremarkableFailureIsLeftAlone(t *testing.T) {
	const detail = "claude exited with an error (exit status 1): no such file"

	assert.Equal(t, detail, ai.Classify(detail, false))
}
