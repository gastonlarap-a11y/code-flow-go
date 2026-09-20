package dbml

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// SQLite (DBML-026).
//
// The odd one out in three ways: it is a file rather than a server, it answers through `PRAGMA`
// functions rather than a catalogue, and it is opened **read-only** so a path that does not exist is
// refused rather than created — a typo in a filename would otherwise leave an empty database behind
// and report a schema with no tables.

type sqliteIntrospector struct{}

func (sqliteIntrospector) dsn(connection Connection, _ string) (string, string, error) {
	path := stringOf(connection.FilePath)
	if path == "" {
		return "", "", errNoDatabaseFile
	}

	// Checked before opening, because `mode=ro` refuses a missing file with a message about the
	// mode rather than about the file.
	info, err := os.Stat(path)
	if err != nil {
		return "", "", fmt.Errorf("%w: %s", ErrConnectionRefused, "no such database file: "+path)
	}
	if info.IsDir() {
		return "", "", fmt.Errorf("%w: %s", ErrConnectionRefused, path+" is a directory, not a database")
	}

	// `mode=ro` is the read-only rule made structural: the driver refuses a write, so nothing in
	// this package has to remember not to attempt one.
	return "sqlite", "file:" + url.PathEscape(path) + "?mode=ro&_pragma=busy_timeout(5000)", nil
}

var errNoDatabaseFile = fmt.Errorf("%w: this connection names no database file", ErrConnectionRefused)

// read walks the tables one at a time: `PRAGMA` is a function per table, not a catalogue.
func (s sqliteIntrospector) read(ctx context.Context, db *sql.DB, _ Connection, into *snapshotBuilder) error {
	tables, err := s.tableNames(ctx, db)
	if err != nil {
		return err
	}

	for _, table := range tables {
		if err := s.readColumns(ctx, db, table, into); err != nil {
			return err
		}
		if err := s.readRefs(ctx, db, table, into); err != nil {
			return err
		}
		if err := s.readIndexes(ctx, db, table, into); err != nil {
			return err
		}
	}
	return nil
}

// sqliteTable is one table. Only its name, because everything else comes from a `PRAGMA`: the
// stored DDL is not read, since the one thing it alone carries — `AUTOINCREMENT` — cannot change
// whether a column increments.
type sqliteTable struct{ name string }

func (sqliteIntrospector) tableNames(ctx context.Context, db *sql.DB) ([]sqliteTable, error) {
	rows, done, err := query(ctx, db, "table list", `
		SELECT name FROM sqlite_master
		 WHERE type = 'table' AND name NOT LIKE 'sqlite_%'
		 ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer done()

	tables := make([]sqliteTable, 0, 16)
	for rows.Next() {
		var table sqliteTable
		if err := rows.Scan(&table.name); err != nil {
			return nil, fmt.Errorf("scan a table name: %w", err)
		}
		tables = append(tables, table)
	}
	return tables, rows.Err()
}

func (sqliteIntrospector) readColumns(ctx context.Context, db *sql.DB, table sqliteTable, into *snapshotBuilder) error {
	rows, done, err := query(ctx, db, "columns",
		`SELECT cid, name, type, "notnull", dflt_value, pk FROM pragma_table_info(?)`, table.name)
	if err != nil {
		return err
	}
	defer done()

	type pragmaColumn struct {
		name, dataType string
		notNull        bool
		defaultValue   *string
		keyPosition    int
		position       int
	}
	columns := make([]pragmaColumn, 0, 8)

	for rows.Next() {
		var column pragmaColumn
		var defaultValue sql.NullString

		if err := rows.Scan(&column.position, &column.name, &column.dataType,
			&column.notNull, &defaultValue, &column.keyPosition); err != nil {
			return fmt.Errorf("scan a column: %w", err)
		}
		column.defaultValue = nonEmpty(stripSQLiteDefault(text(defaultValue)))
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// **The rowid alias requires a primary key of exactly one INTEGER column.** Read row by row,
	// the first member of a composite key looks like one — which is how a join table was imported
	// as `pedido_id INTEGER [pk, increment]`, found by importing a real database.
	keyMembers := 0
	for _, column := range columns {
		if column.keyPosition > 0 {
			keyMembers++
		}
	}

	for _, column := range columns {
		// Exactly `INTEGER`: SQLite does not alias `INT`, `BIGINT` or anything else to the rowid,
		// so a column declared that way does not assign itself a value.
		rowidAlias := keyMembers == 1 && column.keyPosition == 1 &&
			strings.EqualFold(strings.TrimSpace(column.dataType), "INTEGER")

		into.columns = append(into.columns, columnRow{
			schema: "", table: table.name, name: column.name, dataType: column.dataType,
			notNull: column.notNull,
			// A rowid alias assigns itself a value on insert, which is what `increment` means
			// here. `AUTOINCREMENT` only changes **how the next one is chosen** — it cannot apply
			// to a column that is not already an alias — so it adds nothing to this boolean and
			// the stored DDL is not read for it.
			increment:    rowidAlias,
			defaultValue: column.defaultValue,
			position:     column.position,
		})

		if column.keyPosition > 0 {
			into.constraints = append(into.constraints, constraintRow{
				schema: "", table: table.name, constraint: "pk_" + table.name,
				column: column.name, kind: "p", position: column.keyPosition,
			})
		}
	}
	return nil
}

func (sqliteIntrospector) readRefs(ctx context.Context, db *sql.DB, table sqliteTable, into *snapshotBuilder) error {
	rows, done, err := query(ctx, db, "relations",
		`SELECT id, seq, "table", "from", "to", on_update, on_delete
		   FROM pragma_foreign_key_list(?)`, table.name)
	if err != nil {
		return err
	}
	defer done()

	for rows.Next() {
		var id, seq int
		var toTable, fromColumn string
		var toColumn sql.NullString
		var onUpdate, onDelete sql.NullString

		if err := rows.Scan(&id, &seq, &toTable, &fromColumn, &toColumn,
			&onUpdate, &onDelete); err != nil {
			return fmt.Errorf("scan a relation: %w", err)
		}

		// A null `to` means the key points at the other table's primary key without naming it.
		target := toColumn.String
		if !toColumn.Valid {
			target = "rowid"
		}

		into.refs = append(into.refs, refRow{
			constraint: fmt.Sprintf("fk_%s_%d", table.name, id),
			fromSchema: "", fromTable: table.name, fromColumn: fromColumn,
			toSchema: "", toTable: toTable, toColumn: target,
			onDelete: text(onDelete), onUpdate: text(onUpdate),
			position: seq,
		})
	}
	return rows.Err()
}

func (sqliteIntrospector) readIndexes(ctx context.Context, db *sql.DB, table sqliteTable, into *snapshotBuilder) error {
	rows, done, err := query(ctx, db, "indexes",
		`SELECT name, "unique", origin FROM pragma_index_list(?)`, table.name)
	if err != nil {
		return err
	}
	defer done()

	indexes := make([]pragmaIndex, 0, 4)

	for rows.Next() {
		var index pragmaIndex
		if err := rows.Scan(&index.name, &index.unique, &index.origin); err != nil {
			return fmt.Errorf("scan an index: %w", err)
		}
		indexes = append(indexes, index)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, index := range indexes {
		// `pk` is the index behind the primary key, which `pragma_table_info` already reported as
		// a constraint; keeping it would draw the same thing twice.
		if index.origin == "pk" {
			continue
		}
		if err := readSQLiteIndexColumns(ctx, db, table.name, index, into); err != nil {
			return err
		}
	}
	return nil
}

// readSQLiteIndexColumns records one index's members — and, when the index **is** a constraint,
// records it as one too.
//
// SQLite reports a `UNIQUE` declaration only as an index with origin `u`; there is no constraint
// catalogue to read it from. Without this, a `email TEXT UNIQUE` column came back with
// `unique: false` — the diagram lost a constraint the database does enforce.
func readSQLiteIndexColumns(ctx context.Context, db *sql.DB, table string, index pragmaIndex, into *snapshotBuilder) error {
	rows, done, err := query(ctx, db, "index columns",
		`SELECT seqno, name FROM pragma_index_info(?)`, index.name)
	if err != nil {
		return err
	}
	defer done()

	for rows.Next() {
		var position int
		var column sql.NullString

		if err := rows.Scan(&position, &column); err != nil {
			return fmt.Errorf("scan an index column: %w", err)
		}
		if !column.Valid {
			// An expression index has no column; there is nothing to draw it against.
			continue
		}

		into.indexes = append(into.indexes, indexRow{
			schema: "", table: table, name: index.name, column: column.String,
			unique: index.unique, primary: false, position: position,
		})

		// Origin `u` is a `UNIQUE` declaration rather than a `CREATE INDEX`, which every other
		// engine reports from a constraint catalogue SQLite does not have. Recorded as both, so the
		// builder's own rules decide what the column says and what the diagram draws.
		if index.origin == "u" {
			into.constraints = append(into.constraints, constraintRow{
				schema: "", table: table, constraint: index.name, column: column.String,
				kind: "u", position: position,
			})
		}
	}
	return rows.Err()
}

// pragmaIndex is one row of `pragma_index_list`.
type pragmaIndex struct {
	name   string
	unique bool
	// origin is `pk` for the primary key's index, `u` for a `UNIQUE` declaration, and `c` for a
	// `CREATE INDEX`.
	origin string
}

// stripSQLiteDefault unwraps the quotes SQLite stores a string default in.
func stripSQLiteDefault(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.Trim(strings.TrimSpace(*value), "'\"")
	return &trimmed
}
