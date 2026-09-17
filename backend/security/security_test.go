package security_test

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/security"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/sentinel"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The key formats are VERBATIM (SEC-002). The expected strings are written out here rather than
// built from the same helpers, because a test that calls the function it is testing proves
// nothing — these were read from `10-security.md` and match what a real 2.7.1 keychain holds.
func TestKeyFormats(t *testing.T) {
	assert.Equal(t, "ado-pat:myorg", security.ADOPATKey("myorg"))
	assert.Equal(t, "github-token:github.com", security.GitHubTokenKey("github.com"))
	assert.Equal(t, "ai-api-key:openai", security.AIAPIKey("openai"))
	assert.Equal(t, "db-password:26946c6f-1234", security.DBPasswordKey("26946c6f-1234"))
	assert.Equal(t, "com.codeflow.app", security.Service)
}

// No validation, sanitisation or escaping — whatever is passed becomes part of the key verbatim,
// exactly as in 2.x. Pinned so nobody "fixes" it and orphans a credential.
func TestKeyFormatsDoNotSanitiseTheirInput(t *testing.T) {
	assert.Equal(t, "ado-pat:", security.ADOPATKey(""))
	assert.Equal(t, "github-token:", security.GitHubTokenKey(""))
	assert.Equal(t, "ado-pat:has:a:colon", security.ADOPATKey("has:a:colon"))
}

// ErrNoEntry is not a failure: a read maps it to "none" and a delete to success.
func TestNoEntryIsNotAnError(t *testing.T) {
	assert.NoError(t, security.AsCommandError(security.ErrNoEntry))
	assert.NoError(t, security.AsCommandError(nil))
	assert.NoError(t, security.AsCommandError(fmt.Errorf("wrapped: %w", security.ErrNoEntry)))
}

// A refusal carries the sentinel at position 0, and keeps the platform's own words after it —
// "User interaction is not allowed" tells the user something a generic message does not.
func TestRefusalCarriesTheSentinelAtPositionZero(t *testing.T) {
	err := security.AsCommandError(fmt.Errorf("User interaction is not allowed: %w", security.ErrRefused))

	require.Error(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), sentinel.CredentialRefused), "got %q", err.Error())
	assert.Contains(t, err.Error(), "User interaction is not allowed")
}

func TestRefusalDoesNotDoubleTheSentinel(t *testing.T) {
	err := security.AsCommandError(fmt.Errorf("%s%w", sentinel.CredentialRefused, security.ErrRefused))

	require.Error(t, err)
	assert.Equal(t, 1, strings.Count(err.Error(), "CREDENTIAL_REFUSED:"))
}

// A store failure that is not a refusal must stay itself: a locked keychain and an unsupported
// platform lead to different next steps, and neither is "reconnect your account".
func TestAnUnrelatedFailurePassesThroughUnchanged(t *testing.T) {
	err := security.AsCommandError(security.ErrUnsupported)

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "CREDENTIAL_REFUSED")
	assert.ErrorIs(t, err, security.ErrUnsupported)
}

// ---- the commands ------------------------------------------------------------------------

func registry(t *testing.T) *bridge.Service {
	t.Helper()
	r := bridge.NewRegistry()
	security.Register(r, security.Deps{Store: security.NewStore()})
	r.Seal()
	return bridge.NewService(r, nil)
}

// Nine credential commands and the pre-commit gate. The gate shares this package because both are
// the security domain, and nothing else: it never touches the keychain.
func TestTheRegisteredCommands(t *testing.T) {
	r := bridge.NewRegistry()
	security.Register(r, security.Deps{Store: security.NewStore()})

	assert.Equal(t, []string{
		"delete_ado_pat", "delete_ai_api_key", "delete_github_token",
		"has_ado_pat", "has_ai_api_key", "has_github_token",
		"scan_staged_secrets",
		"set_ado_pat", "set_ai_api_key", "set_github_token",
	}, r.Names())
}

// Nothing here returns a secret. This is the assertion that keeps it that way: a `get_*` command
// appearing in this list is how a token would reach the renderer's memory.
func TestNoCommandReturnsASecret(t *testing.T) {
	r := bridge.NewRegistry()
	security.Register(r, security.Deps{Store: security.NewStore()})

	for _, name := range r.Names() {
		assert.False(t, strings.HasPrefix(name, "get_"), "%s would hand a secret to the renderer", name)
	}
}

func TestCommandsReportAMissingParameterByName(t *testing.T) {
	svc := registry(t)

	for _, tc := range []struct{ method, missing string }{
		{"set_ado_pat", "org"},
		{"has_github_token", "host"},
		{"delete_ai_api_key", "provider"},
	} {
		_, err := svc.Invoke(t.Context(), tc.method, json.RawMessage(`{}`))
		require.Error(t, err, tc.method)
		assert.Equal(t, "missing required parameter '"+tc.missing+"'", err.Error())
	}
}

func TestSetReportsAMissingSecretByItsOwnParameterName(t *testing.T) {
	svc := registry(t)

	// The renderer's parameter names are inconsistent — pat, token, key — and are kept that way.
	for _, tc := range []struct{ method, params, missing string }{
		{"set_ado_pat", `{"org":"acme"}`, "pat"},
		{"set_github_token", `{"host":"github.com"}`, "token"},
		{"set_ai_api_key", `{"provider":"openai"}`, "key"},
	} {
		_, err := svc.Invoke(t.Context(), tc.method, json.RawMessage(tc.params))
		require.Error(t, err, tc.method)
		assert.Equal(t, "missing required parameter '"+tc.missing+"'", err.Error())
	}
}

// ---- the real keychain -------------------------------------------------------------------

// Gated, because it writes to the developer's own credential store. Unique throwaway keys per run
// so a failed test never leaves an item that a later run mistakes for a real credential — and
// never, under any circumstance, touching a key format the app actually uses.
//
//	CODEFLOW_TEST_KEYCHAIN=1 go test ./backend/security/
func TestRealCredentialStoreRoundTrip(t *testing.T) {
	if os.Getenv("CODEFLOW_TEST_KEYCHAIN") != "1" {
		t.Skip("set CODEFLOW_TEST_KEYCHAIN=1 to exercise the real OS credential store")
	}

	store := security.NewStore()
	key := "codeflow-test:" + strings.ReplaceAll(uuid.NewString(), "-", "")
	t.Cleanup(func() { _ = store.Delete(key) })

	present, err := store.Has(key)
	require.NoError(t, err)
	assert.False(t, present, "a fresh key must not exist")

	_, err = store.Get(key)
	assert.ErrorIs(t, err, security.ErrNoEntry)

	require.NoError(t, store.Set(key, "s3cret-ünïcode-✓"))

	got, err := store.Get(key)
	require.NoError(t, err)
	assert.Equal(t, "s3cret-ünïcode-✓", got, "the value must survive the round trip byte for byte")

	present, err = store.Has(key)
	require.NoError(t, err)
	assert.True(t, present)

	// Overwriting must replace rather than duplicate.
	require.NoError(t, store.Set(key, "replaced"))
	got, err = store.Get(key)
	require.NoError(t, err)
	assert.Equal(t, "replaced", got)

	require.NoError(t, store.Delete(key))
	present, err = store.Has(key)
	require.NoError(t, err)
	assert.False(t, present)

	// Deleting one that was never stored is the state the caller asked for, not an error.
	assert.NoError(t, store.Delete(key))
}

// An empty value is what a half-finished save leaves behind; Settings showing "configured" for one
// is worse than showing nothing.
func TestHasIsFalseForAnEmptySecret(t *testing.T) {
	if os.Getenv("CODEFLOW_TEST_KEYCHAIN") != "1" {
		t.Skip("set CODEFLOW_TEST_KEYCHAIN=1 to exercise the real OS credential store")
	}

	store := security.NewStore()
	key := "codeflow-test:" + strings.ReplaceAll(uuid.NewString(), "-", "")
	t.Cleanup(func() { _ = store.Delete(key) })

	require.NoError(t, store.Set(key, ""))

	present, err := store.Has(key)
	require.NoError(t, err)
	assert.False(t, present)
}
