// Package bun stands in for uptrace/bun's query builders.
package bun

type BaseModel struct{}

type DB struct{}

type SelectQuery struct{}

func (db *DB) NewSelect() *SelectQuery                    { return &SelectQuery{} }
func (q *SelectQuery) Model(model any) *SelectQuery       { return q }
func (q *SelectQuery) TableExpr(expr string) *SelectQuery { return q }

type InsertQuery struct{}

func (db *DB) NewInsert() *InsertQuery              { return &InsertQuery{} }
func (q *InsertQuery) Model(model any) *InsertQuery { return q }
