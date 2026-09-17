package activity

import (
	"context"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
)

// Deps is what this feature needs from the composition root.
type Deps struct {
	Store *Store
}

// Register adds the seven activity commands.
func Register(r *bridge.Registry, deps Deps) {
	r.Add("list_chat_conversations", func(ctx context.Context, p bridge.Params) (any, error) {
		projectID, err := bridge.Arg[string](p, "projectId")
		if err != nil {
			return nil, err
		}
		// The renderer sends `search: search ?? null`, so an absent needle and an explicit null
		// are the same thing — which is what OptionalArg already treats them as.
		search, err := bridge.OptionalArg[string](p, "search")
		if err != nil {
			return nil, err
		}
		return deps.Store.ListConversations(ctx, projectID, search)
	})

	r.Add("get_chat_conversation", func(ctx context.Context, p bridge.Params) (any, error) {
		projectID, sessionID, err := twoStrings(p, "projectId", "sessionId")
		if err != nil {
			return nil, err
		}
		return deps.Store.GetConversation(ctx, projectID, sessionID)
	})

	r.Add("delete_chat_conversation", func(ctx context.Context, p bridge.Params) (any, error) {
		projectID, sessionID, err := twoStrings(p, "projectId", "sessionId")
		if err != nil {
			return nil, err
		}
		return nil, deps.Store.DeleteConversation(ctx, projectID, sessionID)
	})

	r.Add("rename_chat_conversation", func(ctx context.Context, p bridge.Params) (any, error) {
		projectID, sessionID, err := twoStrings(p, "projectId", "sessionId")
		if err != nil {
			return nil, err
		}
		title, err := bridge.Arg[string](p, "title")
		if err != nil {
			return nil, err
		}
		return nil, deps.Store.RenameConversation(ctx, projectID, sessionID, title)
	})

	r.Add("list_job_history", func(ctx context.Context, p bridge.Params) (any, error) {
		projectID, err := bridge.Arg[string](p, "projectId")
		if err != nil {
			return nil, err
		}
		return deps.Store.ListJobs(ctx, projectID)
	})

	r.Add("rename_job_history_entry", func(ctx context.Context, p bridge.Params) (any, error) {
		id, label, err := twoStrings(p, "id", "label")
		if err != nil {
			return nil, err
		}
		return nil, deps.Store.RenameJob(ctx, id, label)
	})

	r.Add("delete_job_history_entry", func(ctx context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		return nil, deps.Store.DeleteJob(ctx, id)
	})
}

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
