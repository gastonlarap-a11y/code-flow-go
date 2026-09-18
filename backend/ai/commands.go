package ai

import (
	"context"
	"errors"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
)

// Deps is what this feature needs from the composition root.
type Deps struct {
	// Router resolves which engine and model a task runs on. Its settings reader may be nil, which
	// resolves everything to the built-in defaults — an install whose database did not open can
	// still be configured.
	Router Router
	// Catalogue answers the Settings screen's two questions.
	Catalogue Catalogue
	// Runs owns the operations in flight, so the stop button has something to stop.
	Runs *RunRegistry
	// Operations is the engine dispatch. Its zero value answers nothing useful, so a caller that
	// wants the AI commands to work passes a wired one.
	Operations Operations
	// Conflicts reads the three sides of a merge conflict. Declared at the consumer: this package
	// needs one question answered and knows nothing else about git.
	Conflicts ConflictReader
	// Projects resolves a project id to the repository it names. Nil when the database did not
	// open, and then the commands that need it say so rather than guessing a directory.
	Projects ProjectPaths
	// Turns persists chat history. Nil without a database: a turn then answers normally and simply
	// is not remembered, which beats refusing to answer at all.
	Turns TurnRecorder
	// Checkpoints protects the working tree around anything that can write to it.
	Checkpoints Checkpointer
	// Branches compares two branches, for drafting a pull request description.
	Branches BranchDiffer
}

// BranchDiffer answers the diff between two branches.
type BranchDiffer interface {
	BranchDiff(ctx context.Context, repo, source, target string) (string, error)
}

// ConflictReader answers the three versions of a conflicted file.
type ConflictReader interface {
	ConflictVersions(ctx context.Context, repo, relPath string) (base, ours, theirs string, err error)
}

// ProjectPaths answers where a project's repository lives on disk.
type ProjectPaths interface {
	ProjectPath(ctx context.Context, projectID string) (string, error)
}

// Register adds the commands this phase answers.
//
// The five template commands and the two Settings queries need no run lifecycle, which is why they
// land before it: they are what the Settings screen calls on open, and an install that cannot yet
// run an engine should still be able to show and configure one.
func Register(r *bridge.Registry, deps Deps) {
	// The built-in prompt templates, exactly as shipped. The renderer shows them as the
	// placeholder in the editor where a user writes their own, so the bytes matter: it compares
	// what is stored against these to decide whether a template is still the default.
	for command, prompt := range map[string]string{
		"default_commit_template":           PromptCommit,
		"default_review_template":           PromptReview,
		"default_analyze_template":          PromptAnalyze,
		"default_pr_description_template":   PromptPRDescription,
		"default_resolve_conflict_template": PromptResolveConflict,
	} {
		r.Add(command, func(_ context.Context, _ bridge.Params) (any, error) {
			return Prompt(prompt), nil
		})
	}

	// An empty list is a valid answer and the renderer's signal to show its own curated list.
	r.Add("list_ai_models", func(ctx context.Context, p bridge.Params) (any, error) {
		provider, err := bridge.Arg[string](p, "provider")
		if err != nil {
			return nil, err
		}
		binary := deps.Router.setting(ctx, provider+"_binary_path")
		return deps.Catalogue.ListModels(ctx, provider, binary)
	})

	r.Add("check_ai_provider", func(ctx context.Context, p bridge.Params) (any, error) {
		provider, err := bridge.Arg[string](p, "provider")
		if err != nil {
			return nil, err
		}
		binary := deps.Router.setting(ctx, provider+"_binary_path")
		return deps.Catalogue.Probe(ctx, provider, binary), nil
	})

	// False rather than an error for a run that is not there: the ordinary case is a run that
	// finished between the panel rendering its stop button and the user pressing it, and an error
	// banner over a run that succeeded is worse than a button that did nothing.
	r.Add("cancel_ai_run", func(_ context.Context, p bridge.Params) (any, error) {
		runID, err := bridge.Arg[string](p, "runId")
		if err != nil {
			return nil, err
		}
		if deps.Runs == nil {
			return false, nil
		}
		return deps.Runs.Cancel(runID), nil
	})

	// ---- the operations ---------------------------------------------------------------------------
	//
	// `runId` is optional on every one of them: the renderer sends it when it wants a stop button,
	// and a run with no id simply cannot be cancelled rather than failing.

	r.Add("generate_commit_message", func(ctx context.Context, p bridge.Params) (any, error) {
		diff, err := bridge.Arg[string](p, "diff")
		if err != nil {
			return nil, err
		}
		runID, err := bridge.ArgOr(p, "runId", "")
		if err != nil {
			return nil, err
		}
		return deps.Operations.GenerateCommitMessage(ctx, runID, diff)
	})

	r.Add("inline_edit_with_ai", func(ctx context.Context, p bridge.Params) (any, error) {
		var args struct {
			RelPath     string `json:"relPath"`
			FileContent string `json:"fileContent"`
			Selection   string `json:"selection"`
			Instruction string `json:"instruction"`
			RunID       string `json:"runId"`
		}
		if err := bridge.Bind(p, &args); err != nil {
			return nil, err
		}
		return deps.Operations.InlineEdit(ctx, args.RunID,
			args.RelPath, args.FileContent, args.Selection, args.Instruction)
	})

	r.Add("resolve_conflict_with_ai", func(ctx context.Context, p bridge.Params) (any, error) {
		repoPath, err := bridge.Arg[string](p, "repoPath")
		if err != nil {
			return nil, err
		}
		relPath, err := bridge.Arg[string](p, "relPath")
		if err != nil {
			return nil, err
		}
		runID, err := bridge.ArgOr(p, "runId", "")
		if err != nil {
			return nil, err
		}
		if deps.Conflicts == nil {
			return nil, errors.New("no repository available")
		}
		// The three sides come from the index rather than from the markers in the working copy:
		// the model gets each version whole instead of reverse-engineering them.
		base, ours, theirs, err := deps.Conflicts.ConflictVersions(ctx, repoPath, relPath)
		if err != nil {
			return nil, err
		}
		return deps.Operations.ResolveConflict(ctx, runID, relPath, base, ours, theirs)
	})

	r.Add("resolve_finding_with_ai", func(ctx context.Context, p bridge.Params) (any, error) {
		projectID, err := bridge.Arg[string](p, "projectId")
		if err != nil {
			return nil, err
		}
		findingPrompt, err := bridge.Arg[string](p, "findingPrompt")
		if err != nil {
			return nil, err
		}
		runID, err := bridge.ArgOr(p, "runId", "")
		if err != nil {
			return nil, err
		}
		if deps.Projects == nil {
			return nil, errors.New("no project store available")
		}
		// The fix happens **inside** the project's own repository: an agent editing files needs a
		// working tree, and the project id is how the renderer names one.
		workingDir, err := deps.Projects.ProjectPath(ctx, projectID)
		if err != nil {
			return nil, err
		}
		// A fix writes to the tree, so it is snapshotted first and the snapshot dropped afterwards
		// when nothing changed — including on failure, so a half-applied fix from a killed run
		// stays undoable (AI-052).
		checkpointID := ""
		if deps.Checkpoints != nil && workingDir != "" {
			if id, err := deps.Checkpoints.CreateCheckpoint(ctx, workingDir, "fix-finding"); err == nil {
				checkpointID = id
			}
		}
		text, err := deps.Operations.ApplyFindingFix(ctx, runID, findingPrompt, workingDir)
		if checkpointID != "" {
			_, _ = deps.Checkpoints.RemoveCheckpointIfUnchanged(ctx, workingDir, checkpointID)
		}
		return text, err
	})

	r.Add("send_chat_message", func(ctx context.Context, p bridge.Params) (any, error) {
		var args struct {
			ProjectID      string         `json:"projectId"`
			Message        string         `json:"message"`
			SessionID      *string        `json:"sessionId"`
			ConversationID string         `json:"conversationId"`
			RunID          string         `json:"runId"`
			Agent          *AgentOverride `json:"agent"`
		}
		if err := bridge.Bind(p, &args); err != nil {
			return nil, err
		}
		if args.ProjectID == "" {
			return nil, bridge.MissingParameterError("projectId")
		}
		if deps.Projects == nil {
			return nil, errors.New("no project store available")
		}
		workingDir, err := deps.Projects.ProjectPath(ctx, args.ProjectID)
		if err != nil {
			return nil, err
		}

		request := ChatRequest{
			ProjectID:      args.ProjectID,
			WorkingDir:     workingDir,
			Message:        args.Message,
			ConversationID: args.ConversationID,
			RunID:          args.RunID,
			Agent:          args.Agent,
		}
		if args.SessionID != nil {
			request.SessionID = *args.SessionID
		}
		return deps.Operations.Chat(ctx, deps.Turns, deps.Checkpoints, request)
	})

	r.Add("generate_pr_description", func(ctx context.Context, p bridge.Params) (any, error) {
		var args struct {
			ProjectID    string `json:"projectId"`
			SourceBranch string `json:"sourceBranch"`
			TargetBranch string `json:"targetBranch"`
			RunID        string `json:"runId"`
		}
		if err := bridge.Bind(p, &args); err != nil {
			return nil, err
		}
		if deps.Projects == nil || deps.Branches == nil {
			return nil, errors.New("no repository available")
		}
		repo, err := deps.Projects.ProjectPath(ctx, args.ProjectID)
		if err != nil {
			return nil, err
		}
		// Local git, no host call: a description can be drafted before the pull request exists,
		// which is when the user actually wants one.
		diff, err := deps.Branches.BranchDiff(ctx, repo, args.SourceBranch, args.TargetBranch)
		if err != nil {
			return nil, err
		}
		return deps.Operations.GeneratePRDescription(ctx, args.RunID,
			args.SourceBranch, args.TargetBranch, diff)
	})
}
