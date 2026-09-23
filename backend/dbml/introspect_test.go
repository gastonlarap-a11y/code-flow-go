package dbml_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/dbml"
)

// `XLANG-018`: a database that could not be reached carries a sentinel, so the panel shows the
// driver's own diagnosis — a wrong port, a bad password, a server that is not running — instead of
// "could not import".

func TestARefusedDatabaseCarriesItsSentinel(t *testing.T) {
	store, _ := newStore(t)
	credentials := newFakeCredentials()
	deps := dbml.Deps{Store: store, Credentials: credentials}

	saved, err := deps.SaveConnection(t.Context(), dbml.NewConnection{
		Name: "apagada", Driver: dbml.DriverPostgres,
		// Port 1 answers nothing on any machine this runs on.
		Host: new("127.0.0.1"), Port: new(int64(1)), Database: new("app"),
		Username: new("postgres"), Password: new("la-clave-secreta"),
	})
	require.NoError(t, err)

	registry := registryFor(t, deps)

	for _, command := range []string{"dbml_test_connection", "dbml_introspect_database"} {
		t.Run(command, func(t *testing.T) {
			_, err := invoke(t, registry, command, map[string]any{"connectionId": saved.ID})
			require.Error(t, err)

			// Position 0, which is where the renderer looks for it.
			assert.True(t, strings.HasPrefix(err.Error(), "DB_CONNECTION_REFUSED: "), err.Error())

			// **And never the connection string.** It holds the password, and several drivers put
			// it in their own exception — which would carry it into a toast, a log and a bug
			// report.
			assert.NotContains(t, err.Error(), "la-clave-secreta")
			assert.NotContains(t, err.Error(), "password=")
			assert.NotContains(t, err.Error(), "postgres://")
		})
	}
}

// The sentinel is for a database that said no. Everything else — an unknown driver, a missing
// connection — is an ordinary failure, because the two lead to different next steps.
func TestOnlyARefusalCarriesTheSentinel(t *testing.T) {
	store, _ := newStore(t)
	registry := registryFor(t, dbml.Deps{Store: store})

	_, err := invoke(t, registry, "dbml_test_connection",
		map[string]any{"connectionId": "no-existe"})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "DB_CONNECTION_REFUSED")
	assert.ErrorIs(t, err, dbml.ErrNotFound)
}

// A connection with no password at all still works: a database that takes none is ordinary, and a
// missing secret is not a failure to read one.
func TestAConnectionWithNoStoredPasswordStillDials(t *testing.T) {
	store, _ := newStore(t)
	credentials := newFakeCredentials()
	deps := dbml.Deps{Store: store, Credentials: credentials}

	connection := sqliteAt(t, `CREATE TABLE t (id INTEGER PRIMARY KEY)`)
	saved, err := deps.SaveConnection(t.Context(), dbml.NewConnection{
		Name: "local", Driver: dbml.DriverSQLite, FilePath: connection.FilePath,
	})
	require.NoError(t, err)

	registry := registryFor(t, deps)

	answer, err := invoke(t, registry, "dbml_introspect_database",
		map[string]any{"connectionId": saved.ID})
	require.NoError(t, err)

	snapshot, ok := answer.(dbml.SchemaSnapshot)
	require.True(t, ok)
	assert.Len(t, snapshot.Tables, 1)
}

// The password reaches the driver and nothing else: it is read inside this process, handed over,
// and never returned in any shape.
func TestThePasswordIsReadForTheDialAndNothingElse(t *testing.T) {
	store, _ := newStore(t)
	credentials := newFakeCredentials()
	deps := dbml.Deps{Store: store, Credentials: credentials}

	connection := sqliteAt(t, `CREATE TABLE t (id INTEGER PRIMARY KEY)`)
	saved, err := deps.SaveConnection(t.Context(), dbml.NewConnection{
		Name: "local", Driver: dbml.DriverSQLite, FilePath: connection.FilePath,
		Password: new("la-clave"),
	})
	require.NoError(t, err)

	credentials.calls = nil
	registry := registryFor(t, deps)

	_, err = invoke(t, registry, "dbml_introspect_database",
		map[string]any{"connectionId": saved.ID})
	require.NoError(t, err)

	assert.Equal(t, []string{"get:" + saved.ID}, credentials.calls, "read once, for the dial")
}

func TestTheSnapshotCrossesInTheRenderersShape(t *testing.T) {
	connection := sqliteAt(t, `
		CREATE TABLE customers (
			id INTEGER PRIMARY KEY,
			email TEXT NOT NULL UNIQUE
		)`)

	snapshot, err := dbml.Introspect(t.Context(), connection, "")
	require.NoError(t, err)

	raw, err := json.Marshal(snapshot)
	require.NoError(t, err)
	encoded := string(raw)

	// snake_case throughout, as the renderer's own `DbmlSchemaSnapshot` declares it. A field
	// renamed on this side compiles and arrives as `undefined`.
	for _, key := range []string{
		`"tables"`, `"refs"`, `"enums"`, `"not_null"`, `"default_value"`, `"increment"`,
	} {
		assert.Contains(t, encoded, key)
	}
	// And a column with no default is `null`, never omitted: the renderer branches on it.
	assert.Contains(t, encoded, `"default_value":null`)
}
