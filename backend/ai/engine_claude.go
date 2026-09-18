package ai

import (
	"encoding/json"
	"errors"
	"strings"
)

// The Claude CLI (AI-028 … AI-031).
//
// It is the default engine — everything unrecognised resolves here — so its two peculiarities
// carry the most weight: it streams one JSON event per line while it runs, and it reports its own
// failures on **stdout** while exiting non-zero and leaving stderr empty.

// ClaudeCommands builds argv and the stdin payload for the Claude CLI.
type ClaudeCommands struct{}

// BuildCommand assembles the arguments after the binary name.
//
// `--output-format stream-json --verbose` together are the only combination the CLI accepts
// alongside `-p`, and they are chosen rather than tolerated: plain `json` prints nothing until the
// process exits, so the activity log would stay empty for a nine-minute review and then fill at
// once. One JSON event per line is what makes it live.
func (ClaudeCommands) BuildCommand(inv Invocation) []string {
	args := []string{"-p", inv.Prompt}

	if inv.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", inv.SystemPrompt)
	}
	if inv.Model != "" {
		args = append(args, "--model", inv.Model)
	}

	args = append(args, "--output-format", "stream-json", "--verbose")

	// `--setting-sources user` keeps the *analysed* repository's own configuration out of the run.
	// Without it a project's `.claude/settings.json` applied — its hooks, plugins and LSP servers
	// all loaded — and a Stop hook that type-checks and lints ran inside CodeFlow's analysis,
	// holding the process open long after the review had finished. Observed live 2026-08-02.
	args = append(args, "--setting-sources", "user")

	if len(inv.AllowedTools) > 0 {
		list := strings.Join(inv.AllowedTools, ",")
		// Both flags, and the difference is the whole point: `--allowedTools` names what runs
		// without asking, `--tools` names what *exists*. Passing only the former changed nothing
		// measurable — Bash stayed available and went on being called seventeen times in a review
		// that took nine minutes.
		args = append(args, "--tools", list, "--allowedTools", list)
	}
	if inv.AutoApproveEdits {
		args = append(args, "--permission-mode", "acceptEdits")
	}
	if inv.MCPConfig != "" {
		args = append(args, "--mcp-config", inv.MCPConfig, "--strict-mcp-config")
	}
	if inv.SessionID != "" {
		args = append(args, "--resume", inv.SessionID)
	}
	return args
}

// StdinPayload is the data, piped: this CLI does read stdin.
func (ClaudeCommands) StdinPayload(inv Invocation) string { return inv.StdinContent }

// claudeResult is the terminal event of the stream.
type claudeResult struct {
	Type       string `json:"type"`
	IsError    bool   `json:"is_error"`
	Result     string `json:"result"`
	SessionID  string `json:"session_id"`
	ModelUsage map[string]struct {
		InputTokens int `json:"inputTokens"`
	} `json:"modelUsage"`
}

// ClaudeInterpreter turns the stream into a reply.
type ClaudeInterpreter struct{ Binary string }

// Interpret reads the last `{"type":"result"}` line of the stream.
//
// **Stdout is parsed before the exit status is looked at**, and the order is the rule rather than
// an implementation detail. Under this output format the CLI reports an expired login or an
// unknown model as `{"is_error":true,"result":"<reason>"}` on stdout while leaving stderr empty
// and exiting non-zero. Branching on the exit status first reports an empty stderr and throws away
// the only copy of the reason.
func (c ClaudeInterpreter) Interpret(stdout, stderr string, exitCode int) (Result, error) {
	payload, found := lastResultPayload(stdout)

	if found {
		text := strings.TrimSpace(payload.Result)
		if payload.IsError {
			return Result{}, errors.New(Classify(text, false))
		}
		return Result{Text: text, SessionID: payload.SessionID}, nil
	}

	if exitCode != 0 {
		return Result{}, errors.New(Classify(FailureDetail(c.Binary, exitCode, stdout, stderr), false))
	}

	// A zero exit with no terminal event: a CLI that ignored the flag, or a build that does not
	// stream. The whole buffer is the reply rather than an error — the run succeeded.
	return Result{Text: strings.TrimSpace(stdout)}, nil
}

// lastResultPayload scans the stream backwards for the terminal event.
//
// Backwards because the stream carries one event per line and only the last `result` is the
// answer; the intermediate lines are tool-use events that are valid JSON and are **not** the
// reply. Falling back to parsing the whole buffer covers a CLI that ignored the streaming flag.
func lastResultPayload(stdout string) (claudeResult, bool) {
	lines := strings.Split(stdout, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var payload claudeResult
		if err := json.Unmarshal([]byte(line), &payload); err == nil && payload.Type == "result" {
			return payload, true
		}
	}

	var whole claudeResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &whole); err == nil && whole.Type == "result" {
		return whole, true
	}
	return claudeResult{}, false
}

// ModelUsed reports which model answered, when there is one honest answer.
//
// Only when exactly one model was used: a run that fanned out across several has no single answer,
// and naming one of them would be a guess the caller then stores as fact. Nothing means "fall back
// to the configured setting".
func ModelUsed(stdout string) string {
	payload, found := lastResultPayload(stdout)
	if !found || len(payload.ModelUsage) != 1 {
		return ""
	}
	for model := range payload.ModelUsage {
		return model
	}
	return ""
}
