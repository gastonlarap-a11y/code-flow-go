package repomap

import (
	"context"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
)

// Deps is what the map's commands need.
type Deps struct {
	Service *Service
}

// Register adds the map's six commands (REPOMAP-007). Every one takes the repository's folder as
// `rootPath`, and none needs the database, so they survive a failed storage stage.
func Register(r *bridge.Registry, deps Deps) {
	service := deps.Service

	r.Add("repomap_project", func(ctx context.Context, p bridge.Params) (any, error) {
		snap, build, err := snapshotFor(ctx, service, p)
		if err != nil {
			return nil, err
		}
		return MapView{Graph: snap.ProjectGraph(ProjectNodes), Build: build}, nil
	})

	r.Add("repomap_folder", func(ctx context.Context, p bridge.Params) (any, error) {
		focus, err := bridge.ArgOr(p, "focus", "")
		if err != nil {
			return nil, err
		}
		snap, build, err := snapshotFor(ctx, service, p)
		if err != nil {
			return nil, err
		}
		return MapView{Graph: snap.FolderGraph(focus), Build: build}, nil
	})

	r.Add("repomap_file", func(ctx context.Context, p bridge.Params) (any, error) {
		path, err := bridge.Arg[string](p, "path")
		if err != nil {
			return nil, err
		}
		snap, _, err := snapshotFor(ctx, service, p)
		if err != nil {
			return nil, err
		}
		return snap.File(path), nil // a typed nil: the renderer reads null as "not in the map"
	})

	r.Add("repomap_search", func(ctx context.Context, p bridge.Params) (any, error) {
		query, err := bridge.Arg[string](p, "query")
		if err != nil {
			return nil, err
		}
		snap, _, err := snapshotFor(ctx, service, p)
		if err != nil {
			return nil, err
		}
		return snap.Search(query, SearchLimit), nil
	})

	r.Add("repomap_usages", func(ctx context.Context, p bridge.Params) (any, error) {
		name, err := bridge.Arg[string](p, "name")
		if err != nil {
			return nil, err
		}
		path, err := bridge.ArgOr(p, "path", "")
		if err != nil {
			return nil, err
		}
		snap, _, err := snapshotFor(ctx, service, p)
		if err != nil {
			return nil, err
		}
		return snap.Usages(name, path, UsagesLimit), nil
	})

	r.Add("repomap_most_used", func(ctx context.Context, p bridge.Params) (any, error) {
		snap, _, err := snapshotFor(ctx, service, p)
		if err != nil {
			return nil, err
		}
		return snap.MostUsed(MostUsedLimit), nil
	})
}

func snapshotFor(ctx context.Context, service *Service, p bridge.Params) (*Snapshot, Build, error) {
	rootPath, err := bridge.Arg[string](p, "rootPath")
	if err != nil {
		return nil, Build{}, err
	}
	return service.Snapshot(ctx, rootPath)
}
