package workspaces

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/ai"
	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/platform"
)

// Deps is what this feature needs from the composition root.
type Deps struct {
	Store *Store
	Paths platform.Paths
}

// promptDefaults maps a prompt kind to the embedded text that backs it. The same two kinds the
// storage backfill seeds; kept in one place so the two cannot drift.
var promptDefaults = map[string]string{
	"review_standard": ai.PromptPRReviewStandard,
	"pr_description":  ai.PromptPRDescription,
}

// Register adds the workspace, project, settings, prompt, context, agent and MCP commands.
//
// Parameter names are the renderer's own, camelCase: `workspaceId`, not `workspace_id`. The
// *results* are snake_case, because `types/domain.ts` declares them that way. That asymmetry is
// 2.x's and it is deliberate to keep — the renderer sends camelCase arguments and reads snake_case
// fields, and changing either half would mean touching the 125 files this port exists to leave
// alone.
func Register(r *bridge.Registry, deps Deps) {
	registerWorkspaces(r, deps)
	registerProjects(r, deps)
	registerSettings(r, deps)
	registerPrompts(r, deps)
	registerContexts(r, deps)
}

func registerWorkspaces(r *bridge.Registry, deps Deps) {
	r.Add("list_workspaces", func(ctx context.Context, _ bridge.Params) (any, error) {
		return deps.Store.ListWorkspaces(ctx)
	})

	r.Add("create_workspace", func(ctx context.Context, p bridge.Params) (any, error) {
		name, err := bridge.Arg[string](p, "name")
		if err != nil {
			return nil, err
		}
		icon, err := bridge.ArgOr(p, "icon", "folder")
		if err != nil {
			return nil, err
		}
		colour, err := bridge.ArgOr(p, "color", "#6366f1")
		if err != nil {
			return nil, err
		}

		// A new workspace is seeded with the current built-in prompts, in the same transaction
		// that creates it — the backfill only covers workspaces that already existed.
		seeds := make(map[string]string, len(promptDefaults))
		for kind, prompt := range promptDefaults {
			seeds[kind] = ai.Prompt(prompt)
		}
		return deps.Store.CreateWorkspace(ctx, name, icon, colour, seeds)
	})

	r.Add("rename_workspace", func(ctx context.Context, p bridge.Params) (any, error) {
		id, name, err := twoStrings(p, "id", "name")
		if err != nil {
			return nil, err
		}
		return nil, notFoundAsError("workspace", id, deps.Store.RenameWorkspace(ctx, id, name))
	})

	r.Add("update_workspace_color", func(ctx context.Context, p bridge.Params) (any, error) {
		id, colour, err := twoStrings(p, "id", "color")
		if err != nil {
			return nil, err
		}
		return nil, notFoundAsError("workspace", id, deps.Store.SetWorkspaceColor(ctx, id, colour))
	})

	r.Add("update_workspace_git_identity", func(ctx context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		name, err := bridge.OptionalArg[string](p, "name")
		if err != nil {
			return nil, err
		}
		email, err := bridge.OptionalArg[string](p, "email")
		if err != nil {
			return nil, err
		}
		return nil, notFoundAsError("workspace", id, deps.Store.SetWorkspaceGitIdentity(ctx, id, name, email))
	})

	r.Add("delete_workspace", func(ctx context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		return nil, notFoundAsError("workspace", id, deps.Store.DeleteWorkspace(ctx, id))
	})
}

func registerProjects(r *bridge.Registry, deps Deps) {
	r.Add("list_projects", func(ctx context.Context, p bridge.Params) (any, error) {
		workspaceID, err := bridge.Arg[string](p, "workspaceId")
		if err != nil {
			return nil, err
		}
		return deps.Store.ListProjects(ctx, workspaceID)
	})

	// Answers `Project | null`, so a missing project is nil rather than an error: the renderer
	// asks for one it may no longer have, and branches on the null.
	r.Add("get_project", func(ctx context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		project, err := deps.Store.GetProject(ctx, id)
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return project, nil
	})

	r.Add("create_project", func(ctx context.Context, p bridge.Params) (any, error) {
		var input NewProject
		raw, ok := p.Raw("input")
		if !ok {
			return nil, bridge.MissingParameterError("input")
		}
		if err := unmarshalInput(raw, &input); err != nil {
			return nil, err
		}
		if input.WorkspaceID == "" {
			return nil, bridge.MissingParameterError("input.workspace_id")
		}
		return deps.Store.CreateProject(ctx, input)
	})

	r.Add("update_project_color", func(ctx context.Context, p bridge.Params) (any, error) {
		id, colour, err := twoStrings(p, "id", "color")
		if err != nil {
			return nil, err
		}
		return nil, notFoundAsError("project", id, deps.Store.SetProjectColor(ctx, id, colour))
	})

	r.Add("move_project_to_workspace", func(ctx context.Context, p bridge.Params) (any, error) {
		id, workspaceID, err := twoStrings(p, "id", "workspaceId")
		if err != nil {
			return nil, err
		}
		return nil, notFoundAsError("project", id, deps.Store.MoveProjectToWorkspace(ctx, id, workspaceID))
	})

	r.Add("delete_project", func(ctx context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		return nil, notFoundAsError("project", id, deps.Store.DeleteProject(ctx, id))
	})
}

func registerSettings(r *bridge.Registry, deps Deps) {
	r.Add("get_setting", func(ctx context.Context, p bridge.Params) (any, error) {
		key, err := bridge.Arg[string](p, "key")
		if err != nil {
			return nil, err
		}
		return deps.Store.GetSetting(ctx, key)
	})

	r.Add("set_setting", func(ctx context.Context, p bridge.Params) (any, error) {
		key, value, err := twoStrings(p, "key", "value")
		if err != nil {
			return nil, err
		}
		return nil, deps.Store.SetSetting(ctx, key, value)
	})

	// Where a clone lands when the user has not chosen somewhere else. The setting wins; the
	// fallback is {base}/repos, which start-up has already created.
	r.Add("default_clone_dir", func(ctx context.Context, _ bridge.Params) (any, error) {
		configured, err := deps.Store.GetSetting(ctx, "default_clone_dir")
		if err != nil {
			return nil, err
		}
		if configured != nil && *configured != "" {
			return *configured, nil
		}
		return filepath.Clean(deps.Paths.Repos()), nil
	})
}

func registerPrompts(r *bridge.Registry, deps Deps) {
	// Returns the built-in text for a kind, which is what the Settings screen shows as the
	// "reset to default" preview.
	r.Add("default_workspace_prompt", func(_ context.Context, p bridge.Params) (any, error) {
		kind, err := bridge.Arg[string](p, "kind")
		if err != nil {
			return nil, err
		}
		return defaultPrompt(kind), nil
	})

	// A stored prompt that is empty **once trimmed** is treated exactly like a missing row, and
	// both fall back to the built-in default. That is how the UI implements "restore default":
	// saving a blank is an ordinary upsert, not a delete.
	r.Add("get_workspace_prompt", func(ctx context.Context, p bridge.Params) (any, error) {
		workspaceID, kind, err := twoStrings(p, "workspaceId", "kind")
		if err != nil {
			return nil, err
		}
		stored, err := deps.Store.GetWorkspacePrompt(ctx, workspaceID, kind)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(stored) != "" {
			return stored, nil
		}
		return defaultPrompt(kind), nil
	})

	r.Add("set_workspace_prompt", func(ctx context.Context, p bridge.Params) (any, error) {
		workspaceID, kind, err := twoStrings(p, "workspaceId", "kind")
		if err != nil {
			return nil, err
		}
		content, err := bridge.Arg[string](p, "content")
		if err != nil {
			return nil, err
		}
		return nil, deps.Store.SetWorkspacePrompt(ctx, workspaceID, kind, content)
	})
}

func registerContexts(r *bridge.Registry, deps Deps) {
	r.Add("list_review_contexts", func(ctx context.Context, p bridge.Params) (any, error) {
		workspaceID, err := bridge.Arg[string](p, "workspaceId")
		if err != nil {
			return nil, err
		}
		return deps.Store.ListReviewContexts(ctx, workspaceID)
	})

	r.Add("upsert_review_context", func(ctx context.Context, p bridge.Params) (any, error) {
		id, err := bridge.OptionalArg[string](p, "id")
		if err != nil {
			return nil, err
		}
		workspaceID, name, err := twoStrings(p, "workspaceId", "name")
		if err != nil {
			return nil, err
		}
		content, err := bridge.ArgOr(p, "content", "")
		if err != nil {
			return nil, err
		}
		enabled, err := bridge.ArgOr(p, "enabled", true)
		if err != nil {
			return nil, err
		}
		return deps.Store.UpsertReviewContext(ctx, id, workspaceID, name, content, enabled)
	})

	r.Add("delete_review_context", func(ctx context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		return nil, notFoundAsError("review context", id, deps.Store.DeleteReviewContext(ctx, id))
	})

	r.Add("list_workspace_agents", func(ctx context.Context, p bridge.Params) (any, error) {
		workspaceID, err := bridge.Arg[string](p, "workspaceId")
		if err != nil {
			return nil, err
		}
		return deps.Store.ListAgents(ctx, workspaceID)
	})

	r.Add("upsert_workspace_agent", func(ctx context.Context, p bridge.Params) (any, error) {
		id, err := bridge.OptionalArg[string](p, "id")
		if err != nil {
			return nil, err
		}
		agent := Agent{}
		var err2 error
		if agent.WorkspaceID, err2 = bridge.Arg[string](p, "workspaceId"); err2 != nil {
			return nil, err2
		}
		if agent.Name, err2 = bridge.Arg[string](p, "name"); err2 != nil {
			return nil, err2
		}
		for _, field := range []struct {
			name string
			into *string
		}{
			{"role", &agent.Role}, {"provider", &agent.Provider},
			{"model", &agent.Model}, {"prompt", &agent.Prompt},
		} {
			if *field.into, err2 = bridge.ArgOr(p, field.name, ""); err2 != nil {
				return nil, err2
			}
		}
		if agent.Enabled, err2 = bridge.ArgOr(p, "enabled", true); err2 != nil {
			return nil, err2
		}
		return deps.Store.UpsertAgent(ctx, id, agent)
	})

	r.Add("delete_workspace_agent", func(ctx context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		return nil, notFoundAsError("agent", id, deps.Store.DeleteAgent(ctx, id))
	})

	r.Add("list_workspace_mcps", func(ctx context.Context, p bridge.Params) (any, error) {
		workspaceID, err := bridge.Arg[string](p, "workspaceId")
		if err != nil {
			return nil, err
		}
		return deps.Store.ListMCPs(ctx, workspaceID)
	})

	r.Add("upsert_workspace_mcp", func(ctx context.Context, p bridge.Params) (any, error) {
		id, err := bridge.OptionalArg[string](p, "id")
		if err != nil {
			return nil, err
		}
		mcp := MCP{}
		var err2 error
		if mcp.WorkspaceID, err2 = bridge.Arg[string](p, "workspaceId"); err2 != nil {
			return nil, err2
		}
		if mcp.Name, err2 = bridge.Arg[string](p, "name"); err2 != nil {
			return nil, err2
		}
		if mcp.Command, err2 = bridge.Arg[string](p, "command"); err2 != nil {
			return nil, err2
		}
		for _, field := range []struct {
			name string
			into *string
		}{{"args", &mcp.Args}, {"env", &mcp.Env}} {
			if *field.into, err2 = bridge.ArgOr(p, field.name, ""); err2 != nil {
				return nil, err2
			}
		}
		if mcp.Enabled, err2 = bridge.ArgOr(p, "enabled", true); err2 != nil {
			return nil, err2
		}
		return deps.Store.UpsertMCP(ctx, id, mcp)
	})

	r.Add("delete_workspace_mcp", func(ctx context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		return nil, notFoundAsError("MCP server", id, deps.Store.DeleteMCP(ctx, id))
	})
}

// twoStrings reads two required string parameters, reporting the first missing one by name.
func twoStrings(p bridge.Params, first, second string) (string, string, error) {
	a, err := bridge.Arg[string](p, first)
	if err != nil {
		return "", "", err
	}
	b, err := bridge.Arg[string](p, second)
	if err != nil {
		return "", "", err
	}
	return a, b, nil
}

// defaultPrompt resolves a prompt kind to its built-in text.
//
// An unrecognised kind is **not an error**: 2.x answers the review methodology for anything it
// does not know, and `sdd_stages` with an empty string — that one is a text store with no built-in
// default, because the guide itself is static content in the renderer and never persisted.
// Returning an error here instead would turn an unknown kind into a red banner where the user
// expects an editor.
func defaultPrompt(kind string) string {
	if kind == "sdd_stages" {
		return ""
	}
	if prompt, known := promptDefaults[kind]; known {
		return ai.Prompt(prompt)
	}
	return ai.Prompt(ai.PromptPRReviewStandard)
}

// notFoundAsError turns the store's sentinel into a message naming what was missing. The renderer
// shows it verbatim, so "workspace w1 no longer exists" beats "not found".
func notFoundAsError(kind, id string, err error) error {
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("%s %s no longer exists", kind, id)
	}
	return err
}
