package ai_test

import (
	"strings"
	"testing"

	"github.com/gastonlarap-a11y/code-flow/backend/ai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func claudeArgs(inv ai.Invocation) string {
	return strings.Join(ai.ClaudeCommands{}.BuildCommand(inv), " ")
}

// The two flags are the only combination the CLI accepts alongside -p, and they are chosen rather
// than tolerated: plain json prints nothing until the process exits, so a nine-minute review would
// show an empty log and then fill at once.
func TestClaudeAlwaysStreams(t *testing.T) {
	args := claudeArgs(ai.Invocation{Prompt: "the ask"})

	assert.Contains(t, args, "-p the ask")
	assert.Contains(t, args, "--output-format stream-json --verbose")
}

// Without this the analysed repository's own configuration applied to the run — its hooks, plugins
// and LSP servers. A Stop hook that type-checks and lints ran inside CodeFlow's analysis and held
// the process open long after the review had finished. Observed live 2026-08-02.
func TestClaudeIgnoresTheAnalysedRepositorysSettings(t *testing.T) {
	assert.Contains(t, claudeArgs(ai.Invocation{}), "--setting-sources user")
}

// Both flags, and the difference is the point: --allowedTools names what runs without asking,
// --tools names what exists. Passing only the former changed nothing measurable.
func TestClaudePassesTheToolListTwice(t *testing.T) {
	args := claudeArgs(ai.Invocation{AllowedTools: []string{"Read", "Grep", "Glob"}})

	assert.Contains(t, args, "--tools Read,Grep,Glob")
	assert.Contains(t, args, "--allowedTools Read,Grep,Glob")
}

func TestClaudeOmitsEveryOptionalFlagWhenUnset(t *testing.T) {
	args := claudeArgs(ai.Invocation{Prompt: "ask"})

	for _, absent := range []string{
		"--model", "--append-system-prompt", "--tools", "--allowedTools",
		"--permission-mode", "--mcp-config", "--resume",
	} {
		assert.NotContains(t, args, absent)
	}
}

func TestClaudeCarriesTheOptionalFlagsWhenSet(t *testing.T) {
	args := claudeArgs(ai.Invocation{
		Prompt:           "ask",
		SystemPrompt:     "be brief",
		Model:            "a-model",
		AutoApproveEdits: true,
		MCPConfig:        "/tmp/mcp.json",
		SessionID:        "sess-1",
	})

	assert.Contains(t, args, "--append-system-prompt be brief")
	assert.Contains(t, args, "--model a-model")
	assert.Contains(t, args, "--permission-mode acceptEdits")
	assert.Contains(t, args, "--mcp-config /tmp/mcp.json --strict-mcp-config")
	assert.Contains(t, args, "--resume sess-1")
}

func TestClaudePipesItsData(t *testing.T) {
	assert.Equal(t, "the diff", ai.ClaudeCommands{}.StdinPayload(ai.Invocation{StdinContent: "the diff"}))
}

// ---- interpreting ---------------------------------------------------------------------------------

const toolUseLine = `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read"}]}}`

func TestClaudeReadsTheLastResultEvent(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"system","subtype":"init"}`,
		toolUseLine,
		`{"type":"result","is_error":false,"result":"the answer","session_id":"sess-9"}`,
	}, "\n")

	result, err := ai.ClaudeInterpreter{Binary: "claude"}.Interpret(stream, "", 0)

	require.NoError(t, err)
	assert.Equal(t, "the answer", result.Text)
	assert.Equal(t, "sess-9", result.SessionID)
}

// The intermediate lines are valid JSON and are not the reply. A consumer assembling the answer
// from them shows the user the agent's scratchpad and calls it the result.
func TestClaudeNeverMistakesAToolEventForTheReply(t *testing.T) {
	stream := toolUseLine + "\n" +
		`{"type":"result","is_error":false,"result":"final"}`

	result, err := ai.ClaudeInterpreter{Binary: "claude"}.Interpret(stream, "", 0)

	require.NoError(t, err)
	assert.Equal(t, "final", result.Text)
}

// The defect this pins: under this output format the CLI reports its own failures on stdout while
// leaving stderr empty and exiting non-zero. Branching on the exit status first reports an empty
// stderr and throws away the only copy of the reason.
func TestClaudeSurfacesTheReasonJSONCarriesWhenStderrIsEmpty(t *testing.T) {
	stream := `{"type":"result","is_error":true,"result":"Model 'gpt-9' is not available"}`

	_, err := ai.ClaudeInterpreter{Binary: "claude"}.Interpret(stream, "", 1)

	require.Error(t, err)
	assert.Equal(t, "Model 'gpt-9' is not available", err.Error(),
		"the only copy of the reason is on stdout; reading the exit status first loses it")
}

// The dictionary phrases are transcribed from real failing runs, so this uses one of them rather
// than a plausible-sounding paraphrase — an invented wording would pass a test and fail in life.
func TestClaudeTagsALostLoginFromItsOwnJSON(t *testing.T) {
	stream := `{"type":"result","is_error":true,"result":"OAuth session expired, run claude auth login"}`

	_, err := ai.ClaudeInterpreter{Binary: "claude"}.Interpret(stream, "", 1)

	require.Error(t, err)
	assert.Equal(t, "AUTH_EXPIRED::OAuth session expired, run claude auth login", err.Error())
}

func TestClaudeTagsAQuotaRefusal(t *testing.T) {
	stream := `{"type":"result","is_error":true,"result":"Claude usage limit reached"}`

	_, err := ai.ClaudeInterpreter{Binary: "claude"}.Interpret(stream, "", 1)

	require.Error(t, err)
	assert.Equal(t, "QUOTA_EXCEEDED::Claude usage limit reached", err.Error())
}

// A CLI that ignored the flag, or a build that does not stream: the whole buffer is the reply.
func TestClaudeFallsBackToTheWholeBuffer(t *testing.T) {
	result, err := ai.ClaudeInterpreter{Binary: "claude"}.
		Interpret(`{"type":"result","is_error":false,"result":"answered"}`, "", 0)

	require.NoError(t, err)
	assert.Equal(t, "answered", result.Text)
}

func TestClaudeWithNoResultEventAndAZeroExitIsStillASuccess(t *testing.T) {
	result, err := ai.ClaudeInterpreter{Binary: "claude"}.Interpret("plain text reply\n", "", 0)

	require.NoError(t, err)
	assert.Equal(t, "plain text reply", result.Text)
}

func TestClaudeWithNoResultEventAndAFailureReportsTheStreams(t *testing.T) {
	_, err := ai.ClaudeInterpreter{Binary: "claude"}.Interpret("", "command not found\n", 127)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "claude exited with an error (exit status: 127)")
	assert.Contains(t, err.Error(), "command not found")
}

// A run that fanned out across several models has no single honest answer, and naming one of them
// would be a guess the caller stores as fact.
func TestTheModelUsedIsOnlyReportedWhenThereIsOne(t *testing.T) {
	one := `{"type":"result","result":"x","modelUsage":{"claude-opus-5":{"inputTokens":10}}}`
	assert.Equal(t, "claude-opus-5", ai.ModelUsed(one))

	several := `{"type":"result","result":"x","modelUsage":{"a":{"inputTokens":1},"b":{"inputTokens":2}}}`
	assert.Empty(t, ai.ModelUsed(several), "the caller falls back to the configured setting")

	assert.Empty(t, ai.ModelUsed("not json at all"))
}
