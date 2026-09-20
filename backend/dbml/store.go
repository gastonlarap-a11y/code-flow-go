package dbml

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/gastonlarap-a11y/code-flow/backend/storage"
)

// The two tables this feature owns rows in: `dbml_layouts` and `db_connections` (DBML-005,
// DBML-023, DBML-024).

// maxPositions bounds one save. A document with more tables than this is not one a person is
// dragging cards around in.
const maxPositions = 2000

// TablePosition is where a person dragged one table.
//
// snake_case in both directions: these are rows sent back, which is the exception the API client's
// own commands document.
type TablePosition struct {
	TableKey string  `json:"table_key"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
}

// The four engines a schema can be read out of (DBML-023).
const (
	DriverPostgres  = "postgres"
	DriverSQLServer = "sqlserver"
	DriverMySQL     = "mysql"
	DriverSQLite    = "sqlite"
)

// Connection is a saved database, as it crosses the boundary in both directions.
//
// **It has no password field**, and that is the enforcement rather than a convention: the secret
// lives in the OS credential store and is read only inside this process, so `DBML-024` is a property
// of the type instead of something every handler has to remember.
type Connection struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Driver   string  `json:"driver"`
	Host     *string `json:"host"`
	Port     *int64  `json:"port"`
	Database *string `json:"database"`
	Username *string `json:"username"`
	// FilePath is SQLite's, which is a file rather than a server.
	FilePath  *string `json:"file_path"`
	UseTLS    bool    `json:"use_tls"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
}

// NewConnection is what the renderer sends to create or update one. This is the **only** direction
// a password travels.
type NewConnection struct {
	// ID is null when creating.
	ID       *string `json:"id"`
	Name     string  `json:"name"`
	Driver   string  `json:"driver"`
	Host     *string `json:"host"`
	Port     *int64  `json:"port"`
	Database *string `json:"database"`
	Username *string `json:"username"`
	FilePath *string `json:"file_path"`
	UseTLS   bool    `json:"use_tls"`
	// Password blank means **leave what is stored**, never "clear it" — see SaveConnection.
	Password *string `json:"password"`
}

// ErrNotFound is what a command naming a connection that is not there answers.
var ErrNotFound = errors.New("connection not found")

// ErrUnknownDriver refuses an engine this cannot read.
//
// An error rather than a fallback, unlike the AI engine catalogue which defaults to Claude: a schema
// read with the wrong engine is not a degraded answer, it is a wrong one.
var ErrUnknownDriver = errors.New("unknown database driver")

// Store is this feature's queries.
type Store struct {
	db    *storage.DB
	clock storage.Clock
}

// NewStore wires the store.
func NewStore(db *storage.DB, clock storage.Clock) *Store {
	if clock == nil {
		clock = storage.SystemClock{}
	}
	return &Store{db: db, clock: clock}
}

// ---- layouts (DBML-005) --------------------------------------------------------------------------

// LoadLayout reads the positions a person gave one document's tables.
//
// Only positions somebody set are stored; every other table is placed by the auto-layout on each
// render, which is why an empty answer is the ordinary one for a document nobody has arranged.
func (s *Store) LoadLayout(ctx context.Context, projectID, relPath string) ([]TablePosition, error) {
	out := make([]TablePosition, 0, 16)

	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		rows, err := db.QueryContext(ctx, `
			SELECT table_key, x, y FROM dbml_layouts
			 WHERE project_id = ? AND rel_path = ?
			 ORDER BY table_key`, projectID, relPath)
		if err != nil {
			return fmt.Errorf("read the layout: %w", err)
		}
		defer func() { _ = rows.Close() }()

		for rows.Next() {
			var position TablePosition
			if err := rows.Scan(&position.TableKey, &position.X, &position.Y); err != nil {
				return fmt.Errorf("scan a position: %w", err)
			}
			out = append(out, position)
		}
		return rows.Err()
	})
	return out, err
}

// SavePositions stores positions, moving any table that already had one (DBML-005).
//
// **One multi-row `INSERT … ON CONFLICT`**, which is what makes it atomic without a transaction: a
// save that fails leaves the previous layout intact rather than half of the new one. A key repeated
// within one save keeps its last position, because that is what the last `DO UPDATE` writes.
func (s *Store) SavePositions(ctx context.Context, projectID, relPath string, positions []TablePosition) error {
	if len(positions) == 0 {
		// Writing nothing is the right answer, not an error: a document with no dragged cards saves
		// on every close.
		return nil
	}
	if len(positions) > maxPositions {
		return fmt.Errorf("more than %d positions in one save", maxPositions) //nolint:err113 // names the bound
	}

	// Validated before anything is written: a blank key would land as a row nothing could ever read
	// back, and refusing the whole save is what keeps the layout consistent with what the user sees.
	for _, position := range positions {
		if strings.TrimSpace(position.TableKey) == "" {
			return errBlankTableKey
		}
	}

	query := &strings.Builder{}
	query.WriteString(`INSERT INTO dbml_layouts (id, project_id, rel_path, table_key, x, y, updated_at) VALUES `)

	now := s.clock.Now()
	args := make([]any, 0, len(positions)*7)

	for i, position := range positions {
		if i > 0 {
			query.WriteString(", ")
		}
		query.WriteString("(?, ?, ?, ?, ?, ?, ?)")
		args = append(args, uuid.NewString(), projectID, relPath, position.TableKey,
			position.X, position.Y, now)
	}

	query.WriteString(`
		ON CONFLICT(project_id, rel_path, table_key) DO UPDATE SET
		    x = excluded.x, y = excluded.y, updated_at = excluded.updated_at`)

	return s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, query.String(), args...); err != nil {
			return fmt.Errorf("save the layout: %w", err)
		}
		return nil
	})
}

var errBlankTableKey = errors.New("a position is missing its table_key") //nolint:staticcheck // ST1005: VERBATIM

// ClearLayout forgets one document's positions, so the auto-layout places all of it again.
//
// Rows are **not** pruned anywhere else: renaming a table and undoing the rename must not cost its
// position, and the layout simply ignores a key it has no table for.
func (s *Store) ClearLayout(ctx context.Context, projectID, relPath string) error {
	return s.write(ctx,
		`DELETE FROM dbml_layouts WHERE project_id = ? AND rel_path = ?`, projectID, relPath)
}

// ---- connections (DBML-023) -----------------------------------------------------------------------

// ListConnections reads the saved databases. Never carries a password — there is no column for one.
func (s *Store) ListConnections(ctx context.Context) ([]Connection, error) {
	out := make([]Connection, 0, 8)

	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		rows, err := db.QueryContext(ctx, `
			SELECT id, name, driver, host, port, database, username, file_path, use_tls,
			       created_at, updated_at
			  FROM db_connections
			 ORDER BY name, created_at`)
		if err != nil {
			return fmt.Errorf("list connections: %w", err)
		}
		defer func() { _ = rows.Close() }()

		for rows.Next() {
			connection, err := scanConnection(rows.Scan)
			if err != nil {
				return err
			}
			out = append(out, connection)
		}
		return rows.Err()
	})
	return out, err
}

// GetConnection reads one by id.
func (s *Store) GetConnection(ctx context.Context, id string) (Connection, error) {
	var connection Connection

	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		row := db.QueryRowContext(ctx, `
			SELECT id, name, driver, host, port, database, username, file_path, use_tls,
			       created_at, updated_at
			  FROM db_connections WHERE id = ?`, id)

		found, err := scanConnection(row.Scan)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		connection = found
		return nil
	})
	return connection, err
}

func scanConnection(scan func(...any) error) (Connection, error) {
	var c Connection
	err := scan(&c.ID, &c.Name, &c.Driver, &c.Host, &c.Port, &c.Database, &c.Username,
		&c.FilePath, &c.UseTLS, &c.CreatedAt, &c.UpdatedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Connection{}, fmt.Errorf("scan a connection: %w", err)
	}
	return c, err
}

// UpsertConnection writes the row, and answers it as stored.
//
// The **row goes before the credential** at the call site above this one, so a failed insert cannot
// file a secret under an id that does not exist.
func (s *Store) UpsertConnection(ctx context.Context, input NewConnection) (Connection, error) {
	if !KnownDriver(input.Driver) {
		return Connection{}, fmt.Errorf("%w: %s", ErrUnknownDriver, input.Driver)
	}

	now := s.clock.Now()
	connection := Connection{
		ID: uuid.NewString(), Name: input.Name, Driver: input.Driver,
		Host: input.Host, Port: input.Port, Database: input.Database, Username: input.Username,
		FilePath: input.FilePath, UseTLS: input.UseTLS,
		CreatedAt: now, UpdatedAt: now,
	}
	if input.ID != nil && strings.TrimSpace(*input.ID) != "" {
		connection.ID = *input.ID
	}

	err := s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO db_connections (id, name, driver, host, port, database, username,
			                            file_path, use_tls, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
			    name = excluded.name, driver = excluded.driver, host = excluded.host,
			    port = excluded.port, database = excluded.database, username = excluded.username,
			    file_path = excluded.file_path, use_tls = excluded.use_tls,
			    updated_at = excluded.updated_at`,
			connection.ID, connection.Name, connection.Driver, connection.Host, connection.Port,
			connection.Database, connection.Username, connection.FilePath, connection.UseTLS,
			now, now)
		if err != nil {
			return fmt.Errorf("save the connection: %w", err)
		}
		return nil
	})
	if err != nil {
		return Connection{}, err
	}

	// An update keeps the creation time the row already had; re-reading is cheaper than a second
	// statement that would have to know whether this was an insert.
	stored, err := s.GetConnection(ctx, connection.ID)
	if err != nil {
		return Connection{}, err
	}
	return stored, nil
}

// DeleteConnection removes the row.
//
// The **credential goes first** at the call site above this one: a row that outlives its secret asks
// for the password again, while a secret that outlives its row is one nothing will ever read or
// clean up.
func (s *Store) DeleteConnection(ctx context.Context, id string) error {
	return s.write(ctx, `DELETE FROM db_connections WHERE id = ?`, id)
}

// KnownDriver reports whether an engine can be read at all.
func KnownDriver(driver string) bool {
	switch driver {
	case DriverPostgres, DriverSQLServer, DriverMySQL, DriverSQLite:
		return true
	default:
		return false
	}
}

func (s *Store) write(ctx context.Context, query string, args ...any) error {
	return s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("write: %w", err)
		}
		return nil
	})
}
