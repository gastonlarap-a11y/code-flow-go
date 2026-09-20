package workspaces_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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
	//
	// **On Windows this is not enforced, and the skip is the honest way to say so.** Go's `Chmod`
	// maps to the read-only attribute and nothing else, so `Perm()` answers 0666 for any writable
	// file however it was created; the mode passed to `WriteFile` is simply discarded. The file
	// inherits the ACL of the directory above it, which under `C:\CodeFlow` is whatever the
	// installer left. Making this file private on Windows needs an explicit ACL through
	// `golang.org/x/sys/windows`, which nothing here does today — 2.x did not either, so it is a
	// gap this port inherits rather than one it opened. Asserting the Unix bits anyway would turn
	// a real platform limitation into a red test that somebody eventually deletes.
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix permission bits; mcp.json inherits the directory ACL (see the comment above)")
	}

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
