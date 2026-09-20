package review

import (
	"context"

	"github.com/gastonlarap-a11y/code-flow/backend/ai"
	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/git"
)

// The two commands that run a review.
//
// They look alike and behave differently in the one way that matters: the project-backed one has a
// clone, a history and a memory, so it reconciles against the last review and saves this one; the
// link one has none of that, so every call re-runs the whole analysis from scratch.

// RegisterPipeline adds the commands that run a review.
func RegisterPipeline(r *bridge.Registry, deps PipelineDeps) {
	r.Add("review_pull_request", func(ctx context.Context, p bridge.Params) (any, error) {
		projectID, err := bridge.Arg[string](p, "projectId")
		if err != nil {
			return nil, err
		}
		prID, err := bridge.Arg[int64](p, "prId")
		if err != nil {
			return nil, err
		}
		jobID, err := bridge.Arg[string](p, "jobId")
		if err != nil {
			return nil, err
		}
		level, err := bridge.Arg[string](p, "level")
		if err != nil {
			return nil, err
		}
		agent, err := agentOverride(p)
		if err != nil {
			return nil, err
		}

		return deps.Run(ctx, RunRequest{
			ProjectID: projectID, PRID: prID, JobID: jobID, Level: level, Agent: agent,
		})
	})

	r.Add("review_pr_from_link", func(ctx context.Context, p bridge.Params) (any, error) {
		url, err := bridge.Arg[string](p, "url")
		if err != nil {
			return nil, err
		}
		jobID, err := bridge.Arg[string](p, "jobId")
		if err != nil {
			return nil, err
		}
		level, err := bridge.Arg[string](p, "level")
		if err != nil {
			return nil, err
		}
		workspaceID, err := bridge.Arg[string](p, "workspaceId")
		if err != nil {
			return nil, err
		}
		agent, err := agentOverride(p)
		if err != nil {
			return nil, err
		}

		return deps.RunFromLink(ctx, LinkRunRequest{
			URL: url, JobID: jobID, Level: level, WorkspaceID: workspaceID, Agent: agent,
		})
	})
}

// agentOverride reads the three optional parameters that route a run through an agent's own
// provider and model instead of the per-task cascade.
func agentOverride(p bridge.Params) (*ai.AgentOverride, error) {
	provider, err := bridge.OptionalArg[string](p, "agentProvider")
	if err != nil {
		return nil, err
	}
	model, err := bridge.OptionalArg[string](p, "agentModel")
	if err != nil {
		return nil, err
	}
	prompt, err := bridge.OptionalArg[string](p, "agentPrompt")
	if err != nil {
		return nil, err
	}
	if provider == nil && model == nil && prompt == nil {
		return nil, nil //nolint:nilnil // no override is the ordinary case, not a failure
	}
	return &ai.AgentOverride{
		Provider: optional(provider), Model: optional(model), Prompt: optional(prompt),
	}, nil
}

func optional(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// LinkRunRequest is one review reached by a pasted URL alone.
type LinkRunRequest struct {
	URL   string
	JobID string
	Level string
	// WorkspaceID is the caller's, because there is no project to derive one from — and it is what
	// decides which methodology, contexts and MCP servers the run uses.
	WorkspaceID string
	Agent       *ai.AgentOverride
}

// RunFromLink reviews a pull request with no clone and no project (REVIEW-010).
//
// Nothing is saved: no run, no history row. A run with no project has no project to file itself
// under, and a run with no saved row has nothing for a later re-review to reconcile against — so
// every call re-runs the whole analysis, with no delta banner and no thread reuse.
//
// There is no "nothing changed" short-circuit either: with no previous run there is no head to
// compare, so every call pays the full cost whether or not the pull request moved.
func (d PipelineDeps) RunFromLink(ctx context.Context, request LinkRunRequest) (string, error) {
	started := d.now()

	host, link, err := d.Hosts.HostForLink(ctx, request.URL)
	if err != nil {
		return "", err
	}

	config, err := d.reviewConfig(ctx, request.WorkspaceID)
	if err != nil {
		return "", err
	}

	pull, err := host.Get(ctx, link.Number)
	if err != nil {
		return "", err
	}
	diff, err := host.Diff(ctx, link.Number)
	if err != nil {
		return "", err
	}

	workingDir, err := d.linkReviewWorkspace(link, pull, diff)
	if err != nil {
		return "", err
	}

	result, err := d.AI.Review(ctx, request.JobID, ai.ReviewRequest{
		Title:       pull.Title,
		Description: pull.Description,
		// The no-clone warning rides at index 0 — pushed to second only by an agent's own
		// instructions, which are what the person driving this run asked for.
		Contexts: reviewContexts(config.Contexts, request.Agent, true),
		// Reshaped like any other prompt diff (`GIT-031`): this route once sent the provider's diff
		// whole, and this application found it reviewing its own change.
		Diff: git.RenderTextForPrompt(diff, git.PromptBudgetChars),
		// No checkout means no code to quote around the change, and saying so in three places at
		// once — here, in the context above, and in the level directive — is what makes it true
		// rather than merely stated.
		CodeContext: "",
		WorkingDir:  workingDir,
		Template:    config.Template,
		Level:       request.Level,
		Explorable:  false,
		MCPConfig:   d.mcpConfig(request.WorkspaceID, config.MCPs),
		Agent:       request.Agent,
	})
	if err != nil {
		return "", err
	}

	// The level and the duration alone: there is no coverage to report and nothing was reconciled.
	return result.Text + d.footer(request.Level, started, nil, nil, nil), nil
}
