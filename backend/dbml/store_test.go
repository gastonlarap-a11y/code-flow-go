package dbml_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/dbml"
	"github.com/gastonlarap-a11y/code-flow/backend/storage"
)

var clock = storage.FixedClock{At: time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)}

func newStore(t *testing.T) (*dbml.Store, *storage.DB) {
	t.Helper()

	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "codeflow.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	require.NoError(t, db.Write(t.Context(), func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO workspaces (id, name, created_at) VALUES ('w1', 'Work', 'now')`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO projects (id, workspace_id, name, local_path, created_at)
			 VALUES ('p1', 'w1', 'api', '/tmp/api', 'now')`)
		return err
	}))
	return dbml.NewStore(db, clock), db
}

func ptr[T any](value T) *T { return &value }

// ---- layouts (DBML-005) --------------------------------------------------------------------------

func TestPositionsRoundTripOrderedByTableKey(t *testing.T) {
	store, _ := newStore(t)

	require.NoError(t, store.SavePositions(t.Context(), "p1", "schemas/orders.dbml",
		[]dbml.TablePosition{
			{TableKey: "public.orders", X: 120, Y: 40},
			{TableKey: "public.customers", X: 10.5, Y: -20.25},
		}))

	loaded, err := store.LoadLayout(t.Context(), "p1", "schemas/orders.dbml")
	require.NoError(t, err)
	require.Len(t, loaded, 2)

	assert.Equal(t, "public.customers", loaded[0].TableKey, "ordered by table_key")
	assert.InDelta(t, 10.5, loaded[0].X, 0.0001)
	assert.InDelta(t, -20.25, loaded[0].Y, 0.0001, "a negative coordinate survives")
	assert.Equal(t, "public.orders", loaded[1].TableKey)
}

func TestSavingAgainMovesTheTableRatherThanAddingARow(t *testing.T) {
	store, _ := newStore(t)

	require.NoError(t, store.SavePositions(t.Context(), "p1", "a.dbml",
		[]dbml.TablePosition{{TableKey: "t", X: 1, Y: 1}}))
	require.NoError(t, store.SavePositions(t.Context(), "p1", "a.dbml",
		[]dbml.TablePosition{{TableKey: "t", X: 2, Y: 2}}))

	loaded, err := store.LoadLayout(t.Context(), "p1", "a.dbml")
	require.NoError(t, err)
	require.Len(t, loaded, 1)
	assert.InDelta(t, 2.0, loaded[0].X, 0.0001)
}

// A key repeated within one save keeps its **last** position, because that is what the last
// `DO UPDATE` in the statement writes.
func TestAKeyRepeatedInOneSaveKeepsItsLastPosition(t *testing.T) {
	store, _ := newStore(t)

	require.NoError(t, store.SavePositions(t.Context(), "p1", "a.dbml", []dbml.TablePosition{
		{TableKey: "t", X: 1, Y: 1},
		{TableKey: "t", X: 9, Y: 9},
	}))

	loaded, err := store.LoadLayout(t.Context(), "p1", "a.dbml")
	require.NoError(t, err)
	require.Len(t, loaded, 1)
	assert.InDelta(t, 9.0, loaded[0].X, 0.0001)
}

// A blank key refuses the **whole** save and writes nothing: one row nothing could read back would
// leave the layout disagreeing with what the user sees.
func TestABlankTableKeyRefusesTheWholeSave(t *testing.T) {
	store, _ := newStore(t)

	require.NoError(t, store.SavePositions(t.Context(), "p1", "a.dbml",
		[]dbml.TablePosition{{TableKey: "existente", X: 1, Y: 1}}))

	err := store.SavePositions(t.Context(), "p1", "a.dbml", []dbml.TablePosition{
		{TableKey: "nueva", X: 5, Y: 5},
		{TableKey: "   ", X: 6, Y: 6},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a position is missing its table_key")

	loaded, err := store.LoadLayout(t.Context(), "p1", "a.dbml")
	require.NoError(t, err)
	require.Len(t, loaded, 1)
	assert.Equal(t, "existente", loaded[0].TableKey, "nothing from the refused save landed")
}

func TestAnEmptySaveWritesNothingAndIsNotAnError(t *testing.T) {
	store, _ := newStore(t)

	require.NoError(t, store.SavePositions(t.Context(), "p1", "a.dbml", nil))
	require.NoError(t, store.SavePositions(t.Context(), "p1", "a.dbml", []dbml.TablePosition{}))

	loaded, err := store.LoadLayout(t.Context(), "p1", "a.dbml")
	require.NoError(t, err)
	assert.Empty(t, loaded)
}

func TestTooManyPositionsAreRefused(t *testing.T) {
	store, _ := newStore(t)

	positions := make([]dbml.TablePosition, 0, 2001)
	for i := range 2001 {
		positions = append(positions, dbml.TablePosition{TableKey: string(rune(i)) + "t"})
	}

	err := store.SavePositions(t.Context(), "p1", "a.dbml", positions)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "2000")
}

// A save for a project that does not exist fails on the foreign key rather than filing orphan rows.
func TestASaveForAnUnknownProjectFails(t *testing.T) {
	store, _ := newStore(t)

	err := store.SavePositions(t.Context(), "no-existe", "a.dbml",
		[]dbml.TablePosition{{TableKey: "t", X: 1, Y: 1}})
	assert.Error(t, err)
}

func TestLayoutsAreScopedToOneDocument(t *testing.T) {
	store, _ := newStore(t)

	require.NoError(t, store.SavePositions(t.Context(), "p1", "a.dbml",
		[]dbml.TablePosition{{TableKey: "t", X: 1, Y: 1}}))
	require.NoError(t, store.SavePositions(t.Context(), "p1", "b.dbml",
		[]dbml.TablePosition{{TableKey: "t", X: 2, Y: 2}}))

	// The same table name in two documents is two positions: the key is the triple.
	first, err := store.LoadLayout(t.Context(), "p1", "a.dbml")
	require.NoError(t, err)
	assert.InDelta(t, 1.0, first[0].X, 0.0001)

	second, err := store.LoadLayout(t.Context(), "p1", "b.dbml")
	require.NoError(t, err)
	assert.InDelta(t, 2.0, second[0].X, 0.0001)

	require.NoError(t, store.ClearLayout(t.Context(), "p1", "a.dbml"))

	cleared, err := store.LoadLayout(t.Context(), "p1", "a.dbml")
	require.NoError(t, err)
	assert.Empty(t, cleared)

	kept, err := store.LoadLayout(t.Context(), "p1", "b.dbml")
	require.NoError(t, err)
	assert.Len(t, kept, 1, "the other document is untouched")
}

// Rows are **not** pruned for tables the document no longer declares: renaming a table and undoing
// the rename must not cost its position, and the layout ignores a key it has no table for.
func TestAPositionOutlivesTheTableItPlaced(t *testing.T) {
	store, _ := newStore(t)

	require.NoError(t, store.SavePositions(t.Context(), "p1", "a.dbml",
		[]dbml.TablePosition{{TableKey: "pedidos", X: 5, Y: 5}}))
	// The user renames the table, so the next save carries only the new key.
	require.NoError(t, store.SavePositions(t.Context(), "p1", "a.dbml",
		[]dbml.TablePosition{{TableKey: "orders", X: 7, Y: 7}}))

	loaded, err := store.LoadLayout(t.Context(), "p1", "a.dbml")
	require.NoError(t, err)
	assert.Len(t, loaded, 2, "the old key is still there for when the rename is undone")
}

func TestThePositionWireShapeStaysSnakeCase(t *testing.T) {
	encoded, err := json.Marshal(dbml.TablePosition{TableKey: "public.orders", X: 1, Y: 2})
	require.NoError(t, err)

	// These are rows sent back — the exception the API client's own commands document — so the keys
	// stay as the database spells them in both directions.
	assert.JSONEq(t, `{"table_key":"public.orders","x":1,"y":2}`, string(encoded))
}

// ---- connections (DBML-023) ----------------------------------------------------------------------

func TestConnectionsRoundTripWithoutAPassword(t *testing.T) {
	store, _ := newStore(t)

	saved, err := store.UpsertConnection(t.Context(), dbml.NewConnection{
		Name: "staging", Driver: dbml.DriverPostgres,
		Host: ptr("db.test"), Port: ptr(int64(5432)), Database: ptr("app"),
		Username: ptr("readonly"), UseTLS: true,
		Password: ptr("no-debería-viajar"),
	})
	require.NoError(t, err)

	assert.NotEmpty(t, saved.ID)
	assert.Equal(t, "staging", saved.Name)
	assert.True(t, saved.UseTLS)

	// The type has no password field at all, which is what makes `DBML-024` a property of the type
	// rather than of every handler remembering.
	encoded, err := json.Marshal(saved)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "password")
	assert.NotContains(t, string(encoded), "no-debería-viajar")

	listed, err := store.ListConnections(t.Context())
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, saved.ID, listed[0].ID)
}

func TestUpdatingAConnectionKeepsItsIDAndCreationTime(t *testing.T) {
	store, _ := newStore(t)

	first, err := store.UpsertConnection(t.Context(), dbml.NewConnection{
		Name: "staging", Driver: dbml.DriverMySQL, Host: ptr("old.test"),
	})
	require.NoError(t, err)

	second, err := store.UpsertConnection(t.Context(), dbml.NewConnection{
		ID: &first.ID, Name: "staging renamed", Driver: dbml.DriverMySQL, Host: ptr("new.test"),
	})
	require.NoError(t, err)

	assert.Equal(t, first.ID, second.ID)
	assert.Equal(t, first.CreatedAt, second.CreatedAt)
	assert.Equal(t, "staging renamed", second.Name)
	require.NotNil(t, second.Host)
	assert.Equal(t, "new.test", *second.Host)

	listed, err := store.ListConnections(t.Context())
	require.NoError(t, err)
	assert.Len(t, listed, 1, "an update, not a second row")
}

// An unrecognised driver is an **error**, unlike the AI engine catalogue which falls back to
// Claude: a schema read with the wrong engine is not a degraded answer, it is a wrong one.
func TestAnUnknownDriverIsRefused(t *testing.T) {
	store, _ := newStore(t)

	_, err := store.UpsertConnection(t.Context(), dbml.NewConnection{
		Name: "raro", Driver: "oracle",
	})
	require.ErrorIs(t, err, dbml.ErrUnknownDriver)
	assert.Contains(t, err.Error(), "oracle")

	listed, err := store.ListConnections(t.Context())
	require.NoError(t, err)
	assert.Empty(t, listed, "nothing was written")
}

func TestKnownDriver(t *testing.T) {
	for _, driver := range []string{"postgres", "sqlserver", "mysql", "sqlite"} {
		assert.True(t, dbml.KnownDriver(driver), driver)
	}
	for _, driver := range []string{"", "oracle", "Postgres", "postgresql"} {
		assert.False(t, dbml.KnownDriver(driver), driver)
	}
}

// SQLite is a file rather than a server, so its connection carries a path and no host.
func TestASQLiteConnectionCarriesAPathRatherThanAHost(t *testing.T) {
	store, _ := newStore(t)

	saved, err := store.UpsertConnection(t.Context(), dbml.NewConnection{
		Name: "local", Driver: dbml.DriverSQLite, FilePath: ptr("/data/app.db"),
	})
	require.NoError(t, err)

	assert.Nil(t, saved.Host)
	assert.Nil(t, saved.Port)
	require.NotNil(t, saved.FilePath)
	assert.Equal(t, "/data/app.db", *saved.FilePath)
}

func TestGettingAConnectionThatIsNotThere(t *testing.T) {
	store, _ := newStore(t)

	_, err := store.GetConnection(t.Context(), "no-existe")
	assert.ErrorIs(t, err, dbml.ErrNotFound)
}

func TestAnEmptyConnectionListIsAnArrayNotNull(t *testing.T) {
	store, _ := newStore(t)

	listed, err := store.ListConnections(t.Context())
	require.NoError(t, err)
	assert.NotNil(t, listed)

	encoded, err := json.Marshal(listed)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(encoded))
}

// Connections are scoped to neither a project nor a workspace: the same staging database is read
// from whichever folder happens to be open.
func TestConnectionsAreNotScopedToAnything(t *testing.T) {
	store, db := newStore(t)

	_, err := store.UpsertConnection(t.Context(), dbml.NewConnection{
		Name: "staging", Driver: dbml.DriverPostgres, Host: ptr("db.test"),
	})
	require.NoError(t, err)

	// Deleting the project takes its layouts and leaves the connection alone.
	require.NoError(t, db.Write(t.Context(), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM projects WHERE id = 'p1'`)
		return err
	}))

	listed, err := store.ListConnections(t.Context())
	require.NoError(t, err)
	assert.Len(t, listed, 1)
}
