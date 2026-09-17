// Package storage owns the one SQLite file CodeFlow ever opens.
//
// Two decisions from 2.x are carried over unchanged because the whole application is built on
// them (STORE-001):
//
//   - **One physical connection for the life of the process**, serialised behind a mutex. Not a
//     pool. SQLite's `foreign_keys` pragma is per connection, and several behaviours the
//     specification pins — cascade deletes, `AMBIGUOUS-WS-a` — depend on it being on for every
//     statement. A pool would silently hand out connections where it was off.
//   - **Migrations have no version table.** Every step re-derives "did I already run?" from the
//     live schema, which is what makes running the whole procedure on every launch safe
//     (STORE-002). See migrations.go.
//
// Feature packages do not import this to build their own queries out of raw strings; each one
// owns a store that takes a *DB. What they share is the transaction discipline below.
package storage

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"sync"
	"time"

	// modernc.org/sqlite is a pure-Go translation of SQLite: no cgo, which is what lets the
	// Windows build cross-compile from any machine. The alternative (mattn/go-sqlite3) would
	// require a C toolchain per target.
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// Schema is the DDL batch, exported so a test can compare it against a real database.
func Schema() string { return schemaSQL }

// TimestampLayout is how every stored timestamp is written: UTC, seven fractional digits, an
// explicit `+00:00` offset and never a `Z`.
//
// It is `Storage/Clock.cs`'s format, and it is load-bearing rather than cosmetic. Timestamps are
// stored as TEXT and **compared and ordered as strings** (STORE query semantics), so a row written
// with a different number of fractional digits, or with `Z` instead of `+00:00`, sorts into the
// wrong place against every row 2.x wrote. There is no second chance to notice: the list just
// comes back in a strange order.
const TimestampLayout = "2006-01-02T15:04:05.0000000+00:00"

// Clock produces the timestamps rows are written with. An interface with one method so a test can
// pin the value instead of matching a pattern.
type Clock interface {
	Now() string
}

// SystemClock is the real one.
type SystemClock struct{}

// Now implements Clock.
func (SystemClock) Now() string { return time.Now().UTC().Format(TimestampLayout) }

// FixedClock returns the same instant every time. For tests.
type FixedClock struct{ At time.Time }

// Now implements Clock.
func (c FixedClock) Now() string { return c.At.UTC().Format(TimestampLayout) }

// DB is the application's storage handle.
//
// Read and Write both serialise through the same mutex — there is no reader/writer distinction,
// exactly as in 2.x, where every db function was called with one mutex held for the duration of a
// command. The pair of method names exists to say what a caller intends, not to allow concurrency:
// a Read that quietly writes is a bug this makes visible in review.
type DB struct {
	mu sync.Mutex
	db *sql.DB
}

// Open opens (creating if absent) the database at path, applies the pragmas and runs the
// migrations. It is the "storage" start-up stage.
func Open(ctx context.Context, path string) (*DB, error) {
	// Pragmas travel in the DSN because they must be applied to the connection itself, and with
	// one connection there is no second one to forget them. journal_mode=WAL is persistent (a
	// property of the file); foreign_keys and synchronous are per connection.
	//
	// synchronous=NORMAL is SQLite's recommended pairing with WAL: durability differs from the
	// FULL default only on power loss, never on an application crash.
	dsn := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)",
		path,
	)

	handle, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}

	// The three settings that make "one connection" true rather than aspirational. Without
	// MaxOpenConns(1), database/sql opens more on demand and the per-connection pragmas above
	// apply to whichever one happened to be used.
	handle.SetMaxOpenConns(1)
	handle.SetMaxIdleConns(1)
	handle.SetConnMaxLifetime(0)

	if err := handle.PingContext(ctx); err != nil {
		_ = handle.Close() // the handle is unusable; the open error is what the caller needs
		return nil, fmt.Errorf("connect to %s: %w", path, err)
	}

	db := &DB{db: handle}
	if err := Migrate(ctx, db); err != nil {
		_ = handle.Close()
		return nil, fmt.Errorf("migrate %s: %w", path, err)
	}
	return db, nil
}

// Close releases the connection. Called from the application's shutdown hook.
func (d *DB) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.db == nil {
		return nil
	}
	err := d.db.Close()
	d.db = nil
	return err
}

// Read runs fn against the database. Serialised with every other access.
func (d *DB) Read(ctx context.Context, fn func(context.Context, *sql.DB) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.db == nil {
		return ErrClosed
	}
	return fn(ctx, d.db)
}

// Write runs fn inside a transaction, committing on success and rolling back on any error or
// panic.
//
// A panic must roll back rather than leave the single connection inside an open transaction:
// bridge.Invoke recovers panics, so the process survives and the *next* command would otherwise
// find the connection in a state nothing ever leaves.
func (d *DB) Write(ctx context.Context, fn func(context.Context, *sql.Tx) error) (err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.db == nil {
		return ErrClosed
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			// Ignored: the panic is the failure being reported, and a rollback error on the way
			// out would replace it with something less useful.
			_ = tx.Rollback()
			panic(p)
		}
	}()

	if err := fn(ctx, tx); err != nil {
		// Ignored for the same reason: fn's error is what the caller needs to see.
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// ErrClosed is returned once Close has run. It is a real case rather than a defensive one: an AI
// run or a stream connector can outlive the window and reach storage during shutdown.
var ErrClosed = fmt.Errorf("the database is closed")

// NullString converts an optional value to what database/sql wants for a nullable column. It
// replaces the C# DBNull helper, and exists because passing a nil *string directly stores the
// string "<nil>" in some drivers rather than NULL.
func NullString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

// NullInt64 is NullString's counterpart for nullable integers.
func NullInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}
