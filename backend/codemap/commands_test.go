package codemap_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/codemap"
)

// noTools is a machine with neither go nor node: each half of the map says so instead of failing.
func noTools() codemap.Tools { return codemap.Tools{} }

func registryWith(t *testing.T, service *codemap.Service) *bridge.Registry {
	t.Helper()
	return bridgeRegistry(t, codemap.Deps{Service: service})
}

func bridgeRegistry(t *testing.T, deps codemap.Deps) *bridge.Registry {
	t.Helper()
	registry := bridge.NewRegistry()
	codemap.Register(registry, deps)
	registry.Seal()
	return registry
}

// call invokes a command the way the bridge does and answers its result as the renderer reads it.
func call(t *testing.T, registry *bridge.Registry, name string, params map[string]any) (map[string]any, []any, error) {
	t.Helper()
	handler, found := registry.Lookup(name)
	require.True(t, found, "%s is not registered", name)
	raw, err := json.Marshal(params)
	require.NoError(t, err)
	answer, err := handler(t.Context(), bridge.NewParams(raw))
	if err != nil {
		return nil, nil, err
	}
	encoded, err := json.Marshal(answer)
	require.NoError(t, err)
	var object map[string]any
	if json.Unmarshal(encoded, &object) == nil {
		return object, nil, nil
	}
	var list []any
	require.NoError(t, json.Unmarshal(encoded, &list), "neither an object nor an array: %s", encoded)
	return nil, list, nil
}

// schemaOnly is a repository holding nothing but migrations and an unbuildable go.mod.
func schemaOnly(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "db"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(root, "db", "001.up.sql"), []byte("CREATE TABLE invoices (id int);"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/x\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte("{}"), 0o600))
	return root
}

func TestTheEightCommandsAreRegistered(t *testing.T) {
	registry := registryWith(t, codemap.NewService(noTools, nil))
	for _, name := range []string{
		"codemap_status", "codemap_overview", "codemap_search", "codemap_symbol", "codemap_flow", "codemap_impact",
		"codemap_ask", "codemap_archify",
	} {
		_, found := registry.Lookup(name)
		assert.True(t, found, name)
	}
	assert.Equal(t, 8, registry.Len())
}

// Without an Archify install the status says so, and the export refuses with the way to get one.
func TestArchifyIsOfferedOnlyWhenInstalled(t *testing.T) {
	t.Setenv("ARCHIFY_BIN", "")
	t.Setenv("HOME", t.TempDir())
	root := schemaOnly(t)
	registry := registryWith(t, codemap.NewService(noTools, nil))

	status, _, err := call(t, registry, "codemap_status", map[string]any{"rootPath": root})
	require.NoError(t, err)
	assert.Empty(t, status["archify"])

	_, _, err = call(t, registry, "codemap_archify", map[string]any{"rootPath": root})
	require.ErrorIs(t, err, codemap.ErrNoArchify)

	_, _, err = call(t, registry, "codemap_archify", map[string]any{})
	require.Error(t, err, "rootPath is required")
}

// A machine without the toolchains still answers: each language says what it is missing, and the
// SQL half is mapped on its own.
func TestStatusNamesWhatIsMissing(t *testing.T) {
	root := schemaOnly(t)
	registry := registryWith(t, codemap.NewService(noTools, nil))

	status, _, err := call(t, registry, "codemap_status", map[string]any{"rootPath": root})
	require.NoError(t, err)

	assert.Equal(t, root, status["root_path"], "named as the renderer sent it")
	languages, ok := status["languages"].([]any)
	require.True(t, ok)
	states := map[string]string{}
	for _, entry := range languages {
		language, ok := entry.(map[string]any)
		require.True(t, ok)
		states[language["lang"].(string)] = language["state"].(string) //nolint:forcetypeassert // a test reading its own fixture
	}
	assert.Equal(t, map[string]string{
		"go":  codemap.StateMissingTool,
		"ts":  codemap.StateMissingTool,
		"sql": codemap.StateMapped,
	}, states)
	assert.NotNil(t, status["notes"], "never null on the wire")
}

func TestQueriesCrossInTheRenderersShape(t *testing.T) {
	root := schemaOnly(t)
	registry := registryWith(t, codemap.NewService(noTools, nil))

	_, results, err := call(t, registry, "codemap_search", map[string]any{"rootPath": root, "query": "invoice"})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "sql:invoices", results[0].(map[string]any)["id"]) //nolint:forcetypeassert // a test reading its own fixture

	_, results, err = call(t, registry, "codemap_search", map[string]any{"rootPath": root, "query": "nothing-here"})
	require.NoError(t, err)
	assert.NotNil(t, results, "an empty search is [], never null")

	lookup, _, err := call(t, registry, "codemap_symbol", map[string]any{"rootPath": root, "symbol": "invoices"})
	require.NoError(t, err)
	detail, ok := lookup["detail"].(map[string]any)
	require.True(t, ok)
	for _, list := range []string{"callers", "callees", "implementations", "implements", "tables", "tests", "unresolved"} {
		assert.NotNil(t, detail[list], list)
	}

	flow, _, err := call(t, registry, "codemap_flow", map[string]any{"rootPath": root, "symbol": "sql:invoices"})
	require.NoError(t, err)
	assert.NotNil(t, flow["root"])

	impact, _, err := call(t, registry, "codemap_impact", map[string]any{"rootPath": root, "symbol": "sql:invoices"})
	require.NoError(t, err)
	for _, list := range []string{"levels", "entry_points", "go_tests", "ts_tests"} {
		assert.NotNil(t, impact[list], list)
	}

	overview, _, err := call(t, registry, "codemap_overview", map[string]any{"rootPath": root})
	require.NoError(t, err)
	assert.NotNil(t, overview["tables"])
}

func TestTheRefusals(t *testing.T) {
	root := schemaOnly(t)
	registry := registryWith(t, codemap.NewService(noTools, nil))

	_, _, err := call(t, registry, "codemap_status", map[string]any{"rootPath": filepath.Join(root, "missing")})
	require.ErrorIs(t, err, codemap.ErrNotADirectory)

	_, _, err = call(t, registry, "codemap_flow", map[string]any{"rootPath": root, "symbol": "nothing"})
	require.ErrorIs(t, err, codemap.ErrUnknownSymbol)

	_, _, err = call(t, registry, "codemap_search", map[string]any{"rootPath": root})
	require.Error(t, err, "the query is required")

	_, _, err = call(t, registry, "codemap_search", map[string]any{"rootPath": root, "query": "x", "kind": "galaxy"})
	require.Error(t, err)
}

// Questions arrive in bursts; one build serves them, and a change rebuilds.
func TestTheMapIsReusedUntilTheRepositoryChanges(t *testing.T) {
	root := schemaOnly(t)
	events := &bridge.RecordingEmitter{}
	service := codemap.NewService(noTools, events)

	first, _, err := service.Graph(t.Context(), root)
	require.NoError(t, err)
	second, _, err := service.Graph(t.Context(), root)
	require.NoError(t, err)
	assert.Same(t, first, second)
	assert.Len(t, events.Named(codemap.EventProgress), 5, "one build: five stages")
}
