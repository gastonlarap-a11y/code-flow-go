package apiclient_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/apiclient"
	"github.com/gastonlarap-a11y/code-flow/backend/storage"
)

// seedGlobals writes the one `is_global` row a workspace has after a launch, the way the migration
// does: `sort_order = -1`, which is what makes `ORDER BY sort_order` put it first.
func seedGlobals(t *testing.T, db *storage.DB, workspaceID string) string {
	t.Helper()
	id := "glob-" + workspaceID

	require.NoError(t, db.Write(t.Context(), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO api_environments (id, workspace_id, name, variables, is_global, sort_order,
			                              created_at)
			VALUES (?, ?, 'Globals', '[]', 1, -1, 'now')`, id, workspaceID)
		return err
	}))
	return id
}

// ---- environments ----------------------------------------------------------------------------------

func TestGlobalsComesFirstWhateverElseExists(t *testing.T) {
	store, db := newStore(t)
	seedGlobals(t, db, "w1")

	_, err := store.CreateEnvironment(t.Context(), "w1", "Producción")
	require.NoError(t, err)
	_, err = store.CreateEnvironment(t.Context(), "w1", "Staging")
	require.NoError(t, err)

	environments, err := store.ListEnvironments(t.Context(), "w1")
	require.NoError(t, err)
	require.Len(t, environments, 3)

	// `sort_order = -1` on the seeded row is what makes "always in scope" visible rather than
	// merely true.
	assert.True(t, environments[0].IsGlobal)
	assert.Equal(t, "Globals", environments[0].Name)
	assert.Equal(t, "Producción", environments[1].Name)
}

func TestDeletingGlobalsIsANoOpRatherThanAnError(t *testing.T) {
	store, db := newStore(t)
	globals := seedGlobals(t, db, "w1")

	// A no-op rather than a refusal: the renderer hides the button there, so reaching this is a
	// state that should not be — and an error would blame a user who cannot have meant it.
	require.NoError(t, store.DeleteEnvironment(t.Context(), globals))

	environments, err := store.ListEnvironments(t.Context(), "w1")
	require.NoError(t, err)
	require.Len(t, environments, 1)
	assert.True(t, environments[0].IsGlobal)
}

// Duplicating Globals is allowed; the copy is never a second Globals, which is the one thing that
// would stop the seeding from ever running again.
func TestACopyOfGlobalsIsAnOrdinaryEnvironment(t *testing.T) {
	store, db := newStore(t)
	globals := seedGlobals(t, db, "w1")

	copied, err := store.DuplicateEnvironment(t.Context(), globals)
	require.NoError(t, err)

	assert.Equal(t, "Globals copy", copied.Name)
	assert.False(t, copied.IsGlobal)
	assert.Equal(t, int64(0), copied.SortOrder, "and it lands after the one at -1")

	environments, err := store.ListEnvironments(t.Context(), "w1")
	require.NoError(t, err)

	globalCount := 0
	for _, environment := range environments {
		if environment.IsGlobal {
			globalCount++
		}
	}
	assert.Equal(t, 1, globalCount, "exactly one Globals row, always")
}

func TestUpdateEnvironmentCannotChangeTheGlobalFlag(t *testing.T) {
	store, db := newStore(t)
	globals := seedGlobals(t, db, "w1")

	ordinary, err := store.CreateEnvironment(t.Context(), "w1", "Producción")
	require.NoError(t, err)

	// Neither direction: a second Globals would stop the seeding for ever, and clearing the only
	// one would leave the workspace with no always-in-scope variables and no way back.
	ordinary.IsGlobal = true
	ordinary.Name = "Intento"
	require.NoError(t, store.UpdateEnvironment(t.Context(), ordinary))

	environments, err := store.ListEnvironments(t.Context(), "w1")
	require.NoError(t, err)

	byID := map[string]apiclient.Environment{}
	for _, environment := range environments {
		byID[environment.ID] = environment
	}
	assert.False(t, byID[ordinary.ID].IsGlobal)
	assert.Equal(t, "Intento", byID[ordinary.ID].Name, "the editable columns did change")
	assert.True(t, byID[globals].IsGlobal)
}

func TestEnvironmentsAreScopedToTheirWorkspace(t *testing.T) {
	store, _ := newStore(t)

	_, err := store.CreateEnvironment(t.Context(), "w1", "Mía")
	require.NoError(t, err)
	_, err = store.CreateEnvironment(t.Context(), "w2", "Ajena")
	require.NoError(t, err)

	mine, err := store.ListEnvironments(t.Context(), "w1")
	require.NoError(t, err)
	require.Len(t, mine, 1)
	assert.Equal(t, "Mía", mine[0].Name)
}

// ---- history (STORE-016) -----------------------------------------------------------------------

func historyEntry(id, workspaceID, createdAt string) apiclient.HistoryEntry {
	status := int64(200)
	return apiclient.HistoryEntry{
		ID: id, WorkspaceID: workspaceID, Name: "GET /x", Protocol: "http",
		Method: "GET", URL: "https://api.test/x", Status: &status,
		Snapshot: `{"request":{},"response":null}`, CreatedAt: createdAt,
	}
}

func TestAddHistoryIsIdempotentByID(t *testing.T) {
	store, _ := newStore(t)
	entry := historyEntry("h1", "w1", "2026-09-18T10:00:00Z")

	require.NoError(t, store.AddHistory(t.Context(), entry))
	// A replayed or re-imported entry must not become a second row.
	entry.Name = "otro nombre"
	require.NoError(t, store.AddHistory(t.Context(), entry))

	listed, err := store.ListHistory(t.Context(), "w1", 100)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, "GET /x", listed[0].Name, "the first write wins")
}

func TestAddHistoryKeepsACallerSuppliedInstant(t *testing.T) {
	store, _ := newStore(t)

	// A replayed entry keeps the instant the request actually ran, rather than being restamped to
	// the moment it was replayed.
	require.NoError(t, store.AddHistory(t.Context(),
		historyEntry("h1", "w1", "2020-01-01T00:00:00Z")))

	listed, err := store.ListHistory(t.Context(), "w1", 100)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, "2020-01-01T00:00:00Z", listed[0].CreatedAt)

	// And an entry with none is stamped now.
	blank := historyEntry("h2", "w1", "")
	require.NoError(t, store.AddHistory(t.Context(), blank))

	listed, err = store.ListHistory(t.Context(), "w1", 100)
	require.NoError(t, err)
	require.Len(t, listed, 2)
	assert.Equal(t, clock.Now(), listed[0].CreatedAt)
}

// The cap is the storage bound, and it is per workspace: heavy traffic in one never evicts
// another's history.
//
// The 2004 older rows are seeded in one transaction rather than through 2004 calls. The rule under
// test is what **one** insert does — trim this workspace back to the cap — and driving the store
// two thousand times to reach it would spend a minute and a half proving the same thing.
func TestTheHistoryCapIsPerWorkspace(t *testing.T) {
	store, db := newStore(t)

	require.NoError(t, store.AddHistory(t.Context(),
		historyEntry("quiet", "w2", "2020-01-01T00:00:00Z")))

	require.NoError(t, db.Write(t.Context(), func(ctx context.Context, tx *sql.Tx) error {
		for i := range 2004 {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO api_history (id, workspace_id, name, protocol, method, url, snapshot,
				                         created_at)
				VALUES (?, 'w1', 'GET /x', 'http', 'GET', 'https://api.test/x', '{}', ?)`,
				fmt.Sprintf("h%04d", i),
				fmt.Sprintf("2026-09-18T%02d:%02d:%02dZ", i/3600, (i/60)%60, i%60)); err != nil {
				return err
			}
		}
		return nil
	}))

	// One insert past the cap, through the store, is what has to trim.
	require.NoError(t, store.AddHistory(t.Context(),
		historyEntry("newest", "w1", "2026-09-19T00:00:00Z")))

	busy, err := store.ListHistory(t.Context(), "w1", 5000)
	require.NoError(t, err)
	assert.Len(t, busy, 2000, "trimmed to the hard cap by the insert itself")
	assert.Equal(t, "newest", busy[0].ID, "newest first, and the newest survived")

	quiet, err := store.ListHistory(t.Context(), "w2", 100)
	require.NoError(t, err)
	assert.Len(t, quiet, 1, "the other workspace's history was never touched")
}

func TestListHistoryLimitIsForDisplayOnly(t *testing.T) {
	store, _ := newStore(t)
	for i := range 10 {
		require.NoError(t, store.AddHistory(t.Context(), historyEntry(
			fmt.Sprintf("h%d", i), "w1", fmt.Sprintf("2026-09-18T10:00:%02dZ", i))))
	}

	listed, err := store.ListHistory(t.Context(), "w1", 3)
	require.NoError(t, err)
	require.Len(t, listed, 3)
	assert.Equal(t, "h9", listed[0].ID, "newest first")

	// The rows are still there — the limit decided what was shown, not what is kept.
	all, err := store.ListHistory(t.Context(), "w1", 100)
	require.NoError(t, err)
	assert.Len(t, all, 10)
}

func TestClearHistoryIsScopedToOneWorkspace(t *testing.T) {
	store, _ := newStore(t)
	require.NoError(t, store.AddHistory(t.Context(), historyEntry("a", "w1", "")))
	require.NoError(t, store.AddHistory(t.Context(), historyEntry("b", "w2", "")))

	require.NoError(t, store.ClearHistory(t.Context(), "w1"))

	mine, err := store.ListHistory(t.Context(), "w1", 100)
	require.NoError(t, err)
	assert.Empty(t, mine)

	theirs, err := store.ListHistory(t.Context(), "w2", 100)
	require.NoError(t, err)
	assert.Len(t, theirs, 1)
}

// ---- cookies (STORE-020) -----------------------------------------------------------------------

func cookie(workspaceID, domain, path, name, value string) apiclient.Cookie {
	return apiclient.Cookie{
		WorkspaceID: workspaceID, Domain: domain, Path: path, Name: name, Value: value,
	}
}

// Keyed on the wire identity, not the row id — which is how `Set-Cookie` identifies a cookie, and
// what stops a second send from accumulating a duplicate.
func TestUpsertIsKeyedOnTheWireIdentity(t *testing.T) {
	store, _ := newStore(t)

	require.NoError(t, store.UpsertCookie(t.Context(),
		cookie("w1", "api.test", "/", "session", "primero")))
	require.NoError(t, store.UpsertCookie(t.Context(),
		cookie("w1", "api.test", "/", "session", "segundo")))

	jar, err := store.ListCookies(t.Context(), "w1")
	require.NoError(t, err)
	require.Len(t, jar, 1)
	assert.Equal(t, "segundo", jar[0].Value)
}

func TestTheSameCookieInTwoWorkspacesIsTwoRows(t *testing.T) {
	store, _ := newStore(t)

	require.NoError(t, store.UpsertCookie(t.Context(),
		cookie("w1", "api.test", "/", "session", "mío")))
	require.NoError(t, store.UpsertCookie(t.Context(),
		cookie("w2", "api.test", "/", "session", "ajeno")))

	mine, err := store.ListCookies(t.Context(), "w1")
	require.NoError(t, err)
	require.Len(t, mine, 1)
	assert.Equal(t, "mío", mine[0].Value, "a jar belongs to a workspace")
}

func TestTheFourPartsOfTheKeyAreAllPartOfIt(t *testing.T) {
	store, _ := newStore(t)

	// Same name, different path and domain: three cookies, not one.
	require.NoError(t, store.UpsertCookie(t.Context(), cookie("w1", "api.test", "/", "session", "a")))
	require.NoError(t, store.UpsertCookie(t.Context(), cookie("w1", "api.test", "/admin", "session", "b")))
	require.NoError(t, store.UpsertCookie(t.Context(), cookie("w1", "otro.test", "/", "session", "c")))

	jar, err := store.ListCookies(t.Context(), "w1")
	require.NoError(t, err)
	assert.Len(t, jar, 3)
}

func TestAPathlessCookieDefaultsToRoot(t *testing.T) {
	store, _ := newStore(t)
	require.NoError(t, store.UpsertCookie(t.Context(), cookie("w1", "api.test", "", "session", "x")))

	jar, err := store.ListCookies(t.Context(), "w1")
	require.NoError(t, err)
	require.Len(t, jar, 1)
	assert.Equal(t, "/", jar[0].Path, "what a Set-Cookie with no Path means")
}

func TestAnExpiryAndTheFlagsSurviveTheRoundTrip(t *testing.T) {
	store, _ := newStore(t)

	expires := "2027-01-01T00:00:00Z"
	stored := cookie("w1", "api.test", "/", "session", "x")
	stored.Secure, stored.HTTPOnly, stored.Expires = true, true, &expires
	require.NoError(t, store.UpsertCookie(t.Context(), stored))

	jar, err := store.ListCookies(t.Context(), "w1")
	require.NoError(t, err)
	require.Len(t, jar, 1)
	assert.True(t, jar[0].Secure)
	assert.True(t, jar[0].HTTPOnly)
	require.NotNil(t, jar[0].Expires)
	assert.Equal(t, expires, *jar[0].Expires)

	// A session cookie keeps a null expiry rather than an empty string: the renderer branches on it.
	session := cookie("w1", "api.test", "/", "csrf", "y")
	require.NoError(t, store.UpsertCookie(t.Context(), session))

	jar, err = store.ListCookies(t.Context(), "w1")
	require.NoError(t, err)
	for _, c := range jar {
		if c.Name == "csrf" {
			assert.Nil(t, c.Expires)
		}
	}
}

func TestClearCookiesIsScopedToOneWorkspace(t *testing.T) {
	store, _ := newStore(t)
	require.NoError(t, store.UpsertCookie(t.Context(), cookie("w1", "api.test", "/", "a", "1")))
	require.NoError(t, store.UpsertCookie(t.Context(), cookie("w2", "api.test", "/", "a", "1")))

	require.NoError(t, store.ClearCookies(t.Context(), "w1"))

	mine, err := store.ListCookies(t.Context(), "w1")
	require.NoError(t, err)
	assert.Empty(t, mine)
	assert.NotNil(t, mine, "empty, never nil")

	theirs, err := store.ListCookies(t.Context(), "w2")
	require.NoError(t, err)
	assert.Len(t, theirs, 1)
}
