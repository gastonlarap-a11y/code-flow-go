package codemap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/ai"
)

// Asking the AI about the code, grounded in the map (CODEMAP-014).

// Asker is what the code map asks of the AI layer — the same one method the schema designer's
// assistant uses.
type Asker interface {
	Invoke(ctx context.Context, runID string, task ai.Task, inv ai.Invocation, withTrace bool) (ai.Result, error)
}

// AskRequest is one question.
type AskRequest struct {
	RootPath string
	Question string
	// Symbol is the symbol the question is about, as an ID or a name; empty asks about the whole
	// repository.
	Symbol string
	// RunID lets the panel stream the run and stop it, like every other AI run.
	RunID string
	// SessionID continues an earlier answer's conversation, where the engine can.
	SessionID string
	// Language is the interface's language, "en" or "es", which the answer is written in.
	Language string
}

// AskAnswer is the reply, and the conversation to continue it in.
type AskAnswer struct {
	Answer    string `json:"answer"`
	SessionID string `json:"session_id"`
}

// The refusals, each naming what is wrong.
var (
	ErrNoQuestion = errors.New("ask a question first")       //nolint:staticcheck // ST1005: shown to the user as a sentence
	ErrNoEngine   = errors.New("no AI engine is configured") //nolint:staticcheck // ST1005: shown to the user as a sentence
)

const (
	// maxQuestionChars bounds the question, by Unicode scalar.
	maxQuestionChars = 4_000
	// The share of the context each part may take: enough to ground an answer, small enough that
	// the model reads it rather than drowning in it.
	overviewChars = 3_000
	partChars     = 5_000
)

// askTools are what the model may use: reading the repository, and asking the map. Nothing that
// writes, nothing that runs a command.
var askTools = []string{"Read", "Grep", "Glob", "mcp__" + MCPServerName}

const askSystemPrompt = `You answer questions about the repository in the working directory, inside CodeFlow's code map.

On stdin is what the code map knows about the question's subject. It comes from the compiler — real
calls, interface dispatch, the hops from TypeScript into Go, the SQL tables read and written — so
trust it over anything a name suggests. You can read files with Read, Grep and Glob, and, when they
are available, ask the map more with the codemap tools (codemap_symbol, codemap_flow,
codemap_impact, codemap_search, codemap_overview).

Explain how the code works: follow the flow end to end, name every function you mention with its
file:line, and say plainly what is only known at run time. Do not change anything.

Answer in %s, concisely, in Markdown.`

// Ask answers a question about a repository, with the map's facts about the question's subject
// handed to the model and the map itself offered to it as tools.
func (s *Service) Ask(ctx context.Context, engine Asker, executable string, request AskRequest) (AskAnswer, error) {
	question := strings.TrimSpace(request.Question)
	if question == "" {
		return AskAnswer{}, ErrNoQuestion
	}
	if len([]rune(question)) > maxQuestionChars {
		question = string([]rune(question)[:maxQuestionChars])
	}
	if engine == nil {
		return AskAnswer{}, ErrNoEngine
	}

	g, status, err := s.Graph(ctx, request.RootPath)
	if err != nil {
		return AskAnswer{}, err
	}
	facts, err := askContext(g, status, request.Symbol)
	if err != nil {
		return AskAnswer{}, err
	}

	invocation := ai.Invocation{
		Prompt:       question,
		SystemPrompt: fmt.Sprintf(askSystemPrompt, languageName(request.Language)),
		StdinContent: facts,
		AllowedTools: askTools,
		WorkingDir:   request.RootPath,
		SessionID:    request.SessionID,
		ReadOnly:     true,
	}
	if executable != "" {
		config, cleanup, err := writeMCPConfig(executable, invocation.WorkingDir)
		if err != nil {
			return AskAnswer{}, err
		}
		// Deleted on every exit path: a reply, a CLI failure, a cancellation.
		defer cleanup()
		invocation.MCPConfig = config
	}

	result, err := engine.Invoke(ctx, request.RunID, ai.TaskChat, invocation, false)
	if err != nil {
		return AskAnswer{}, err
	}
	return AskAnswer{Answer: strings.TrimSpace(result.Text), SessionID: result.SessionID}, nil
}

// askContext is what the model is told before it reads anything: the map at a glance and, for a
// question about one symbol, that symbol, its flow and its impact.
func askContext(g *Graph, status Status, symbol string) (string, error) {
	var b strings.Builder
	b.WriteString("# The code map's facts\n\n")
	b.WriteString(Clip(RenderOverview(g.Overview(status)), overviewChars))
	if strings.TrimSpace(symbol) == "" {
		return b.String(), nil
	}
	n, err := single(g, symbol)
	if err != nil {
		return "", err
	}
	b.WriteString("\n# The symbol the question is about\n\n")
	b.WriteString(Clip(RenderDetail(g.Detail(n)), partChars))
	b.WriteString("\n# Where it leads\n\n")
	b.WriteString(Clip(RenderFlow(g.Flow(n, FlowOptions{Depth: 8, Budget: 150})), partChars))
	b.WriteString("\n# What depends on it\n\n")
	b.WriteString(Clip(RenderImpact(g.Impact(n, 4)), partChars))
	return b.String(), nil
}

// writeMCPConfig writes a one-server MCP configuration that runs this binary as the map's server,
// readable by its owner only, and answers its path and how to remove it.
func writeMCPConfig(executable, root string) (string, func(), error) {
	config := map[string]any{
		"mcpServers": map[string]any{
			MCPServerName: map[string]any{
				"command": executable,
				"args":    []string{MCPCommand, "--root", root},
			},
		},
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return "", func() {}, fmt.Errorf("encoding the code map's MCP configuration: %w", err)
	}
	file, err := os.CreateTemp("", "codeflow-codemap-mcp-*.json")
	if err != nil {
		return "", func() {}, fmt.Errorf("writing the code map's MCP configuration: %w", err)
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) } // best effort: the OS clears its temp folder too
	if _, err := file.Write(raw); err != nil {
		_ = file.Close() // the write error is the one worth reporting
		cleanup()
		return "", func() {}, fmt.Errorf("writing the code map's MCP configuration: %w", err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("writing the code map's MCP configuration: %w", err)
	}
	return path, cleanup, nil
}

func languageName(code string) string {
	if strings.HasPrefix(strings.ToLower(code), "es") {
		return "Spanish"
	}
	return "English"
}
