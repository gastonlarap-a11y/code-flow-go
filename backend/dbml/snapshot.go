package dbml

import (
	"sort"
	"strings"
)

// The structured schema an introspection answers with (DBML-025).
//
// **Not DBML text.** The renderer turns this into a document, so emission lives in exactly one
// place — a pure function its own tests can call — instead of once per engine here, where nothing
// could test it without a server and four copies would drift.
//
// Between the engines and this sits the builder below: every engine answers the same five questions
// in its own dialect and **as rows**, so assembling those rows into a schema is identical work done
// once. That is also what makes all four testable — a synthetic row set proves the assembly without
// a PostgreSQL, a SQL Server and a MySQL to connect to.

// SchemaSnapshot is a whole database, reduced to what a diagram needs.
type SchemaSnapshot struct {
	Tables []SnapshotTable `json:"tables"`
	Refs   []SnapshotRef   `json:"refs"`
	Enums  []SnapshotEnum  `json:"enums"`
}

// SnapshotTable is one table and what it declares.
type SnapshotTable struct {
	Schema  string           `json:"schema"`
	Name    string           `json:"name"`
	Columns []SnapshotColumn `json:"columns"`
	Indexes []SnapshotIndex  `json:"indexes"`
}

// SnapshotColumn is one column.
type SnapshotColumn struct {
	Name string `json:"name"`
	// Type is as the engine spells it, arguments included — `varchar(120)`.
	Type    string `json:"type"`
	PK      bool   `json:"pk"`
	NotNull bool   `json:"not_null"`
	// Unique is true only for a **single-column** unique constraint: a member of a two-column
	// unique key is not unique on its own, and saying it is would be a claim the database did not
	// make.
	Unique       bool    `json:"unique"`
	Increment    bool    `json:"increment"`
	DefaultValue *string `json:"default_value"`
}

// SnapshotIndex is one index worth drawing.
type SnapshotIndex struct {
	Name    *string  `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
	PK      bool     `json:"pk"`
}

// SnapshotRef is one foreign key.
type SnapshotRef struct {
	FromSchema  string   `json:"from_schema"`
	FromTable   string   `json:"from_table"`
	FromColumns []string `json:"from_columns"`
	ToSchema    string   `json:"to_schema"`
	ToTable     string   `json:"to_table"`
	ToColumns   []string `json:"to_columns"`
	OnDelete    *string  `json:"on_delete"`
	OnUpdate    *string  `json:"on_update"`
}

// SnapshotEnum is one enumerated type. PostgreSQL is the only engine that has them.
type SnapshotEnum struct {
	Schema string   `json:"schema"`
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// ---- the rows every engine answers in --------------------------------------------------------------

// columnRow is one column as a catalogue reports it.
type columnRow struct {
	schema, table, name, dataType string
	notNull, increment            bool
	defaultValue                  *string
	position                      int
}

// constraintRow is one member of a key or unique constraint, **with its position**.
//
// The position is the reason three of the four engines read from a system catalogue rather than
// from `information_schema`: a composite key read through `constraint_column_usage` comes back with
// its columns unordered, which pairs the wrong ones together and is silent about it.
type constraintRow struct {
	schema, table, constraint, column string
	kind                              string // "p" for primary key, "u" for unique
	position                          int
}

// refRow is one member of a foreign key, also positioned, and for the same reason.
type refRow struct {
	constraint                        string
	fromSchema, fromTable, fromColumn string
	toSchema, toTable, toColumn       string
	onDelete, onUpdate                *string
	position                          int
}

// indexRow is one member of an index.
type indexRow struct {
	schema, table, name, column string
	unique, primary             bool
	position                    int
}

// enumRow is one value of an enumerated type.
type enumRow struct {
	schema, name, value string
	position            int
}

// ---- the builder ---------------------------------------------------------------------------------

// snapshotBuilder assembles rows into a schema (DBML-025).
type snapshotBuilder struct {
	columns     []columnRow
	constraints []constraintRow
	refs        []refRow
	indexes     []indexRow
	enums       []enumRow
}

// build turns the five row sets into the snapshot the renderer emits from.
func (b *snapshotBuilder) build() SchemaSnapshot {
	primaryKeys, uniqueSingles, constraintSize := b.keySets()

	snapshot := SchemaSnapshot{
		Tables: b.buildTables(primaryKeys, uniqueSingles, constraintSize),
		Refs:   b.buildRefs(),
		Enums:  b.buildEnums(),
	}
	return snapshot
}

// keySets reduces the constraint rows to the three questions a column and an index have to answer.
//
// The third one is **how many members each constraint has**, not merely which names are
// constraints: that is what decides whether its backing index is worth drawing.
func (b *snapshotBuilder) keySets() (primaryKeys, uniqueSingles map[string]bool, constraintSize map[string]int) {
	primaryKeys = map[string]bool{}
	uniqueSingles = map[string]bool{}
	constraintSize = map[string]int{}

	members := map[string][]constraintRow{}
	for _, row := range b.constraints {
		key := row.schema + "." + row.table + "." + row.constraint
		members[key] = append(members[key], row)
	}

	for key, rows := range members {
		constraintSize[key] = len(rows)

		for _, row := range rows {
			column := row.schema + "." + row.table + "." + row.column
			if row.kind == "p" {
				primaryKeys[column] = true
				continue
			}
			// **Only a single-column unique constraint** makes its column unique.
			if len(rows) == 1 {
				uniqueSingles[column] = true
			}
		}
	}
	return primaryKeys, uniqueSingles, constraintSize
}

func (b *snapshotBuilder) buildTables(primaryKeys, uniqueSingles map[string]bool, constraintSize map[string]int) []SnapshotTable {
	order := make([]string, 0, 16)
	byTable := map[string]*SnapshotTable{}

	for _, row := range b.columns {
		key := row.schema + "." + row.table
		table, seen := byTable[key]
		if !seen {
			table = &SnapshotTable{
				Schema: row.schema, Name: row.table,
				Columns: make([]SnapshotColumn, 0, 8),
				Indexes: make([]SnapshotIndex, 0, 2),
			}
			byTable[key] = table
			order = append(order, key)
		}

		column := row.schema + "." + row.table + "." + row.name
		table.Columns = append(table.Columns, SnapshotColumn{
			Name: row.name, Type: row.dataType,
			PK: primaryKeys[column], NotNull: row.notNull, Unique: uniqueSingles[column],
			Increment: row.increment, DefaultValue: row.defaultValue,
		})
	}

	for _, index := range b.buildIndexes(constraintSize) {
		key := index.schema + "." + index.table
		if table, found := byTable[key]; found {
			table.Indexes = append(table.Indexes, index.SnapshotIndex)
		}
	}

	sort.Strings(order)
	tables := make([]SnapshotTable, 0, len(order))
	for _, key := range order {
		tables = append(tables, *byTable[key])
	}
	return tables
}

// placedIndex is an index with the table it belongs to, which the snapshot's own type does not
// carry because it is nested under one.
type placedIndex struct {
	SnapshotIndex
	schema, table string
}

// buildIndexes assembles the index rows, dropping the ones that say nothing new.
//
// **Every engine reports the index behind a constraint alongside the constraint itself**, so
// keeping both would draw the same thing twice — once as the column's `pk`/`unique` setting and
// again as an index block.
//
// But only when the column's setting **can** say it: a single-column key produces no block, because
// `pk` or `unique` on the column already carries it, while a composite one does produce one,
// because DBML has nowhere else to put a two-column key. Dropping every constraint-backed index
// regardless — which is what an earlier version of this did — loses every composite unique key in
// the diagram, silently.
func (b *snapshotBuilder) buildIndexes(constraintSize map[string]int) []placedIndex {
	type indexKey struct{ schema, table, name string }

	order := make([]indexKey, 0, 8)
	members := map[indexKey][]indexRow{}

	for _, row := range b.indexes {
		key := indexKey{row.schema, row.table, row.name}
		if _, seen := members[key]; !seen {
			order = append(order, key)
		}
		members[key] = append(members[key], row)
	}

	out := make([]placedIndex, 0, len(order))
	for _, key := range order {
		rows := members[key]
		if size, backs := constraintSize[key.schema+"."+key.table+"."+key.name]; backs && size <= 1 {
			continue
		}
		if len(rows) == 1 && (rows[0].primary || rows[0].unique) {
			// A single-column unique index says the same thing the column's own `unique` does.
			continue
		}

		sort.SliceStable(rows, func(i, j int) bool { return rows[i].position < rows[j].position })

		columns := make([]string, 0, len(rows))
		for _, row := range rows {
			columns = append(columns, row.column)
		}

		name := key.name
		out = append(out, placedIndex{
			Name: &name, Columns: columns,
			Unique: rows[0].unique, PK: rows[0].primary,
			schema: key.schema, table: key.table,
		})
	}
	return out
}

func (b *snapshotBuilder) buildRefs() []SnapshotRef {
	type refKey struct{ schema, table, constraint string }

	order := make([]refKey, 0, 8)
	members := map[refKey][]refRow{}

	for _, row := range b.refs {
		key := refKey{row.fromSchema, row.fromTable, row.constraint}
		if _, seen := members[key]; !seen {
			order = append(order, key)
		}
		members[key] = append(members[key], row)
	}

	refs := make([]SnapshotRef, 0, len(order))
	for _, key := range order {
		rows := members[key]
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].position < rows[j].position })

		from := make([]string, 0, len(rows))
		to := make([]string, 0, len(rows))
		for _, row := range rows {
			from = append(from, row.fromColumn)
			to = append(to, row.toColumn)
		}

		refs = append(refs, SnapshotRef{
			FromSchema: key.schema, FromTable: key.table, FromColumns: from,
			ToSchema: rows[0].toSchema, ToTable: rows[0].toTable, ToColumns: to,
			// `NO ACTION` is dropped: it is what every key that declares nothing reports, so
			// carrying it would write a rule the author never wrote.
			OnDelete: meaningfulAction(rows[0].onDelete),
			OnUpdate: meaningfulAction(rows[0].onUpdate),
		})
	}
	return refs
}

func (b *snapshotBuilder) buildEnums() []SnapshotEnum {
	type enumKey struct{ schema, name string }

	order := make([]enumKey, 0, 4)
	members := map[enumKey][]enumRow{}

	for _, row := range b.enums {
		key := enumKey{row.schema, row.name}
		if _, seen := members[key]; !seen {
			order = append(order, key)
		}
		members[key] = append(members[key], row)
	}

	enums := make([]SnapshotEnum, 0, len(order))
	for _, key := range order {
		rows := members[key]
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].position < rows[j].position })

		values := make([]string, 0, len(rows))
		for _, row := range rows {
			values = append(values, row.value)
		}
		enums = append(enums, SnapshotEnum{Schema: key.schema, Name: key.name, Values: values})
	}
	return enums
}

// meaningfulAction drops what every key reports when it declares nothing.
func meaningfulAction(action *string) *string {
	if action == nil {
		return nil
	}
	switch normalizeAction(*action) {
	case "", "NO ACTION":
		return nil
	default:
		normalized := normalizeAction(*action)
		return &normalized
	}
}

func normalizeAction(action string) string {
	switch action {
	case "a", "A":
		return "NO ACTION"
	case "r", "R":
		return "RESTRICT"
	case "c", "C":
		return "CASCADE"
	case "n", "N":
		return "SET NULL"
	case "d", "D":
		return "SET DEFAULT"
	default:
		return strings.ToUpper(strings.TrimSpace(action))
	}
}
