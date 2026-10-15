package ai

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// AI-057: only an engine that enforces reading, with no edit approval and only reading tools, may run
// a read-only invocation.
func TestOnlyAReadingEngineRunsARequiredReadOnlyInvocation(t *testing.T) {
	reading := []string{"Read", "Grep", "Glob", "mcp__codemap"}
	tests := []struct {
		name     string
		provider string
		inv      Invocation
		refused  bool
	}{
		{"claude with reading tools", "claude", Invocation{AllowedTools: reading}, false},
		{"codex with reading tools", "codex", Invocation{AllowedTools: reading}, false},
		{"agy cannot be held to reading", "gemini", Invocation{AllowedTools: reading}, true},
		{"opencode has no bound", "opencode", Invocation{AllowedTools: reading}, true},
		{"an HTTP engine cannot read the repository", "ollama", Invocation{AllowedTools: reading}, true},
		{"an approved edit is not reading", "claude", Invocation{AllowedTools: reading, AutoApproveEdits: true}, true},
		{"the CLI's default tools are not named", "claude", Invocation{}, true},
		{"a writing tool", "claude", Invocation{AllowedTools: []string{"Read", "Edit"}}, true},
		{"a shell", "claude", Invocation{AllowedTools: []string{"Bash"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkReadOnly(EngineFor(tt.provider), tt.inv)
			if tt.refused {
				require.ErrorIs(t, err, ErrEngineNotReadOnly)
				return
			}
			assert.NoError(t, err)
		})
	}
}

// The refusal happens before anything is spawned or sent: an HTTP engine never sees the request.
func TestAReadOnlyRunIsRefusedBeforeTheEngineRuns(t *testing.T) {
	ops := NewOperations(Router{}, nil, nil, nil)
	_, err := ops.invokeWith(t.Context(), "", Config{Provider: "ollama", Binary: "http://127.0.0.1:1"},
		Invocation{RequireReadOnly: true, AllowedTools: []string{"Read"}}, false)
	require.ErrorIs(t, err, ErrEngineNotReadOnly)
}
