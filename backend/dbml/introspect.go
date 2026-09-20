package dbml

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	// The four drivers, registered for their side effect. All free and permissively licensed:
	// pgx and go-mssqldb are MIT, the MySQL driver is MPL-2.0, and modernc's SQLite — which this
	// process already carries for its own database — is BSD-3.
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"
	_ "modernc.org/sqlite"
)

// Reaching a database and reading its schema (DBML-023, DBML-026).
//
// **Read-only, structurally.** Nothing here issues DDL or DML: a schema designer that could write
// to the database a person pointed it at is a different and much more dangerous tool. Every query
// below reads a catalogue.

// queryTimeout bounds each catalogue read, for the server that accepts the socket and then goes
// quiet. Thirty seconds is long enough for a large catalogue on a slow link and short enough that a
// hung server does not hold the panel.
const queryTimeout = 30 * time.Second

// ErrConnectionRefused marks a database that could not be reached or that refused the login.
//
// Told apart from every other failure because the next step differs: "the database said no" points
// at a port, a password or a server that is not running, while "the command blew up" points here.
// The sentinel goes on at the command boundary.
var ErrConnectionRefused = errors.New("the database refused the connection")

// introspector is one engine's five queries, declared here because this package is the consumer.
type introspector interface {
	// dsn builds the connection string. It is never logged and never returned in an error: it
	// holds the password.
	dsn(connection Connection, password string) (driverName, dsn string, err error)
	// read fills the builder from the engine's own catalogue.
	read(ctx context.Context, db *sql.DB, connection Connection, into *snapshotBuilder) error
}

func introspectorFor(driver string) (introspector, error) {
	switch driver {
	case DriverPostgres:
		return postgresIntrospector{}, nil
	case DriverSQLServer:
		return sqlServerIntrospector{}, nil
	case DriverMySQL:
		return mysqlIntrospector{}, nil
	case DriverSQLite:
		return sqliteIntrospector{}, nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownDriver, driver)
	}
}

// open dials the database (DBML-026).
//
// **Pooling is off on every engine.** For the servers it avoids holding a socket open to a database
// the user reads once; for SQLite it is a correctness rule rather than hygiene — a pooled
// connection keeps the file open after it is closed, and an open file cannot be moved, replaced or
// deleted, so reading a schema would leave the user's own database locked for as long as CodeFlow
// ran.
func open(ctx context.Context, connection Connection, password string) (*sql.DB, error) {
	engine, err := introspectorFor(connection.Driver)
	if err != nil {
		return nil, err
	}

	driverName, dsn, err := engine.dsn(connection, password)
	if err != nil {
		return nil, err
	}

	db, err := sql.Open(driverName, dsn)
	if err != nil {
		// The driver's own sentence only: several of them put the whole connection string in their
		// error, and that string holds the password.
		return nil, refused(err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)
	db.SetConnMaxLifetime(time.Minute)

	ping, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	if err := db.PingContext(ping); err != nil {
		_ = db.Close()
		return nil, refused(err)
	}
	return db, nil
}

// refused wraps a driver failure, keeping **only the driver's own sentence**.
//
// A connection string in an error reaches a toast, a log and a bug report, and it carries the
// password. The drivers that embed it are the reason this function exists rather than the error
// being returned as it came.
func refused(err error) error {
	return fmt.Errorf("%w: %s", ErrConnectionRefused, scrub(err.Error()))
}

// scrub removes anything that looks like a credential from a driver's sentence.
//
// Belt and braces over `refused`: the drivers are the ones that decide what goes in their error
// text, and a future version of any of them could start including a DSN where today it does not.
func scrub(message string) string {
	out := message

	// A URL-shaped DSN carries `user:password@host`.
	for _, scheme := range []string{"postgres://", "postgresql://", "mysql://", "sqlserver://"} {
		if index := strings.Index(out, scheme); index >= 0 {
			out = out[:index] + "(connection details omitted)"
		}
	}
	// A keyword-shaped one carries `password=…`.
	for _, keyword := range []string{"password=", "Password=", "pwd=", "Pwd="} {
		if index := strings.Index(out, keyword); index >= 0 {
			out = out[:index] + "(connection details omitted)"
		}
	}
	return strings.TrimSpace(out)
}

// TestConnection opens the connection and closes it.
func TestConnection(ctx context.Context, connection Connection, password string) error {
	db, err := open(ctx, connection, password)
	if err != nil {
		return err
	}
	return db.Close()
}

// Introspect reads a schema as structured data (DBML-025).
func Introspect(ctx context.Context, connection Connection, password string) (SchemaSnapshot, error) {
	engine, err := introspectorFor(connection.Driver)
	if err != nil {
		return SchemaSnapshot{}, err
	}

	db, err := open(ctx, connection, password)
	if err != nil {
		return SchemaSnapshot{}, err
	}
	defer func() { _ = db.Close() }()

	builder := &snapshotBuilder{}
	if err := engine.read(ctx, db, connection, builder); err != nil {
		return SchemaSnapshot{}, err
	}

	snapshot := builder.build()
	// Never nil: the renderer maps over all three, and an empty database is an ordinary answer.
	if snapshot.Tables == nil {
		snapshot.Tables = []SnapshotTable{}
	}
	if snapshot.Refs == nil {
		snapshot.Refs = []SnapshotRef{}
	}
	if snapshot.Enums == nil {
		snapshot.Enums = []SnapshotEnum{}
	}
	return snapshot, nil
}

// ---- shared helpers --------------------------------------------------------------------------------

// query runs one catalogue read under the timeout, wrapping the failure with what was being read.
func query(ctx context.Context, db *sql.DB, what, statement string, args ...any) (*sql.Rows, func(), error) {
	bounded, cancel := context.WithTimeout(ctx, queryTimeout)

	rows, err := db.QueryContext(bounded, statement, args...)
	if err != nil {
		cancel()
		return nil, func() {}, fmt.Errorf("read the %s: %w", what, err)
	}
	return rows, func() {
		_ = rows.Close()
		cancel()
	}, nil
}

func text(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

// nonEmpty drops a default that is only whitespace, which several engines report for a column that
// declares none.
func nonEmpty(value *string) *string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	return value
}

// hostPort renders an address, filling in a default port.
func hostPort(connection Connection, defaultPort int) string {
	host := "localhost"
	if connection.Host != nil && strings.TrimSpace(*connection.Host) != "" {
		host = strings.TrimSpace(*connection.Host)
	}

	port := defaultPort
	if connection.Port != nil && *connection.Port > 0 {
		port = int(*connection.Port)
	}
	return host + ":" + strconv.Itoa(port)
}

func stringOf(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

// userInfo builds the credential half of a URL-shaped DSN.
func userInfo(username *string, password string) *url.Userinfo {
	name := stringOf(username)
	if name == "" && password == "" {
		return nil
	}
	if password == "" {
		return url.User(name)
	}
	return url.UserPassword(name, password)
}
