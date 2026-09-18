package ai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Caps, in Unicode scalars rather than bytes so a diff of accented prose is cut where a reader
// would expect. Each is a judgement about what is worth sending rather than a technical limit.
const (
	// maxDiffChars is a working diff or a file's context.
	maxDiffChars = 20_000
	// maxConflictSideChars bounds one side of a conflict. A side bigger than this is better
	// merged by hand than fed whole to a model.
	//
	// The branch-comparison cap (120 000) arrives with generate_pr_description, which is the only
	// operation that needs it — declaring it now would be a constant nothing reads.
	maxConflictSideChars = 40_000
)

// Operations is everything the AI commands do, behind one type so the transport branch is decided
// in exactly one place (AI-003).
type Operations struct {
	router  Router
	runs    *RunRegistry
	runner  Runner
	client  *http.Client
	secrets SecretReader
}

// NewOperations wires the operations.
func NewOperations(router Router, runs *RunRegistry, client *http.Client, secrets SecretReader) Operations {
	return Operations{
		router:  router,
		runs:    runs,
		runner:  NewRunner(runs),
		client:  client,
		secrets: secrets,
	}
}

// Invoke runs one invocation on whichever engine the task resolves to.
//
// The transport is branched on **before** anything subprocess-specific happens, because two of the
// six engines have no process: resolving a binary for an endpoint is how a working Ollama install
// reports itself as not found.
func (o Operations) Invoke(ctx context.Context, runID string, task Task, inv Invocation, withTrace bool) (Result, error) {
	config := o.router.Resolve(ctx, task)
	return o.invokeWith(ctx, runID, config, inv, withTrace)
}

func (o Operations) invokeWith(ctx context.Context, runID string, config Config, inv Invocation, withTrace bool) (Result, error) {
	inv.Model = config.Model
	if inv.AllowedTools == nil {
		inv.AllowedTools = config.AllowedTools
	}

	engine := EngineFor(config.Provider)

	if engine.Transport() != TransportSubprocess {
		// No run is registered for an HTTP turn: there is no process to kill, the request is one
		// round trip, and a stop button over something with no cancel path would lie.
		return NewHTTPEngine(o.client, o.secrets, config.Provider).Complete(ctx, config.Binary, inv)
	}

	var run *Run
	if runID != "" && o.runs != nil {
		run = o.runs.Begin(runID, withTrace)
		defer o.runs.End(runID)
	}

	builder, interpreter, cleanup, err := subprocessEngine(config.Provider, config.Binary, inv)
	if err != nil {
		return Result{}, err
	}
	// Deleted on **every** exit path — reply, CLI error, launch failure, cancellation — which is
	// what closed BUG-AI-a. A defer is the only placement that covers all four.
	defer cleanup()

	return o.runner.Execute(ctx, run, config.Binary, builder, interpreter, inv)
}

// subprocessEngine picks the command builder and interpreter for a provider, preparing whatever
// scratch file its CLI needs.
func subprocessEngine(provider, binary string, inv Invocation) (CommandBuilder, Interpreter, func(), error) {
	switch provider {
	case "codex":
		return CodexCommands{}, CodexInterpreter{Binary: binary}, func() {}, nil

	case "gemini":
		commands, cleanup, err := PrepareBrief(inv)
		if err != nil {
			return nil, nil, func() {}, err
		}
		return commands, AgyInterpreter{Binary: binary}, cleanup, nil

	case "opencode":
		commands, cleanup, err := PreparePayload(inv)
		if err != nil {
			return nil, nil, func() {}, err
		}
		return commands, OpenCodeInterpreter{Binary: binary}, cleanup, nil

	default:
		return ClaudeCommands{}, ClaudeInterpreter{Binary: binary}, func() {}, nil
	}
}

// ---- the operations -------------------------------------------------------------------------------

// GenerateCommitMessage drafts a commit message from the staged diff (AI-019).
func (o Operations) GenerateCommitMessage(ctx context.Context, runID, diff string) (string, error) {
	if strings.TrimSpace(diff) == "" {
		// VERBATIM.
		return "", errors.New("No staged changes to summarize") //nolint:staticcheck // ST1005: VERBATIM
	}

	template := o.router.SharedTemplate(ctx, "commit_template", "claude_commit_template", Prompt(PromptCommit))

	result, err := o.Invoke(ctx, runID, TaskCommit, Invocation{
		Prompt:       template,
		StdinContent: truncate(diff, maxDiffChars),
		ReadOnly:     true,
	}, false)
	if err != nil {
		return "", err
	}
	return stripCodeFence(result.Text), nil
}

// ResolveConflict proposes a merged file from the three sides (AI-021).
//
// Nothing is written to disk here: the caller decides whether to accept it, which is what keeps a
// model's guess from silently becoming the resolution.
func (o Operations) ResolveConflict(ctx context.Context, runID, filePath, base, ours, theirs string) (string, error) {
	template := o.router.SharedTemplate(ctx,
		"resolve_conflict_template", "claude_resolve_conflict_template", Prompt(PromptResolveConflict))

	// Each side capped independently, and no empty-side guard: a side that is empty because one
	// branch deleted the file is information the model needs, not a missing input.
	payload := fmt.Sprintf(
		"ARCHIVO: %s\n\nVERSIÓN BASE:\n%s\n\nVERSIÓN NUESTRA:\n%s\n\nVERSIÓN DE ELLOS:\n%s",
		filePath,
		truncate(base, maxConflictSideChars),
		truncate(ours, maxConflictSideChars),
		truncate(theirs, maxConflictSideChars))

	result, err := o.Invoke(ctx, runID, TaskConflict, Invocation{
		Prompt:       template,
		StdinContent: payload,
		ReadOnly:     true,
	}, false)
	if err != nil {
		return "", err
	}
	return stripCodeFence(result.Text), nil
}

// InlineEdit rewrites an editor selection (AI-027).
//
// Nothing reaches disk: the editor applies the answer to its buffer, so the user's undo still
// works and a wrong suggestion costs one keystroke.
func (o Operations) InlineEdit(ctx context.Context, runID, relPath, fileContent, selection, instruction string) (string, error) {
	if strings.TrimSpace(selection) == "" {
		// VERBATIM, Spanish.
		return "", errors.New("No hay código seleccionado para editar") //nolint:staticcheck // ST1005: VERBATIM
	}

	payload := fmt.Sprintf(
		"ARCHIVO: %s\n\nCONTENIDO DEL ARCHIVO (contexto):\n%s\n\nFRAGMENTO SELECCIONADO:\n%s\n\nINSTRUCCIÓN:\n%s",
		relPath, truncate(fileContent, maxDiffChars), selection, instruction)

	result, err := o.Invoke(ctx, runID, TaskInline, Invocation{
		// Always the built-in prompt: there is no override path for this one, because the answer
		// is spliced straight into a file and its shape is not the user's to change.
		SystemPrompt: Prompt(PromptInlineEdit),
		Prompt:       instruction,
		StdinContent: payload,
		ReadOnly:     true,
	}, false)
	if err != nil {
		return "", err
	}
	return stripCodeFence(result.Text), nil
}

// ApplyFindingFix lets an agent fix a review finding in the working tree (AI-026).
//
// The only operation that writes, and the two things it does differently both follow from that:
// the user's general tool list is **ignored** in favour of the engine's fixed write set — clicking
// "fix" is itself the write-access opt-in — and edits are auto-approved, because there is no way
// to answer a permission prompt headlessly.
func (o Operations) ApplyFindingFix(ctx context.Context, runID, findingPrompt, workingDir string) (string, error) {
	config := o.router.Resolve(ctx, TaskFix)

	if !agentic(config.Provider) {
		// A backstop: the UI already hides "fix with AI" for these. VERBATIM, Spanish, and it
		// names the alternatives because "not supported" alone leaves the user nowhere to go.
		return "", errors.New("Este proveedor no puede editar archivos. Usa Claude, Gemini u opencode.") //nolint:staticcheck // ST1005: VERBATIM
	}

	result, err := o.invokeWith(ctx, runID, config, Invocation{
		SystemPrompt:     Prompt(PromptFixFinding),
		Prompt:           findingPrompt,
		WorkingDir:       workingDir,
		AutoApproveEdits: true,
		AllowedTools:     fixTools(config.Provider),
		// Not read-only: repeating it would apply the agent's edits twice.
		ReadOnly: false,
	}, false)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Text), nil
}

// agentic reports whether an engine has a tool loop at all. The two HTTP engines are plain
// completion endpoints: no tools, no repository, nothing to edit with.
func agentic(provider string) bool {
	return EngineFor(provider).Transport() == TransportSubprocess
}

// fixTools is the write set a fix runs with.
//
// opencode's names are flagged unverified in the original and are **never actually passed** — its
// CLI has no allow-list flag, so its write access comes entirely from `--auto` (AMBIGUOUS-AI-a).
// They are kept so the two engines that do read the list behave the same.
func fixTools(provider string) []string {
	if provider == "opencode" {
		return []string{"read", "edit", "write", "bash", "grep", "glob"}
	}
	return []string{"Read", "Edit", "Write", "Bash", "Grep", "Glob"}
}

// ---- shared helpers -------------------------------------------------------------------------------

// truncate cuts text to a number of **characters**, never bytes: cutting mid-character sends
// invalid UTF-8 to the model and fails the whole response rather than shortening it.
func truncate(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}

// stripCodeFence removes one outer fence (AI-018).
//
// Applied to the three results meant to be used verbatim — a file, an editor selection, a commit
// message — because some models wrap their answer in a fence despite being told not to, and those
// backticks would go into the repository's history.
//
// **One** fence, and only an outer one: a genuinely fenced block inside the intended content does
// not survive a round trip, which is what the original does and what the tests pin.
func stripCodeFence(text string) string {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}

	// Past the opening fence's whole line, which may carry a language tag.
	_, rest, found := strings.Cut(trimmed, "\n")
	if !found {
		return trimmed
	}

	closing := strings.LastIndex(rest, "```")
	if closing < 0 {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(rest[:closing])
}
