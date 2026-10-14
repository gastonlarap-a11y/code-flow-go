// Package gorm stands in for gorm.io/gorm.
package gorm

type DB struct{}

func (db *DB) Find(dest any) *DB    { return db }
func (db *DB) Create(value any) *DB { return db }
