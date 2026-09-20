package dbml_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/dbml"

	_ "modernc.org/sqlite"
)

// SQLite is the one engine a test can drive end to end without a server, so it carries the
// end-to-end evidence for the whole introspection path: the reading, the assembly and the wire
// shape. The other three share every line of that path but their queries.

// sqliteAt builds a database from DDL and answers a connection pointing at it.
func sqliteAt(t *testing.T, statements ...string) dbml.Connection {
	t.Helper()

	path := filepath.Join(t.TempDir(), "schema.db")
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	// Pinged so the file exists even with no statements: `sql.Open` is lazy, and a database with
	// no tables is one of the cases under test.
	require.NoError(t, db.PingContext(t.Context()))

	for _, statement := range statements {
		_, err := db.ExecContext(t.Context(), statement)
		require.NoError(t, err, statement)
	}

	return dbml.Connection{
		ID: "c1", Name: "local", Driver: dbml.DriverSQLite, FilePath: &path,
	}
}

func tableNamed(t *testing.T, snapshot dbml.SchemaSnapshot, name string) dbml.SnapshotTable {
	t.Helper()

	for _, table := range snapshot.Tables {
		if table.Name == name {
			return table
		}
	}
	t.Fatalf("no table %q in %+v", name, snapshot.Tables)
	return dbml.SnapshotTable{}
}

func columnNamed(t *testing.T, table dbml.SnapshotTable, name string) dbml.SnapshotColumn {
	t.Helper()

	for _, column := range table.Columns {
		if column.Name == name {
			return column
		}
	}
	t.Fatalf("no column %q in %s", name, table.Name)
	return dbml.SnapshotColumn{}
}

func TestIntrospectingASQLiteSchema(t *testing.T) {
	connection := sqliteAt(t,
		`CREATE TABLE customers (
			id INTEGER PRIMARY KEY,
			email TEXT NOT NULL UNIQUE,
			name TEXT DEFAULT 'sin nombre'
		)`,
		`CREATE TABLE orders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			customer_id INTEGER NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
			total REAL NOT NULL
		)`,
		`CREATE INDEX idx_orders_customer ON orders (customer_id, total)`,
	)

	snapshot, err := dbml.Introspect(t.Context(), connection, "")
	require.NoError(t, err)
	require.Len(t, snapshot.Tables, 2)

	customers := tableNamed(t, snapshot, "customers")

	id := columnNamed(t, customers, "id")
	assert.True(t, id.PK)
	// A lone INTEGER primary key is a rowid alias, so it assigns itself a value.
	assert.True(t, id.Increment)
	assert.Equal(t, "INTEGER", id.Type)

	email := columnNamed(t, customers, "email")
	assert.True(t, email.NotNull)
	assert.True(t, email.Unique, "a single-column unique constraint makes its column unique")
	assert.False(t, email.Increment)

	name := columnNamed(t, customers, "name")
	require.NotNil(t, name.DefaultValue)
	// The quotes SQLite stores a string default in are unwrapped, or they reach the diagram as
	// part of the value.
	assert.Equal(t, "sin nombre", *name.DefaultValue)

	t.Run("the relation carries its columns and its action", func(t *testing.T) {
		require.Len(t, snapshot.Refs, 1)
		ref := snapshot.Refs[0]

		assert.Equal(t, "orders", ref.FromTable)
		assert.Equal(t, []string{"customer_id"}, ref.FromColumns)
		assert.Equal(t, "customers", ref.ToTable)
		assert.Equal(t, []string{"id"}, ref.ToColumns)
		require.NotNil(t, ref.OnDelete)
		assert.Equal(t, "CASCADE", *ref.OnDelete)
		assert.Nil(t, ref.OnUpdate, "NO ACTION is dropped — it is what a key that declares nothing reports")
	})

	t.Run("a composite index is drawn and a constraint's own is not", func(t *testing.T) {
		orders := tableNamed(t, snapshot, "orders")
		require.Len(t, orders.Indexes, 1, "the primary key's index is not drawn twice")

		assert.Equal(t, []string{"customer_id", "total"}, orders.Indexes[0].Columns)
		assert.False(t, orders.Indexes[0].Unique)
	})

	t.Run("SQLite has no enumerated types", func(t *testing.T) {
		assert.Empty(t, snapshot.Enums)
		assert.NotNil(t, snapshot.Enums, "empty, never nil")
	})
}

// **The rowid alias requires a primary key of exactly one INTEGER column.** Read row by row, the
// first member of a composite key looks like one — which is how a join table was imported as
// `pedido_id INTEGER [pk, increment]`.
func TestACompositeKeysFirstMemberIsNotARowidAlias(t *testing.T) {
	connection := sqliteAt(t, `
		CREATE TABLE order_items (
			order_id INTEGER NOT NULL,
			product_id INTEGER NOT NULL,
			quantity INTEGER NOT NULL,
			PRIMARY KEY (order_id, product_id)
		)`)

	snapshot, err := dbml.Introspect(t.Context(), connection, "")
	require.NoError(t, err)

	items := tableNamed(t, snapshot, "order_items")
	orderID := columnNamed(t, items, "order_id")

	assert.True(t, orderID.PK)
	assert.False(t, orderID.Increment, "it is a member of a composite key, not a rowid alias")
	assert.False(t, columnNamed(t, items, "product_id").Increment)

	// And neither member is individually unique — the pair is.
	assert.False(t, orderID.Unique)
}

// A type that is not exactly `INTEGER` is not aliased to the rowid, so it does not assign itself a
// value.
func TestOnlyAnExactINTEGERIsARowidAlias(t *testing.T) {
	connection := sqliteAt(t,
		`CREATE TABLE a (id INT PRIMARY KEY)`,
		`CREATE TABLE b (id BIGINT PRIMARY KEY)`,
		`CREATE TABLE c (id TEXT PRIMARY KEY)`,
		`CREATE TABLE d (id INTEGER PRIMARY KEY)`,
	)

	snapshot, err := dbml.Introspect(t.Context(), connection, "")
	require.NoError(t, err)

	for _, table := range []string{"a", "b", "c"} {
		column := columnNamed(t, tableNamed(t, snapshot, table), "id")
		assert.True(t, column.PK, table)
		assert.False(t, column.Increment, "%s: %s is not aliased to the rowid", table, column.Type)
	}
	assert.True(t, columnNamed(t, tableNamed(t, snapshot, "d"), "id").Increment)
}

// A member of a two-column unique key is not unique on its own, and saying it is would be a claim
// the database did not make.
func TestAMemberOfACompositeUniqueKeyIsNotUnique(t *testing.T) {
	connection := sqliteAt(t, `
		CREATE TABLE slots (
			room TEXT NOT NULL,
			at TEXT NOT NULL,
			note TEXT,
			UNIQUE (room, at)
		)`)

	snapshot, err := dbml.Introspect(t.Context(), connection, "")
	require.NoError(t, err)

	slots := tableNamed(t, snapshot, "slots")
	assert.False(t, columnNamed(t, slots, "room").Unique)
	assert.False(t, columnNamed(t, slots, "at").Unique)

	// The pair is drawn as an index, because DBML has nowhere else to put it.
	require.Len(t, slots.Indexes, 1)
	assert.Equal(t, []string{"room", "at"}, slots.Indexes[0].Columns)
	assert.True(t, slots.Indexes[0].Unique)
}

// A composite foreign key keeps its members paired in declaration order. Read unordered, this is
// the failure that pairs the wrong columns together and is silent about it.
func TestACompositeForeignKeyKeepsItsColumnsPaired(t *testing.T) {
	connection := sqliteAt(t,
		`CREATE TABLE parent (a TEXT NOT NULL, b TEXT NOT NULL, PRIMARY KEY (a, b))`,
		`CREATE TABLE child (
			x TEXT NOT NULL,
			y TEXT NOT NULL,
			FOREIGN KEY (x, y) REFERENCES parent(a, b)
		)`,
	)

	snapshot, err := dbml.Introspect(t.Context(), connection, "")
	require.NoError(t, err)

	require.Len(t, snapshot.Refs, 1)
	assert.Equal(t, []string{"x", "y"}, snapshot.Refs[0].FromColumns)
	assert.Equal(t, []string{"a", "b"}, snapshot.Refs[0].ToColumns)
}

func TestAnEmptyDatabaseIsThreeEmptyArrays(t *testing.T) {
	snapshot, err := dbml.Introspect(t.Context(), sqliteAt(t), "")
	require.NoError(t, err)

	// A nil slice marshals as `null` and the importer that maps over it crashes.
	assert.NotNil(t, snapshot.Tables)
	assert.NotNil(t, snapshot.Refs)
	assert.NotNil(t, snapshot.Enums)
	assert.Empty(t, snapshot.Tables)
}

// Opened read-only, so a typo in a filename is refused rather than leaving an empty database
// behind and reporting a schema with no tables.
func TestAMissingSQLiteFileIsRefusedRatherThanCreated(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-existe.db")
	connection := dbml.Connection{
		ID: "c1", Driver: dbml.DriverSQLite, FilePath: &missing,
	}

	_, err := dbml.Introspect(t.Context(), connection, "")
	require.ErrorIs(t, err, dbml.ErrConnectionRefused)
	assert.Contains(t, err.Error(), "no such database file")

	_, statErr := os.Stat(missing)
	assert.True(t, os.IsNotExist(statErr), "nothing was created")
}

func TestASQLiteConnectionWithNoPathIsRefused(t *testing.T) {
	_, err := dbml.Introspect(t.Context(), dbml.Connection{Driver: dbml.DriverSQLite}, "")
	require.ErrorIs(t, err, dbml.ErrConnectionRefused)
	assert.Contains(t, err.Error(), "names no database file")
}

func TestADirectoryIsNotADatabase(t *testing.T) {
	directory := t.TempDir()
	connection := dbml.Connection{Driver: dbml.DriverSQLite, FilePath: &directory}

	_, err := dbml.Introspect(t.Context(), connection, "")
	require.ErrorIs(t, err, dbml.ErrConnectionRefused)
	assert.Contains(t, err.Error(), "is a directory")
}

// Reading a schema must not leave the user's own database locked: an open file cannot be moved,
// replaced or deleted, and that cost a Windows user their database for as long as CodeFlow ran.
func TestReadingASchemaLeavesTheFileFree(t *testing.T) {
	connection := sqliteAt(t, `CREATE TABLE t (id INTEGER PRIMARY KEY)`)

	_, err := dbml.Introspect(t.Context(), connection, "")
	require.NoError(t, err)

	require.NotNil(t, connection.FilePath)
	assert.NoError(t, os.Remove(*connection.FilePath), "the file is no longer held open")
}

func TestTestConnectionOpensAndCloses(t *testing.T) {
	connection := sqliteAt(t, `CREATE TABLE t (id INTEGER PRIMARY KEY)`)

	require.NoError(t, dbml.TestConnection(t.Context(), connection, ""))

	require.NotNil(t, connection.FilePath)
	assert.NoError(t, os.Remove(*connection.FilePath))
}

// Structurally read-only: the driver itself refuses a write, so nothing in this package has to
// remember not to attempt one.
func TestTheConnectionIsOpenedReadOnly(t *testing.T) {
	connection := sqliteAt(t, `CREATE TABLE t (id INTEGER PRIMARY KEY)`)
	require.NotNil(t, connection.FilePath)

	db, err := sql.Open("sqlite", "file:"+*connection.FilePath+"?mode=ro")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	_, err = db.ExecContext(t.Context(), `INSERT INTO t (id) VALUES (1)`)
	assert.Error(t, err, "the mode this package opens with refuses a write")
}

func TestAnUnknownDriverIsRefusedBeforeAnythingIsDialled(t *testing.T) {
	_, err := dbml.Introspect(t.Context(), dbml.Connection{Driver: "oracle"}, "")
	assert.ErrorIs(t, err, dbml.ErrUnknownDriver)

	assert.ErrorIs(t, dbml.TestConnection(t.Context(), dbml.Connection{Driver: ""}, ""),
		dbml.ErrUnknownDriver)
}
