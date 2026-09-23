package dbml_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/ai"
	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/dbml"
)

// ---- the fakes ---------------------------------------------------------------------------------

// fakeCredentials records the order the store was touched in, which is what two of these rules are
// about.
type fakeCredentials struct {
	secrets map[string]string
	calls   []string
	setErr  error
}

func newFakeCredentials() *fakeCredentials {
	return &fakeCredentials{secrets: map[string]string{}}
}

func (c *fakeCredentials) SetDBPassword(connectionID, password string) error {
	c.calls = append(c.calls, "set:"+connectionID)
	if c.setErr != nil {
		return c.setErr
	}
	c.secrets[connectionID] = password
	return nil
}

func (c *fakeCredentials) DBPassword(connectionID string) (string, error) {
	c.calls = append(c.calls, "get:"+connectionID)
	return c.secrets[connectionID], nil
}

func (c *fakeCredentials) DeleteDBPassword(connectionID string) error {
	c.calls = append(c.calls, "delete:"+connectionID)
	delete(c.secrets, connectionID)
	return nil
}

// fakeEngine records what the assistant asked for.
type fakeEngine struct {
	invocation *ai.Invocation
	task       ai.Task
	answer     string
	err        error
}

func (e *fakeEngine) Invoke(_ context.Context, _ string, task ai.Task, inv ai.Invocation, _ bool) (ai.Result, error) {
	e.invocation, e.task = &inv, task
	return ai.Result{Text: e.answer}, e.err
}

func registryFor(t *testing.T, deps dbml.Deps) *bridge.Registry {
	t.Helper()

	registry := bridge.NewRegistry()
	dbml.Register(registry, deps)
	registry.Seal()
	return registry
}

func invoke(t *testing.T, registry *bridge.Registry, name string, params map[string]any) (any, error) {
	t.Helper()

	handler, found := registry.Lookup(name)
	require.True(t, found, "%s is not registered", name)

	raw, err := json.Marshal(params)
	require.NoError(t, err)
	return handler(t.Context(), bridge.NewParams(raw))
}

// ---- the command surface -------------------------------------------------------------------------

func TestTheTenCommandsAreRegistered(t *testing.T) {
	registry := registryFor(t, dbml.Deps{})

	for _, name := range []string{
		"dbml_list_documents", "dbml_load_layout", "dbml_save_positions", "dbml_clear_layout",
		"dbml_list_connections", "dbml_save_connection", "dbml_delete_connection", "dbml_assist",
		"dbml_test_connection", "dbml_introspect_database",
	} {
		_, registered := registry.Lookup(name)
		assert.True(t, registered, "%s", name)
	}
	assert.Equal(t, 10, registry.Len())
}

// ---- the credential ordering (DBML-023, DBML-024) ---------------------------------------------

// The **row goes first**, so a failed insert cannot file a secret under an id that does not exist.
func TestSavingWritesTheRowBeforeTheCredential(t *testing.T) {
	store, _ := newStore(t)
	credentials := newFakeCredentials()

	saved, err := dbml.Deps{Store: store, Credentials: credentials}.SaveConnection(t.Context(),
		dbml.NewConnection{
			Name: "staging", Driver: dbml.DriverPostgres, Host: new("db.test"),
			Password: new("la-clave"),
		})
	require.NoError(t, err)

	// The id the credential was filed under is the one the row got, which is only knowable after
	// the row exists.
	require.Equal(t, []string{"set:" + saved.ID}, credentials.calls)
	assert.Equal(t, "la-clave", credentials.secrets[saved.ID])
}

func TestAFailedInsertFilesNoSecret(t *testing.T) {
	store, _ := newStore(t)
	credentials := newFakeCredentials()

	_, err := dbml.Deps{Store: store, Credentials: credentials}.SaveConnection(t.Context(),
		dbml.NewConnection{Name: "raro", Driver: "oracle", Password: new("la-clave")})
	require.ErrorIs(t, err, dbml.ErrUnknownDriver)

	assert.Empty(t, credentials.calls, "the credential store was never reached")
}

// **A blank password means "leave what is stored"**, never "clear it": the renderer cannot show
// what is held, so an untouched field arrives empty on every edit, and treating that as a clear
// would wipe the secret each time somebody fixed a typo in the port.
func TestABlankPasswordLeavesTheStoredOneAlone(t *testing.T) {
	store, _ := newStore(t)
	credentials := newFakeCredentials()
	deps := dbml.Deps{Store: store, Credentials: credentials}

	saved, err := deps.SaveConnection(t.Context(), dbml.NewConnection{
		Name: "staging", Driver: dbml.DriverPostgres, Password: new("la-clave"),
	})
	require.NoError(t, err)

	for _, password := range []*string{nil, new(""), new("   ")} {
		credentials.calls = nil

		_, err := deps.SaveConnection(t.Context(), dbml.NewConnection{
			ID: &saved.ID, Name: "staging", Driver: dbml.DriverPostgres,
			Port: new(int64(5433)), Password: password,
		})
		require.NoError(t, err)

		assert.Empty(t, credentials.calls, "the credential store was not touched")
		assert.Equal(t, "la-clave", credentials.secrets[saved.ID], "the secret survived the edit")
	}
}

func TestANewPasswordReplacesTheStoredOne(t *testing.T) {
	store, _ := newStore(t)
	credentials := newFakeCredentials()
	deps := dbml.Deps{Store: store, Credentials: credentials}

	saved, err := deps.SaveConnection(t.Context(), dbml.NewConnection{
		Name: "staging", Driver: dbml.DriverPostgres, Password: new("vieja"),
	})
	require.NoError(t, err)

	_, err = deps.SaveConnection(t.Context(), dbml.NewConnection{
		ID: &saved.ID, Name: "staging", Driver: dbml.DriverPostgres, Password: new("nueva"),
	})
	require.NoError(t, err)
	assert.Equal(t, "nueva", credentials.secrets[saved.ID])
}

// A credential the store refused is **reported**: the row is saved and the password is not, and a
// user who was not told would find out at the next connection attempt instead.
func TestAFailureToStoreThePasswordIsReported(t *testing.T) {
	store, _ := newStore(t)
	credentials := newFakeCredentials()
	credentials.setErr = errors.New("the keychain is locked")

	saved, err := dbml.Deps{Store: store, Credentials: credentials}.SaveConnection(t.Context(),
		dbml.NewConnection{Name: "staging", Driver: dbml.DriverPostgres, Password: new("x")})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "keychain is locked")
	// And the row is still there, carrying the id the user can retry under.
	assert.NotEmpty(t, saved.ID)
}

// The **credential goes first**: a row that outlives its secret asks for the password again, which
// is recoverable, while a secret that outlives its row is one nothing will ever read or clean up.
func TestDeletingRemovesTheCredentialBeforeTheRow(t *testing.T) {
	store, _ := newStore(t)
	credentials := newFakeCredentials()
	deps := dbml.Deps{Store: store, Credentials: credentials}

	saved, err := deps.SaveConnection(t.Context(), dbml.NewConnection{
		Name: "staging", Driver: dbml.DriverPostgres, Password: new("la-clave"),
	})
	require.NoError(t, err)

	credentials.calls = nil
	require.NoError(t, deps.DeleteConnection(t.Context(), saved.ID))

	require.Equal(t, []string{"delete:" + saved.ID}, credentials.calls)
	assert.NotContains(t, credentials.secrets, saved.ID)

	listed, err := store.ListConnections(t.Context())
	require.NoError(t, err)
	assert.Empty(t, listed)
}

// ---- the assistant (DBML-016) --------------------------------------------------------------------

func TestTheModeSelectsThePromptAndNothingElse(t *testing.T) {
	prompts := map[string]string{}

	for _, mode := range []string{dbml.ModeEdit, dbml.ModeReview, dbml.ModeExplain} {
		t.Run(mode, func(t *testing.T) {
			engine := &fakeEngine{answer: "la respuesta"}

			_, err := dbml.Assist(t.Context(), engine, dbml.AssistRequest{
				Mode: mode, DBML: "Table t { id int }", Instruction: "añade una tabla",
			})
			require.NoError(t, err)
			require.NotNil(t, engine.invocation)

			// Its own routing key, so a schema can be pointed at a different model than a code
			// review.
			assert.Equal(t, ai.TaskDBML, engine.task)
			// The ask on argv, the schema on stdin.
			assert.Equal(t, "añade una tabla", engine.invocation.Prompt)
			assert.Equal(t, "Table t { id int }", engine.invocation.StdinContent)
			// **The empty list**, which the engines read as "no tools at all": the model is handed
			// the whole document and asked about that document.
			assert.NotNil(t, engine.invocation.AllowedTools)
			assert.Empty(t, engine.invocation.AllowedTools)
			assert.True(t, engine.invocation.ReadOnly)

			prompts[mode] = engine.invocation.SystemPrompt
			assert.NotEmpty(t, prompts[mode])
		})
	}

	assert.NotEqual(t, prompts[dbml.ModeEdit], prompts[dbml.ModeReview])
	assert.NotEqual(t, prompts[dbml.ModeReview], prompts[dbml.ModeExplain])
}

// Only `edit`'s reply is unfenced — stripping a review's first fenced block would eat a finding.
func TestOnlyTheEditReplyIsUnfenced(t *testing.T) {
	fenced := "```dbml\nTable t { id int }\n```"

	edited, err := dbml.Assist(t.Context(), &fakeEngine{answer: fenced}, dbml.AssistRequest{
		Mode: dbml.ModeEdit, DBML: "Table t { }", Instruction: "algo",
	})
	require.NoError(t, err)
	assert.Equal(t, "Table t { id int }", edited)

	reviewed, err := dbml.Assist(t.Context(), &fakeEngine{answer: fenced}, dbml.AssistRequest{
		Mode: dbml.ModeReview, DBML: "Table t { }",
	})
	require.NoError(t, err)
	assert.Equal(t, fenced, reviewed, "the fence is part of the finding")
}

func TestTheAssistantRefusesWhatItCannotAnswer(t *testing.T) {
	engine := &fakeEngine{answer: "no debería llegar"}

	t.Run("an empty document", func(t *testing.T) {
		_, err := dbml.Assist(t.Context(), engine, dbml.AssistRequest{
			Mode: dbml.ModeReview, DBML: "   ",
		})
		assert.ErrorIs(t, err, dbml.ErrEmptySchema)
	})

	t.Run("an edit with no instruction", func(t *testing.T) {
		// Applying nothing has no meaning; the other two stand on their own.
		_, err := dbml.Assist(t.Context(), engine, dbml.AssistRequest{
			Mode: dbml.ModeEdit, DBML: "Table t { }", Instruction: "  ",
		})
		assert.ErrorIs(t, err, dbml.ErrNoInstruction)
	})

	t.Run("review and explain need no instruction", func(t *testing.T) {
		for _, mode := range []string{dbml.ModeReview, dbml.ModeExplain} {
			_, err := dbml.Assist(t.Context(), engine, dbml.AssistRequest{
				Mode: mode, DBML: "Table t { }",
			})
			assert.NoError(t, err, mode)
		}
	})

	t.Run("a document past the cap is refused rather than truncated", func(t *testing.T) {
		// `edit` is asked to return the **whole** document, so a dropped tail would come back as a
		// proposal that deletes every table past the cut.
		_, err := dbml.Assist(t.Context(), engine, dbml.AssistRequest{
			Mode: dbml.ModeEdit, DBML: strings.Repeat("a", 60_001), Instruction: "algo",
		})
		assert.ErrorIs(t, err, dbml.ErrSchemaTooLarge)
	})

	t.Run("an unknown mode names itself", func(t *testing.T) {
		// Which is what a renderer/backend drift looks like from a log.
		_, err := dbml.Assist(t.Context(), engine, dbml.AssistRequest{
			Mode: "rewrite", DBML: "Table t { }",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"rewrite"`)
	})
}

func TestALongInstructionIsCappedRatherThanRefused(t *testing.T) {
	engine := &fakeEngine{answer: "ok"}

	_, err := dbml.Assist(t.Context(), engine, dbml.AssistRequest{
		Mode: dbml.ModeEdit, DBML: "Table t { }",
		Instruction: strings.Repeat("ñ", 5_000),
	})
	require.NoError(t, err)

	// Capped by Unicode scalar, so a cut never lands mid-character.
	require.NotNil(t, engine.invocation)
	assert.Len(t, []rune(engine.invocation.Prompt), 4_000)
}

// ---- through the bridge --------------------------------------------------------------------------

func TestTheLayoutCommandsCrossInTheRenderersShape(t *testing.T) {
	store, _ := newStore(t)
	registry := registryFor(t, dbml.Deps{Store: store})

	_, err := invoke(t, registry, "dbml_save_positions", map[string]any{
		"projectId": "p1", "relPath": "a.dbml",
		"positions": []map[string]any{{"table_key": "public.orders", "x": 10, "y": 20}},
	})
	require.NoError(t, err)

	answer, err := invoke(t, registry, "dbml_load_layout",
		map[string]any{"projectId": "p1", "relPath": "a.dbml"})
	require.NoError(t, err)

	encoded, err := json.Marshal(answer)
	require.NoError(t, err)
	assert.JSONEq(t, `[{"table_key":"public.orders","x":10,"y":20}]`, string(encoded))
}

func TestAMissingParameterIsNamed(t *testing.T) {
	store, _ := newStore(t)
	registry := registryFor(t, dbml.Deps{Store: store})

	_, err := invoke(t, registry, "dbml_load_layout", map[string]any{"projectId": "p1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "relPath")
}

func TestTheAssistantRefusesWithNoEngineConfigured(t *testing.T) {
	registry := registryFor(t, dbml.Deps{})

	_, err := invoke(t, registry, "dbml_assist", map[string]any{
		"mode": "review", "dbml": "Table t { }",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no AI engine")
}

func TestSavingAConnectionThroughTheBridgeNeverEchoesThePassword(t *testing.T) {
	store, _ := newStore(t)
	credentials := newFakeCredentials()
	registry := registryFor(t, dbml.Deps{Store: store, Credentials: credentials})

	answer, err := invoke(t, registry, "dbml_save_connection", map[string]any{
		"connection": map[string]any{
			"id": nil, "name": "staging", "driver": "postgres",
			"host": "db.test", "port": 5432, "database": "app", "username": "readonly",
			"file_path": nil, "use_tls": true, "password": "la-clave",
		},
	})
	require.NoError(t, err)

	encoded, err := json.Marshal(answer)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "la-clave")
	assert.NotContains(t, string(encoded), "password")

	// It did reach the credential store, which is the only place it goes.
	assert.Len(t, credentials.secrets, 1)
}
