package apiclient

import (
	"context"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
)

// The store commands: the tree, the environments, the history, the jar and the two file readers.
//
// Thin forwarders on purpose. The behaviour that is worth anything lives in the store — the cycle
// guard, the dense renumbering, the two-pass duplicate, the per-workspace history cap — and a
// handler that re-implemented any of it would be a second place for it to be wrong.

// Deps is what this feature needs from the composition root.
type Deps struct {
	Store *Store
}

// RegisterStore adds the commands that read and write the workbench's own data.
func RegisterStore(r *bridge.Registry, deps Deps) {
	registerTree(r, deps)
	registerEnvironments(r, deps)
	registerHistory(r, deps)
	registerCookies(r, deps)
	registerFiles(r)
}

func registerTree(r *bridge.Registry, deps Deps) {
	r.Add("api_load_tree", func(ctx context.Context, p bridge.Params) (any, error) {
		workspaceID, err := bridge.Arg[string](p, "workspaceId")
		if err != nil {
			return nil, err
		}
		return deps.Store.LoadTree(ctx, workspaceID)
	})

	r.Add("api_create_collection", func(ctx context.Context, p bridge.Params) (any, error) {
		workspaceID, err := bridge.Arg[string](p, "workspaceId")
		if err != nil {
			return nil, err
		}
		name, err := bridge.Arg[string](p, "name")
		if err != nil {
			return nil, err
		}
		return deps.Store.CreateCollection(ctx, workspaceID, name)
	})

	r.Add("api_update_collection", func(ctx context.Context, p bridge.Params) (any, error) {
		collection, err := bridge.Arg[Collection](p, "collection")
		if err != nil {
			return nil, err
		}
		return nil, deps.Store.UpdateCollection(ctx, collection)
	})

	r.Add("api_delete_collection", func(ctx context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		return nil, deps.Store.DeleteCollection(ctx, id)
	})

	r.Add("api_duplicate_collection", func(ctx context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		return deps.Store.DuplicateCollection(ctx, id)
	})

	r.Add("api_create_folder", func(ctx context.Context, p bridge.Params) (any, error) {
		collectionID, err := bridge.Arg[string](p, "collectionId")
		if err != nil {
			return nil, err
		}
		parentID, err := bridge.OptionalArg[string](p, "parentId")
		if err != nil {
			return nil, err
		}
		name, err := bridge.Arg[string](p, "name")
		if err != nil {
			return nil, err
		}
		return deps.Store.CreateFolder(ctx, collectionID, parentID, name)
	})

	r.Add("api_update_folder", func(ctx context.Context, p bridge.Params) (any, error) {
		folder, err := bridge.Arg[Folder](p, "folder")
		if err != nil {
			return nil, err
		}
		return nil, deps.Store.UpdateFolder(ctx, folder)
	})

	r.Add("api_delete_folder", func(ctx context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		return nil, deps.Store.DeleteFolder(ctx, id)
	})

	r.Add("api_create_request", func(ctx context.Context, p bridge.Params) (any, error) {
		collectionID, err := bridge.Arg[string](p, "collectionId")
		if err != nil {
			return nil, err
		}
		folderID, err := bridge.OptionalArg[string](p, "folderId")
		if err != nil {
			return nil, err
		}
		name, err := bridge.Arg[string](p, "name")
		if err != nil {
			return nil, err
		}
		protocol, err := bridge.Arg[string](p, "protocol")
		if err != nil {
			return nil, err
		}
		spec, err := bridge.Arg[string](p, "spec")
		if err != nil {
			return nil, err
		}
		return deps.Store.CreateRequest(ctx, collectionID, folderID, name, protocol, spec)
	})

	r.Add("api_update_request", func(ctx context.Context, p bridge.Params) (any, error) {
		request, err := bridge.Arg[Request](p, "request")
		if err != nil {
			return nil, err
		}
		return nil, deps.Store.UpdateRequest(ctx, request)
	})

	r.Add("api_delete_request", func(ctx context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		return nil, deps.Store.DeleteRequest(ctx, id)
	})

	r.Add("api_duplicate_request", func(ctx context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		return deps.Store.DuplicateRequest(ctx, id)
	})

	// Every parameter is read before the move is attempted: a missing one is a disagreement between
	// the two sides, and reporting it as "a folder cannot be moved inside itself" would send the
	// reader looking at their tree instead of at the call.
	r.Add("api_move_node", func(ctx context.Context, p bridge.Params) (any, error) {
		kind, err := bridge.Arg[string](p, "kind")
		if err != nil {
			return nil, err
		}
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		collectionID, err := bridge.Arg[string](p, "collectionId")
		if err != nil {
			return nil, err
		}
		parentID, err := bridge.OptionalArg[string](p, "parentId")
		if err != nil {
			return nil, err
		}
		index, err := bridge.Arg[int64](p, "index")
		if err != nil {
			return nil, err
		}
		return nil, deps.Store.MoveNode(ctx, kind, id, collectionID, parentID, index)
	})

	r.Add("api_reorder_collections", func(ctx context.Context, p bridge.Params) (any, error) {
		workspaceID, err := bridge.Arg[string](p, "workspaceId")
		if err != nil {
			return nil, err
		}
		ids, err := bridge.Arg[[]string](p, "ids")
		if err != nil {
			return nil, err
		}
		return nil, deps.Store.ReorderCollections(ctx, workspaceID, ids)
	})
}

func registerEnvironments(r *bridge.Registry, deps Deps) {
	r.Add("api_list_environments", func(ctx context.Context, p bridge.Params) (any, error) {
		workspaceID, err := bridge.Arg[string](p, "workspaceId")
		if err != nil {
			return nil, err
		}
		return deps.Store.ListEnvironments(ctx, workspaceID)
	})

	r.Add("api_create_environment", func(ctx context.Context, p bridge.Params) (any, error) {
		workspaceID, err := bridge.Arg[string](p, "workspaceId")
		if err != nil {
			return nil, err
		}
		name, err := bridge.Arg[string](p, "name")
		if err != nil {
			return nil, err
		}
		return deps.Store.CreateEnvironment(ctx, workspaceID, name)
	})

	r.Add("api_update_environment", func(ctx context.Context, p bridge.Params) (any, error) {
		environment, err := bridge.Arg[Environment](p, "environment")
		if err != nil {
			return nil, err
		}
		return nil, deps.Store.UpdateEnvironment(ctx, environment)
	})

	// A no-op on a Globals row rather than a refusal: the renderer hides the button there, so
	// reaching this is a state that should not be, and an error would blame a user who cannot have
	// meant it.
	r.Add("api_delete_environment", func(ctx context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		return nil, deps.Store.DeleteEnvironment(ctx, id)
	})

	r.Add("api_duplicate_environment", func(ctx context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		return deps.Store.DuplicateEnvironment(ctx, id)
	})
}

func registerHistory(r *bridge.Registry, deps Deps) {
	r.Add("api_list_history", func(ctx context.Context, p bridge.Params) (any, error) {
		workspaceID, err := bridge.Arg[string](p, "workspaceId")
		if err != nil {
			return nil, err
		}
		limit, err := bridge.Arg[int64](p, "limit")
		if err != nil {
			return nil, err
		}
		return deps.Store.ListHistory(ctx, workspaceID, limit)
	})

	// The workspace comes from the entry itself, and is what the trim is counted within.
	r.Add("api_add_history", func(ctx context.Context, p bridge.Params) (any, error) {
		entry, err := bridge.Arg[HistoryEntry](p, "entry")
		if err != nil {
			return nil, err
		}
		return nil, deps.Store.AddHistory(ctx, entry)
	})

	r.Add("api_delete_history", func(ctx context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		return nil, deps.Store.DeleteHistory(ctx, id)
	})

	r.Add("api_clear_history", func(ctx context.Context, p bridge.Params) (any, error) {
		workspaceID, err := bridge.Arg[string](p, "workspaceId")
		if err != nil {
			return nil, err
		}
		return nil, deps.Store.ClearHistory(ctx, workspaceID)
	})
}

func registerCookies(r *bridge.Registry, deps Deps) {
	r.Add("api_list_cookies", func(ctx context.Context, p bridge.Params) (any, error) {
		workspaceID, err := bridge.Arg[string](p, "workspaceId")
		if err != nil {
			return nil, err
		}
		return deps.Store.ListCookies(ctx, workspaceID)
	})

	r.Add("api_upsert_cookie", func(ctx context.Context, p bridge.Params) (any, error) {
		cookie, err := bridge.Arg[Cookie](p, "cookie")
		if err != nil {
			return nil, err
		}
		return nil, deps.Store.UpsertCookie(ctx, cookie)
	})

	r.Add("api_delete_cookie", func(ctx context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		return nil, deps.Store.DeleteCookie(ctx, id)
	})

	r.Add("api_clear_cookies", func(ctx context.Context, p bridge.Params) (any, error) {
		workspaceID, err := bridge.Arg[string](p, "workspaceId")
		if err != nil {
			return nil, err
		}
		return nil, deps.Store.ClearCookies(ctx, workspaceID)
	})
}

// registerFiles adds the two readers. They take no store, so they answer on an install whose
// database did not open — which is also when somebody is most likely to be importing a collection.
func registerFiles(r *bridge.Registry) {
	r.Add("api_read_file_base64", func(_ context.Context, p bridge.Params) (any, error) {
		path, err := bridge.Arg[string](p, "path")
		if err != nil {
			return nil, err
		}
		return ReadFileBase64(path)
	})

	r.Add("api_read_text_file", func(_ context.Context, p bridge.Params) (any, error) {
		path, err := bridge.Arg[string](p, "path")
		if err != nil {
			return nil, err
		}
		return ReadTextFile(path)
	})
}
