package codemap

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestApplyDDLReplaysCreateRenameAndDrop(t *testing.T) {
	tables := tableSet{}
	applyDDL(tables, "001.sql", "-- CREATE TABLE commented_out (id int);\nCREATE TABLE items (id int);\nCREATE TABLE IF NOT EXISTS \"carts_old\" (id int);\nCREATE TABLE legacy (id int);")
	applyDDL(tables, "002.sql", "ALTER TABLE carts_old RENAME TO carts;\nDROP TABLE IF EXISTS legacy;")

	assert.Equal(t, tableSet{
		"items": {File: "001.sql", Line: 2},
		"carts": {File: "002.sql", Line: 1},
	}, tables)
}

func TestClassifySQL(t *testing.T) {
	tables := tableSet{"items": {}, "carts": {}, "orders": {}}
	tests := []struct {
		name    string
		text    string
		want    []tableAccess
		dynamic bool
	}{
		{"a read and a join", "SELECT * FROM items JOIN carts ON carts.id = items.id",
			[]tableAccess{{"items", EdgeRead}, {"carts", EdgeRead}}, false},
		{"writes", "INSERT INTO orders (id) VALUES (?); UPDATE items SET n = 1; DELETE FROM carts",
			[]tableAccess{{"orders", EdgeWrite}, {"items", EdgeWrite}, {"carts", EdgeWrite}}, false},
		{"insert or replace", "INSERT OR REPLACE INTO items VALUES (1)", []tableAccess{{"items", EdgeWrite}}, false},
		{"a lower-case statement", "select id from items", []tableAccess{{"items", EdgeRead}}, false},
		{"prose is not a query", "read from items and carts", nil, false},
		{"a table name on its own is a mention", "items", []tableAccess{{"items", EdgeMention}}, false},
		{"a placeholder table is dynamic", "SELECT * FROM %s", nil, true},
		{"a concatenated table is dynamic", "DELETE FROM ", nil, true},
		{"an unknown table is ignored", "SELECT * FROM sqlite_master", nil, false},
		{"a subquery is not a table", "SELECT * FROM (SELECT 1)", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, dynamic := classifySQL(tt.text, tables)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.dynamic, dynamic)
		})
	}
}

func TestAddCreatedTablesKeepsTheReplayedPosition(t *testing.T) {
	tables := tableSet{"items": {File: "001.sql", Line: 2}}
	addCreatedTables(tables, "CREATE TABLE items (id int); CREATE TABLE prefs (k text)", Pos{File: "schema.go", Line: 9})

	assert.Equal(t, Pos{File: "001.sql", Line: 2}, tables["items"], "a migration's position wins")
	assert.Equal(t, Pos{File: "schema.go", Line: 9}, tables["prefs"])
}

func TestMayBeSQLFiltersBeforeAnythingIsKept(t *testing.T) {
	assert.True(t, mayBeSQL("SELECT id FROM items"))
	assert.False(t, mayBeSQL("SELECT 1"), "no table can follow")
	assert.True(t, mayBeSQL("items"))
	assert.False(t, mayBeSQL("Hello, world"))
	assert.False(t, mayBeSQL("ID"))
}
