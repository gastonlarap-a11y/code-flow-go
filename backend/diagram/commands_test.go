package diagram_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/diagram"
)

func registryFor(t *testing.T) *bridge.Registry {
	t.Helper()

	registry := bridge.NewRegistry()
	diagram.Register(registry)
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

// write creates a file and every directory above it.
func write(t *testing.T, root string, parts ...string) {
	t.Helper()

	path := filepath.Join(append([]string{root}, parts...)...)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(`{"schema":"codeflow.diagram/1"}`), 0o600))
}

func TestTheOneCommandIsRegistered(t *testing.T) {
	registry := registryFor(t)

	_, found := registry.Lookup("diagram_list_documents")
	assert.True(t, found)
	assert.Equal(t, 1, registry.Len(), "the editor is one command; everything else is a file command")
}

func TestListDocumentsFindsOnlyDiagrams(t *testing.T) {
	root := t.TempDir()

	write(t, root, "checkout.mmd")
	write(t, root, "flows", "onboarding.mmd")
	write(t, root, "package.json")
	write(t, root, "schema.dbml")
	write(t, root, "node_modules", "ignored.mmd")

	found, err := diagram.ListDocuments(t.Context(), root)
	require.NoError(t, err)

	assert.Equal(t, []string{"checkout.mmd", "flows/onboarding.mmd"}, found)
}

// The shape the renderer receives, asserted through the bridge rather than through the Go
// signature: `rootPath` is the renderer's spelling, and an empty folder has to arrive as `[]` and
// never as `null` — a nil slice would crash the picker's `.map` on the one case that is most
// common, a project with no diagrams yet.
func TestTheCommandCrossesInTheRenderersShape(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.mmd")

	registry := registryFor(t)

	answer, err := invoke(t, registry, "diagram_list_documents", map[string]any{"rootPath": root})
	require.NoError(t, err)

	encoded, err := json.Marshal(answer)
	require.NoError(t, err)
	assert.JSONEq(t, `["a.mmd"]`, string(encoded))
}

func TestAnEmptyFolderAnswersAnEmptyArray(t *testing.T) {
	registry := registryFor(t)

	answer, err := invoke(t, registry, "diagram_list_documents", map[string]any{"rootPath": t.TempDir()})
	require.NoError(t, err)

	encoded, err := json.Marshal(answer)
	require.NoError(t, err)
	assert.JSONEq(t, `[]`, string(encoded), "never null: the renderer maps over this")
}

func TestAMissingParameterIsNamed(t *testing.T) {
	registry := registryFor(t)

	_, err := invoke(t, registry, "diagram_list_documents", map[string]any{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing required parameter 'rootPath'")
}

func TestAFolderThatIsNotOneIsRefused(t *testing.T) {
	registry := registryFor(t)

	_, err := invoke(t, registry, "diagram_list_documents",
		map[string]any{"rootPath": filepath.Join(t.TempDir(), "nowhere")})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no such folder: ")
}
