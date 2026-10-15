package archify

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/repomap"
)

// invoke calls a registered command as the bridge does, and answers its result as JSON text.
func invoke(t *testing.T, registry *bridge.Registry, name string, params map[string]any) (string, error) {
	t.Helper()
	handler, found := registry.Lookup(name)
	require.True(t, found, "%s is not registered", name)
	raw, err := json.Marshal(params)
	require.NoError(t, err)
	answer, err := handler(t.Context(), bridge.NewParams(raw))
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(answer)
	require.NoError(t, err)
	return string(encoded), nil
}

func TestTheAICommandsNameWhatIsMissing(t *testing.T) {
	registry := bridge.NewRegistry()
	Register(registry, Deps{Runtime: func() Runtime { return Runtime{} }, Maps: repomap.NewService(t.TempDir())})

	tests := []struct {
		name, command string
		params        map[string]any
		want          string
	}{
		{"generate without a repository", "archify_generate", map[string]any{"kind": "sequence"}, "missing required parameter 'rootPath'"},
		{"refine without a repository", "archify_refine", map[string]any{"path": "a.archify.json"}, "missing required parameter 'rootPath'"},
		{"refine without a document", "archify_refine", map[string]any{"rootPath": t.TempDir()}, "missing required parameter 'path'"},
		{"suggestions without a kind", "archify_suggestions", map[string]any{"rootPath": t.TempDir()}, "missing required parameter 'kind'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := invoke(t, registry, tt.command, tt.params)
			require.EqualError(t, err, tt.want)
		})
	}

	_, err := invoke(t, registry, "archify_generate", map[string]any{"rootPath": t.TempDir(), "kind": "sequence"})
	require.ErrorIs(t, err, ErrNoAI, "no engine wired, no run")

	none, err := invoke(t, registry, "archify_suggestions", map[string]any{"rootPath": t.TempDir(), "kind": "workflow"})
	require.NoError(t, err)
	assert.Equal(t, "[]", none, "an empty list, never null")
}
