package ai

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTheLogKeepsWhatItShowsOfAnEvent(t *testing.T) {
	tests := []struct {
		name, line, want string
		keep             bool
	}{
		{"plain text stays", "Compiling…", "Compiling…", true},
		{"untagged JSON stays", `{"answer":42}`, `{"answer":42}`, true},
		{"not JSON stays", `{broken`, `{broken`, true},
		{
			"an assistant turn keeps its prose and its tool calls' argument",
			`{"type":"assistant","message":{"id":"m1","model":"x","usage":{"input_tokens":9},"content":[{"type":"text","text":"Reading the bridge."},{"type":"tool_use","id":"t1","name":"Grep","input":{"pattern":"Invoke","path":"backend","-n":true}}]}}`,
			`{"type":"assistant","message":{"content":[{"type":"text","text":"Reading the bridge."},{"type":"tool_use","name":"Grep","input":{"path":"backend"}}]}}`,
			true,
		},
		{
			"a tool call with no argument the log names keeps its name",
			`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"TodoWrite","input":{"todos":[]}}]}}`,
			`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"TodoWrite"}]}}`,
			true,
		},
		{"an assistant turn with nothing to show is dropped", `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"…"}]}}`, "", false},
		{"the model a run starts with", `{"type":"system","subtype":"init","model":"claude-opus-5-5","tools":["Read"],"mcp_servers":[{"name":"codemap"}]}`, `{"type":"system","subtype":"init","model":"claude-opus-5-5"}`, true},
		{"other system events are dropped", `{"type":"system","subtype":"compact_boundary"}`, "", false},
		{"a tool result is dropped", `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"package main"}]}}`, "", false},
		{"the final result is dropped", `{"type":"result","subtype":"success","result":"{…}"}`, "", false},
		{"any other tagged event is dropped", `{"type":"rate_limit_event","rate_limit_info":{}}`, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, keep := logView(tt.line)
			assert.Equal(t, tt.keep, keep)
			if tt.keep {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

// The keys kept are the renderer's own (XLANG-020), read from its source rather than copied here.
func TestTheKeptArgumentKeysAreTheRenderersOwn(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "frontend", "src", "lib", "agentLog.ts"))
	require.NoError(t, err)
	list := regexp.MustCompile(`TOOL_ARG_KEYS = \[([^\]]*)\]`).FindSubmatch(source)
	require.NotNil(t, list, "agentLog.ts declares TOOL_ARG_KEYS")
	keys := []string{}
	for _, quoted := range regexp.MustCompile(`"([^"]+)"`).FindAllSubmatch(list[1], -1) {
		keys = append(keys, string(quoted[1]))
	}
	assert.Equal(t, keys, logToolArgKeys)
}

func TestAnEventsProseAndArgumentAreBounded(t *testing.T) {
	long := strings.Repeat("é", 2000)
	got, keep := logView(`{"type":"assistant","message":{"content":[{"type":"text","text":"` + long + `"},{"type":"tool_use","name":"Bash","input":{"command":"` + long + `"}}]}}`)
	assert.True(t, keep)
	assert.Less(t, len([]rune(got)), logTextRunes+logArgRunes+200)
	assert.Contains(t, got, "…")
}
