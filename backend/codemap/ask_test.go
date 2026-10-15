package codemap_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/ai"
	"github.com/gastonlarap-a11y/code-flow/backend/codemap"
	"github.com/gastonlarap-a11y/code-flow/backend/repomap"
)

// fakeAI records what the code map hands the AI layer, and what the MCP configuration said while
// the run was going.
type fakeAI struct {
	task       ai.Task
	inv        ai.Invocation
	mcpConfig  string
	answer     string
	sessionOut string
	err        error
}

func (f *fakeAI) Invoke(_ context.Context, _ string, task ai.Task, inv ai.Invocation, _ bool) (ai.Result, error) {
	f.task, f.inv = task, inv
	if inv.MCPConfig != "" {
		raw, err := os.ReadFile(inv.MCPConfig)
		if err == nil {
			f.mcpConfig = string(raw)
		}
	}
	return ai.Result{Text: f.answer, SessionID: f.sessionOut}, f.err
}

func TestAskHandsTheModelTheMapsFacts(t *testing.T) {
	root := schemaOnly(t)
	service := codemap.NewService(noTools, nil)
	engine := &fakeAI{answer: "  It stores invoices.  ", sessionOut: "sess-2"}

	answer, err := service.Ask(t.Context(), engine, "/opt/CodeFlow", codemap.AskRequest{
		RootPath: root, Question: "What is this table for?", Symbol: "invoices",
		RunID: "run-1", SessionID: "sess-1", Language: "es",
	})
	require.NoError(t, err)

	assert.Equal(t, codemap.AskAnswer{Answer: "It stores invoices.", SessionID: "sess-2"}, answer)
	assert.Equal(t, ai.TaskChat, engine.task)
	assert.Equal(t, "What is this table for?", engine.inv.Prompt)
	assert.Contains(t, engine.inv.StdinContent, "# The symbol the question is about")
	assert.Contains(t, engine.inv.StdinContent, "invoices (table) db/001.up.sql:1")
	assert.Contains(t, engine.inv.SystemPrompt, "Answer in Spanish")
	assert.Equal(t, []string{"Read", "Grep", "Glob", "mcp__codemap"}, engine.inv.AllowedTools, "reading and asking the map, nothing that writes")
	assert.True(t, engine.inv.ReadOnly)
	assert.Equal(t, root, engine.inv.WorkingDir)
	assert.Equal(t, "sess-1", engine.inv.SessionID)

	var config struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal([]byte(engine.mcpConfig), &config))
	assert.Equal(t, "/opt/CodeFlow", config.MCPServers["codemap"].Command)
	assert.Equal(t, []string{"codemap-mcp", "--root", root}, config.MCPServers["codemap"].Args)
	_, statErr := os.Stat(engine.inv.MCPConfig)
	assert.True(t, os.IsNotExist(statErr), "the configuration is removed once the run ends")
}

func TestAskAboutTheWholeRepositoryAndWithoutTheMapsTools(t *testing.T) {
	root := schemaOnly(t)
	engine := &fakeAI{answer: "ok"}

	_, err := codemap.NewService(noTools, nil).Ask(t.Context(), engine, "", codemap.AskRequest{RootPath: root, Question: "Overview?"})
	require.NoError(t, err)

	assert.Contains(t, engine.inv.StdinContent, "# The code map's facts")
	assert.NotContains(t, engine.inv.StdinContent, "# The symbol the question is about")
	assert.Empty(t, engine.inv.MCPConfig, "no executable, no MCP server")
	assert.Contains(t, engine.inv.SystemPrompt, "Answer in English")
}

func TestAskRefusals(t *testing.T) {
	root := schemaOnly(t)
	service := codemap.NewService(noTools, nil)

	_, err := service.Ask(t.Context(), &fakeAI{}, "", codemap.AskRequest{RootPath: root, Question: "   "})
	require.ErrorIs(t, err, codemap.ErrNoQuestion)

	_, err = service.Ask(t.Context(), nil, "", codemap.AskRequest{RootPath: root, Question: "why?"})
	require.ErrorIs(t, err, codemap.ErrNoEngine)

	_, err = service.Ask(t.Context(), &fakeAI{}, "", codemap.AskRequest{RootPath: root, Question: "why?", Symbol: "nothing"})
	require.ErrorIs(t, err, codemap.ErrUnknownSymbol)

	failing := &fakeAI{err: errors.New("claude: not logged in")}
	_, err = service.Ask(t.Context(), failing, "/opt/CodeFlow", codemap.AskRequest{RootPath: root, Question: "why?"})
	require.EqualError(t, err, "claude: not logged in", "the engine's own message reaches the user unchanged")
}

func TestTheAskCommandIsRegisteredAndBindsItsParameters(t *testing.T) {
	root := schemaOnly(t)
	engine := &fakeAI{answer: "fine"}
	registry := bridgeRegistry(t, codemap.Deps{Service: codemap.NewService(noTools, nil), AI: engine})

	answer, _, err := call(t, registry, "codemap_ask", map[string]any{
		"rootPath": root, "question": "Which tables exist?", "runId": "r1", "language": "en",
	})
	require.NoError(t, err)
	assert.Equal(t, "fine", answer["answer"])
	assert.Equal(t, "Which tables exist?", engine.inv.Prompt)

	_, _, err = call(t, registry, "codemap_ask", map[string]any{"question": "x"})
	require.EqualError(t, err, "missing required parameter 'rootPath'")
}

// The map's MCP server, over an in-memory connection: the tools a model sees and what they answer.
func TestTheMCPServerAnswersFromTheMap(t *testing.T) {
	root := schemaOnly(t)
	ctx := t.Context()
	server := codemap.NewMCPServer(ctx, codemap.NewService(noTools, nil), repomap.NewReadOnlyService(t.TempDir()), root, "test")
	serverSide, clientSide := mcp.NewInMemoryTransports()
	_, err := server.Connect(ctx, serverSide, nil)
	require.NoError(t, err)
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, clientSide, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	tools, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	require.NoError(t, err)
	names := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
		assert.True(t, tool.Annotations.ReadOnlyHint, "%s must be read-only", tool.Name)
	}
	assert.ElementsMatch(t, []string{
		"codemap_overview", "codemap_search", "codemap_symbol", "codemap_flow", "codemap_impact",
		"codemap_files", "codemap_outline", "codemap_declarations", "codemap_name_usages",
	}, names)

	text := func(name string, args map[string]any) (string, bool) {
		t.Helper()
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		require.NoError(t, err)
		var b strings.Builder
		for _, content := range result.Content {
			if c, ok := content.(*mcp.TextContent); ok {
				b.WriteString(c.Text)
			}
		}
		return b.String(), result.IsError
	}

	found, failed := text("codemap_search", map[string]any{"query": "invoice"})
	assert.False(t, failed)
	assert.Contains(t, found, "invoices (table) db/001.up.sql:1")

	detail, _ := text("codemap_symbol", map[string]any{"name": "invoices"})
	assert.Contains(t, detail, "# invoices (table)")

	overview, _ := text("codemap_overview", nil)
	assert.Contains(t, overview, "sql: mapped")

	missing, failed := text("codemap_flow", map[string]any{"name": "nothing"})
	assert.True(t, failed, "an unknown symbol is an error the model can read")
	assert.Contains(t, missing, "no single symbol has that name")

	// The file index's tools, from the same server.
	files, failed := text("codemap_files", nil)
	assert.False(t, failed)
	assert.Contains(t, files, "Repository map:")
	outline, _ := text("codemap_outline", map[string]any{"path": "nowhere.ts"})
	assert.Contains(t, outline, "is not in the map")
	declared, _ := text("codemap_declarations", map[string]any{"query": "nothing"})
	assert.Contains(t, declared, "Nothing is declared")
}

func TestRunMCPWantsARoot(t *testing.T) {
	var stderr bytes.Buffer
	code := codemap.RunMCP(t.Context(), nil, "test", nil, io.NopCloser(strings.NewReader("")), nopWriteCloser{io.Discard}, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "usage: codemap-mcp --root")
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func TestTextAnswersAreClippedOnALine(t *testing.T) {
	long := strings.Repeat("a line of text\n", 1000)
	clipped := codemap.Clip(long, 100)
	assert.LessOrEqual(t, len(clipped), 140)
	assert.Contains(t, clipped, "more lines not shown")
	assert.Equal(t, "short\n", codemap.Clip("short\n", 100))
}
