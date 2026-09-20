package dbml

import (
	"context"
	"errors"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/sentinel"
)

// The schema designer's commands.
//
// Eight of the ten need nothing but the database and the filesystem; the two that reach a real
// server register beside them and carry the drivers.

// Credentials is the credential store, in this feature's own terms.
//
// Asked for by connection id rather than by key, because the key format is a `VERBATIM` contract
// owned by the credential store (`SEC-014`) and this package has no business spelling it.
type Credentials interface {
	SetDBPassword(connectionID, password string) error
	DBPassword(connectionID string) (string, error)
	DeleteDBPassword(connectionID string) error
}

// Deps is what this feature needs from the composition root.
type Deps struct {
	Store *Store
	// Credentials may be nil only in a test that does not touch a password.
	Credentials Credentials
	// AI runs the assistant. Nil means the assistant refuses, which is the state an install with no
	// engine configured is already in.
	AI Reviewer
}

// Register adds the schema designer's ten commands.
func Register(r *bridge.Registry, deps Deps) {
	registerDocuments(r)
	registerLayout(r, deps)
	registerConnections(r, deps)
	registerAssistant(r, deps)
	registerIntrospection(r, deps)
}

// registerIntrospection adds the two commands that reach a real database.
//
// Both read the password inside this process and hand it straight to the driver; neither answers
// with anything derived from it. The `DB_CONNECTION_REFUSED: ` sentinel goes on **here**, at the
// command boundary, so the panel can show the driver's own diagnosis — a wrong port, a bad password,
// a server that is not running — instead of "could not import".
func registerIntrospection(r *bridge.Registry, deps Deps) {
	r.Add("dbml_test_connection", func(ctx context.Context, p bridge.Params) (any, error) {
		connection, password, err := deps.resolve(ctx, p)
		if err != nil {
			return nil, asCommandError(err)
		}
		return nil, asCommandError(TestConnection(ctx, connection, password))
	})

	r.Add("dbml_introspect_database", func(ctx context.Context, p bridge.Params) (any, error) {
		connection, password, err := deps.resolve(ctx, p)
		if err != nil {
			return nil, asCommandError(err)
		}
		snapshot, err := Introspect(ctx, connection, password)
		if err != nil {
			return nil, asCommandError(err)
		}
		return snapshot, nil
	})
}

// resolve reads the connection and its password. The password never leaves this process.
func (d Deps) resolve(ctx context.Context, p bridge.Params) (Connection, string, error) {
	id, err := bridge.Arg[string](p, "connectionId")
	if err != nil {
		return Connection{}, "", err
	}

	connection, err := d.Store.GetConnection(ctx, id)
	if err != nil {
		return Connection{}, "", err
	}
	if d.Credentials == nil {
		// A database that takes no password still works; one that needs it will say so itself.
		return connection, "", nil
	}

	password, err := d.Credentials.DBPassword(id)
	if err != nil {
		return Connection{}, "", err
	}
	return connection, password, nil
}

// asCommandError puts the refused-database sentinel at position 0, and nowhere else (XLANG-018).
//
// Applied in a handler rather than where the connection failed: inside the process the failure is a
// typed error callers match with `errors.Is`, and it is only on the way out that the renderer needs
// a prefix it can find.
func asCommandError(err error) error {
	if err == nil || !errors.Is(err, ErrConnectionRefused) {
		return err
	}

	// The sentinel, then the driver's own sentence — which `refused` has already stripped of
	// anything that could carry the password.
	detail := strings.TrimPrefix(err.Error(), ErrConnectionRefused.Error()+": ")
	return errors.New(sentinel.DBConnectionRefused + detail) //nolint:staticcheck // ST1005: VERBATIM sentinel
}

// registerDocuments adds the one command that walks a folder.
//
// It takes no project and no repository: the schema designer works in a plain folder, which is the
// whole reason it does not reuse the git-aware walker.
func registerDocuments(r *bridge.Registry) {
	r.Add("dbml_list_documents", func(ctx context.Context, p bridge.Params) (any, error) {
		rootPath, err := bridge.Arg[string](p, "rootPath")
		if err != nil {
			return nil, err
		}
		return ListDocuments(ctx, rootPath)
	})
}

func registerLayout(r *bridge.Registry, deps Deps) {
	r.Add("dbml_load_layout", func(ctx context.Context, p bridge.Params) (any, error) {
		projectID, relPath, err := documentTarget(p)
		if err != nil {
			return nil, err
		}
		return deps.Store.LoadLayout(ctx, projectID, relPath)
	})

	r.Add("dbml_save_positions", func(ctx context.Context, p bridge.Params) (any, error) {
		projectID, relPath, err := documentTarget(p)
		if err != nil {
			return nil, err
		}
		positions, err := bridge.Arg[[]TablePosition](p, "positions")
		if err != nil {
			return nil, err
		}
		return nil, deps.Store.SavePositions(ctx, projectID, relPath, positions)
	})

	r.Add("dbml_clear_layout", func(ctx context.Context, p bridge.Params) (any, error) {
		projectID, relPath, err := documentTarget(p)
		if err != nil {
			return nil, err
		}
		return nil, deps.Store.ClearLayout(ctx, projectID, relPath)
	})
}

func registerConnections(r *bridge.Registry, deps Deps) {
	// Never carries a password: there is no column for one and no field on the type.
	r.Add("dbml_list_connections", func(ctx context.Context, _ bridge.Params) (any, error) {
		return deps.Store.ListConnections(ctx)
	})

	r.Add("dbml_save_connection", func(ctx context.Context, p bridge.Params) (any, error) {
		input, err := bridge.Arg[NewConnection](p, "connection")
		if err != nil {
			return nil, err
		}
		return deps.SaveConnection(ctx, input)
	})

	r.Add("dbml_delete_connection", func(ctx context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "connectionId")
		if err != nil {
			return nil, err
		}
		return nil, deps.DeleteConnection(ctx, id)
	})
}

// SaveConnection writes the row and then the credential, in that order (DBML-023, DBML-024).
//
// **The row first**, so a failed insert cannot file a secret under an id that does not exist — a
// secret nothing would ever read and nothing would ever clean up.
//
// **A blank password means "leave what is stored"**, never "clear it". The renderer cannot show
// what is held, so an untouched field arrives empty on every edit; treating that as a clear would
// wipe the secret each time somebody fixed a typo in the port number.
func (d Deps) SaveConnection(ctx context.Context, input NewConnection) (Connection, error) {
	stored, err := d.Store.UpsertConnection(ctx, input)
	if err != nil {
		return Connection{}, err
	}

	if input.Password == nil || strings.TrimSpace(*input.Password) == "" {
		return stored, nil
	}
	if d.Credentials == nil {
		return stored, errNoCredentialStore
	}
	if err := d.Credentials.SetDBPassword(stored.ID, *input.Password); err != nil {
		// Reported rather than swallowed: the row is saved and the password is not, and a user who
		// was not told would find out at the next connection attempt instead.
		return stored, err
	}
	return stored, nil
}

// DeleteConnection removes the credential and then the row, in that order (DBML-023).
//
// **The credential first**: a row that outlives its secret asks for the password again, which is
// recoverable, while a secret that outlives its row is one nothing will ever read or clean up.
func (d Deps) DeleteConnection(ctx context.Context, id string) error {
	if d.Credentials != nil {
		if err := d.Credentials.DeleteDBPassword(id); err != nil {
			return err
		}
	}
	return d.Store.DeleteConnection(ctx, id)
}

var errNoCredentialStore = errors.New("the credential store is unavailable, so the password was not saved") //nolint:staticcheck // ST1005: VERBATIM

func registerAssistant(r *bridge.Registry, deps Deps) {
	r.Add("dbml_assist", func(ctx context.Context, p bridge.Params) (any, error) {
		mode, err := bridge.Arg[string](p, "mode")
		if err != nil {
			return nil, err
		}
		document, err := bridge.Arg[string](p, "dbml")
		if err != nil {
			return nil, err
		}
		instruction, err := bridge.OptionalArg[string](p, "instruction")
		if err != nil {
			return nil, err
		}
		runID, err := bridge.OptionalArg[string](p, "runId")
		if err != nil {
			return nil, err
		}

		if deps.AI == nil {
			return nil, errNoEngine
		}
		return Assist(ctx, deps.AI, AssistRequest{
			Mode: mode, DBML: document,
			Instruction: optional(instruction), RunID: optional(runID),
		})
	})
}

var errNoEngine = errors.New("no AI engine is configured") //nolint:staticcheck // ST1005: VERBATIM

// documentTarget reads the two parameters every layout command takes.
func documentTarget(p bridge.Params) (projectID, relPath string, err error) {
	if projectID, err = bridge.Arg[string](p, "projectId"); err != nil {
		return "", "", err
	}
	if relPath, err = bridge.Arg[string](p, "relPath"); err != nil {
		return "", "", err
	}
	return projectID, relPath, nil
}

func optional(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
