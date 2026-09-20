package workspaces_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/workspaces"
)

// `REVIEW-004`: the per-workspace `mcp.json` both review pipelines hand to the engine.

func TestWriteMCPConfigWritesOnlyTheEnabledServers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspaces", "w1", "mcp.json")

	written := workspaces.WriteMCPConfig(path, []workspaces.MCP{
		{Name: "files", Command: "mcp-files", Args: "--root /repo --read-only", Enabled: true},
		{Name: "apagado", Command: "mcp-nope", Enabled: false},
	})
	require.Equal(t, path, written)

	body, err := os.ReadFile(path)
	require.NoError(t, err)

	var config struct {
		Servers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal(body, &config))

	require.Len(t, config.Servers, 1)
	assert.Equal(t, "mcp-files", config.Servers["files"].Command)
	// Split on whitespace, which is how the settings field is typed.
	assert.Equal(t, []string{"--root", "/repo", "--read-only"}, config.Servers["files"].Args)
}

// No enabled server means no file and no path, which is what leaves the flag off the engine's
// command line entirely — not a config naming none, which some engines complain about.
func TestWriteMCPConfigAnswersNothingWhenNoServerIsEnabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")

	assert.Empty(t, workspaces.WriteMCPConfig(path, nil))
	assert.Empty(t, workspaces.WriteMCPConfig(path, []workspaces.MCP{
		{Name: "apagado", Command: "x", Enabled: false},
	}))

	_, err := os.Stat(path)
	assert.True(t, os.IsNotExist(err), "nothing is written either")
}

func TestWriteMCPConfigKeepsTheFilePrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspaces", "w1", "mcp.json")
	require.NotEmpty(t, workspaces.WriteMCPConfig(path,
		[]workspaces.MCP{{Name: "s", Command: "x", Env: "TOKEN=secreto", Enabled: true}}))

	// The file can carry an MCP server's own credentials in its `env`, and nothing but this user's
	// app has business reading it — the directory it lands in included.
	file, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), file.Mode().Perm())

	dir, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), dir.Mode().Perm())
}

func TestParseEnvLines(t *testing.T) {
	env := workspaces.ParseEnvLines("TOKEN=secreto\n  HOST = example.test \nsin igual\n\nVACIO=")

	assert.Equal(t, map[string]string{
		"TOKEN": "secreto",
		"HOST":  "example.test",
		// A key with an empty value is a choice; a line with no `=` is a stray line in a text area
		// and is dropped rather than failing a review.
		"VACIO": "",
	}, env)
}
