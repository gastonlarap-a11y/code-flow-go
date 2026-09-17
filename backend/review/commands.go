package review

import (
	"context"
	"errors"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
)

// Deps is what this feature needs from the composition root.
type Deps struct {
	Store *Store
}

// RegisterStore adds the seven review-run history commands.
//
// Named RegisterStore rather than Register because the review *pipeline* — running a review,
// reconciling it against the previous one, posting it — arrives in Phase 5 and will add its own
// Register. These seven only read and edit what is already saved, which is why they can land with
// storage rather than waiting for the engines.
func RegisterStore(r *bridge.Registry, deps Deps) {
	r.Add("list_review_runs", func(ctx context.Context, p bridge.Params) (any, error) {
		workspaceID, err := bridge.Arg[string](p, "workspaceId")
		if err != nil {
			return nil, err
		}
		return deps.Store.ListRuns(ctx, workspaceID)
	})

	// Answers `ReviewRunDetail | null`: the renderer asks for a run it may have just deleted in
	// another window, and branches on the null.
	r.Add("get_review_run", func(ctx context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		run, err := deps.Store.GetRun(ctx, id)
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return run, nil
	})

	// `estado` and `motivo` keep their Spanish names all the way through: they are the field names
	// inside the stored findings JSON, which 2.x wrote and the renderer reads.
	r.Add("mark_review_finding", func(ctx context.Context, p bridge.Params) (any, error) {
		runID, err := bridge.Arg[string](p, "runId")
		if err != nil {
			return nil, err
		}
		findingID, err := bridge.Arg[string](p, "findingId")
		if err != nil {
			return nil, err
		}
		estado, err := bridge.Arg[string](p, "estado")
		if err != nil {
			return nil, err
		}
		motivo, err := bridge.OptionalArg[string](p, "motivo")
		if err != nil {
			return nil, err
		}
		return nil, deps.Store.MarkFinding(ctx, runID, findingID, estado, motivo)
	})

	r.Add("delete_review_run", func(ctx context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		return nil, deps.Store.DeleteRun(ctx, id)
	})

	r.Add("delete_review_runs_for_pr", func(ctx context.Context, p bridge.Params) (any, error) {
		projectID, err := bridge.Arg[string](p, "projectId")
		if err != nil {
			return nil, err
		}
		prID, err := bridge.Arg[int64](p, "prId")
		if err != nil {
			return nil, err
		}
		return nil, deps.Store.DeleteRunsForPR(ctx, projectID, prID)
	})

	r.Add("purge_workspace_review_runs", func(ctx context.Context, p bridge.Params) (any, error) {
		workspaceID, err := bridge.Arg[string](p, "workspaceId")
		if err != nil {
			return nil, err
		}
		return nil, deps.Store.PurgeWorkspace(ctx, workspaceID)
	})

	// Returns how many runs were written, which is what the toast reports.
	r.Add("export_review_runs", func(ctx context.Context, p bridge.Params) (any, error) {
		workspaceID, err := bridge.Arg[string](p, "workspaceId")
		if err != nil {
			return nil, err
		}
		destDir, err := bridge.Arg[string](p, "destDir")
		if err != nil {
			return nil, err
		}
		id, err := bridge.OptionalArg[string](p, "id")
		if err != nil {
			return nil, err
		}
		return deps.Store.ExportRuns(ctx, workspaceID, id, destDir)
	})
}
