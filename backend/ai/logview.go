package ai

import (
	"encoding/json"
	"strings"
)

// What the activity log shows of a CLI's JSON event (AI-012).
//
// An agentic CLI streams one JSON event per line — Claude's `stream-json` — and the renderer turns
// each into a line of the log: an assistant turn's prose and the tools it calls, the model a run
// started with, and nothing for the rest (tool results, token bookkeeping, the final verdict).
// Those events are as big as what they carry: a tool result holding a whole file is a line of a
// hundred kilobytes, which the 2 000-character cap cut in the middle, so the renderer received JSON
// it could not parse and showed it raw. So the event is reduced here to the fields the renderer
// reads — `frontend/src/lib/agentLog.ts`, whose keys this mirrors — before the cap applies, and the
// events it hides are not sent at all.

// logToolArgKeys are the fields that hold a tool call's interesting argument, in the order
// agentLog.ts prefers them — its TOOL_ARG_KEYS, the same eight (XLANG-020).
var logToolArgKeys = []string{"file_path", "path", "notebook_path", "command", "pattern", "url", "query", "prompt"}

const (
	// logTextRunes bounds a turn's prose. The renderer shows 160 characters of it on one line.
	logTextRunes = 600
	// logArgRunes bounds a tool call's argument, which the renderer also cuts at 160.
	logArgRunes = 300
)

type logEvent struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype,omitempty"`
	Model   string `json:"model,omitempty"`
	Message *struct {
		Content []logPart `json:"content"`
	} `json:"message,omitempty"`
}

type logPart struct {
	Type  string         `json:"type"`
	Text  string         `json:"text,omitempty"`
	Name  string         `json:"name,omitempty"`
	Input map[string]any `json:"input,omitempty"`
}

// logView answers what of a line the log keeps, and false when it keeps nothing. A line that is
// not a tagged JSON event — plain text, an untagged object — is kept as it is.
func logView(line string) (string, bool) {
	if !strings.HasPrefix(line, "{") {
		return line, true
	}
	var event logEvent
	if json.Unmarshal([]byte(line), &event) != nil || event.Type == "" {
		return line, true
	}
	var view logEvent
	switch {
	case event.Type == "assistant" && event.Message != nil:
		parts := make([]logPart, 0, len(event.Message.Content))
		for _, part := range event.Message.Content {
			switch part.Type {
			case "text":
				if strings.TrimSpace(part.Text) != "" {
					parts = append(parts, logPart{Type: "text", Text: clipRunes(part.Text, logTextRunes)})
				}
			case "tool_use":
				if part.Name != "" {
					parts = append(parts, logPart{Type: "tool_use", Name: part.Name, Input: toolArg(part.Input)})
				}
			}
		}
		if len(parts) == 0 {
			return "", false
		}
		view = logEvent{Type: "assistant", Message: &struct {
			Content []logPart `json:"content"`
		}{Content: parts}}
	case event.Type == "system" && event.Subtype == "init" && event.Model != "":
		view = logEvent{Type: "system", Subtype: "init", Model: event.Model}
	default:
		return "", false // tool results, the final result, bookkeeping: the log shows none of them
	}
	raw, err := json.Marshal(view)
	if err != nil {
		return line, true // a view that cannot be written is no reason to lose the line
	}
	return string(raw), true
}

// toolArg keeps the one argument the log names a tool call by.
func toolArg(input map[string]any) map[string]any {
	for _, key := range logToolArgKeys {
		if value, ok := input[key].(string); ok && strings.TrimSpace(value) != "" {
			return map[string]any{key: clipRunes(value, logArgRunes)}
		}
	}
	return nil
}

func clipRunes(text string, n int) string {
	if runes := []rune(text); len(runes) > n {
		return string(runes[:n]) + "…"
	}
	return text
}
