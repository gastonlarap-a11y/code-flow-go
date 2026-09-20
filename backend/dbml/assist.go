package dbml

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/ai"
)

// The assistant: one command, three modes, and it reaches for nothing (DBML-016).

// The three modes, `VERBATIM` — the renderer sends these strings.
const (
	ModeEdit    = "edit"
	ModeReview  = "review"
	ModeExplain = "explain"
)

const (
	// maxSchemaChars refuses rather than truncates. `edit` is asked to return the **whole**
	// document, so a dropped tail would come back as a proposal that deletes every table past the
	// cut — a silent, destructive answer that looks like a valid one.
	maxSchemaChars = 60_000
	// maxInstructionChars bounds the ask, by Unicode scalar.
	maxInstructionChars = 4_000
)

// The refusals, each naming what is wrong rather than failing generically.
var (
	ErrEmptySchema    = errors.New("the document is empty")                                     //nolint:staticcheck // ST1005: VERBATIM
	ErrSchemaTooLarge = fmt.Errorf("the document is longer than %d characters", maxSchemaChars) //nolint:err113 // names the bound
	// ErrNoInstruction refuses an `edit` with nothing to do, because applying nothing has no
	// meaning. `review` and `explain` stand on their own.
	ErrNoInstruction = errors.New("an edit needs an instruction") //nolint:staticcheck // ST1005: VERBATIM
)

// AssistRequest is one run of the assistant.
type AssistRequest struct {
	Mode string
	DBML string
	// Instruction is required for `edit` and optional for the other two.
	Instruction string
	// RunID lets the panel show live output and a stop button, the same way every other AI run does.
	RunID string
}

// Reviewer is what this feature asks of the AI layer, declared here and kept to one method.
type Reviewer interface {
	Invoke(ctx context.Context, runID string, task ai.Task, inv ai.Invocation, withTrace bool) (ai.Result, error)
}

// Assist runs one of the three modes over the document (DBML-016).
//
// The mode selects a system prompt **and nothing else**. The ask rides on argv and the schema on
// stdin, which is `AI-002`'s split, and only `edit`'s reply is unfenced — stripping a review's first
// fenced block would eat a finding.
func Assist(ctx context.Context, engine Reviewer, request AssistRequest) (string, error) {
	prompt, err := promptFor(request.Mode)
	if err != nil {
		return "", err
	}

	schema := strings.TrimSpace(request.DBML)
	if schema == "" {
		return "", ErrEmptySchema
	}
	if len([]rune(schema)) > maxSchemaChars {
		return "", ErrSchemaTooLarge
	}

	instruction := strings.TrimSpace(request.Instruction)
	if request.Mode == ModeEdit && instruction == "" {
		return "", ErrNoInstruction
	}
	if len([]rune(instruction)) > maxInstructionChars {
		instruction = string([]rune(instruction)[:maxInstructionChars])
	}

	result, err := engine.Invoke(ctx, request.RunID, ai.TaskDBML, ai.Invocation{
		SystemPrompt: prompt,
		Prompt:       instruction,
		StdinContent: schema,
		// **The empty list, which the engines read as "no tools at all".** The model is handed the
		// whole document and asked about that document, so a tool call could only re-read what it
		// already has or wander into a folder that need not even be a repository.
		AllowedTools: []string{},
		ReadOnly:     true,
	}, false)
	if err != nil {
		return "", err
	}

	if request.Mode == ModeEdit {
		// Only here: the answer is meant to replace the document, and a fence around it would land
		// in the buffer as backticks.
		return ai.StripCodeFence(result.Text), nil
	}
	return strings.TrimSpace(result.Text), nil
}

// promptFor maps a mode onto its embedded prompt.
//
// An unknown mode **names itself** in the error, which is what a renderer/backend drift looks like
// from a log — and the only shape of failure this command can have that is not the user's.
func promptFor(mode string) (string, error) {
	switch mode {
	case ModeEdit:
		return ai.Prompt(ai.PromptDBMLEdit), nil
	case ModeReview:
		return ai.Prompt(ai.PromptDBMLReview), nil
	case ModeExplain:
		return ai.Prompt(ai.PromptDBMLExplain), nil
	default:
		return "", fmt.Errorf("unknown assistant mode %q", mode) //nolint:err113 // names the drift
	}
}
