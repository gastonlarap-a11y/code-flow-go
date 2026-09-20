package workspaces

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// The per-workspace `mcp.json` an AI run is handed (REVIEW-004).
//
// It lives here because this package owns the rows it is written from, and because two features now
// need the same file: a pull-request review and a pre-commit one. Written twice, the two would have
// drifted on the first change to the env parsing — and a server that reaches one review and not the
// other is a difference nobody would look for.

// WriteMCPConfig writes the enabled servers to `path` and answers it, or answers "" when there is
// nothing to write.
//
// Empty is what leaves the flag off the engine's command line entirely, which is the difference
// between "no servers configured" and "a config naming none" — the second makes some engines
// complain.
//
// Overwritten on every review and kept under the workspace's own folder rather than in a temporary
// directory: it is a file a user can open when a server misbehaves.
//
// Best-effort by contract: every failure answers "", because a review without its MCP servers is a
// weaker review and not a failed one.
func WriteMCPConfig(path string, mcps []MCP) string {
	enabled := make(map[string]any, len(mcps))
	for _, mcp := range mcps {
		if !mcp.Enabled {
			continue
		}
		server := map[string]any{"command": mcp.Command}
		if args := strings.Fields(mcp.Args); len(args) > 0 {
			server["args"] = args
		}
		if env := ParseEnvLines(mcp.Env); len(env) > 0 {
			server["env"] = env
		}
		enabled[mcp.Name] = server
	}
	if len(enabled) == 0 || strings.TrimSpace(path) == "" {
		return ""
	}

	encoded, err := json.MarshalIndent(map[string]any{"mcpServers": enabled}, "", "  ")
	if err != nil {
		return ""
	}
	// 0700: this file can carry an MCP server's own credentials in its `env`, and nothing but this
	// user's app has business reading it.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return ""
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return ""
	}
	return path
}

// ParseEnvLines reads `KEY=value` lines out of the free-text field the settings form writes.
//
// A line with no `=` is dropped rather than erroring: it is a text area a person types into, and a
// stray line there must not stop a review.
func ParseEnvLines(raw string) map[string]string {
	env := make(map[string]string, 4)
	for _, line := range strings.Split(raw, "\n") {
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if key != "" {
			env[key] = value
		}
	}
	return env
}
