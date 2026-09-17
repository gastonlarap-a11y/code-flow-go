package files

import (
	"context"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/bridge/jsonwire"
)

// Deps is what this feature needs from the composition root.
type Deps struct {
	// Opener hands a path to the operating system. Nil outside a window — a headless smoke test
	// has no Finder to open anything in.
	Opener Opener
	// Checkpointer protects a repo-wide replace. Nil means the replace still runs and reports a
	// null checkpoint id, which is 2.x's behaviour when the snapshot failed.
	Checkpointer Checkpointer
}

// Register adds the file operations, the palette's file list, search and replace.
func Register(r *bridge.Registry, deps Deps) {
	withRepo := func(name string, run func(context.Context, string, bridge.Params) (any, error)) {
		r.Add(name, func(ctx context.Context, p bridge.Params) (any, error) {
			repo, err := bridge.Arg[string](p, "repoPath")
			if err != nil {
				return nil, err
			}
			return run(ctx, repo, p)
		})
	}

	// ---- the file tree ---------------------------------------------------------------------------

	withRepo("list_dir", func(_ context.Context, repo string, p bridge.Params) (any, error) {
		subPath, err := bridge.OptionalArg[string](p, "subPath")
		if err != nil {
			return nil, err
		}
		return ListDir(repo, subPath)
	})
	withRepo("read_file_text", func(_ context.Context, repo string, p bridge.Params) (any, error) {
		relPath, err := bridge.Arg[string](p, "relPath")
		if err != nil {
			return nil, err
		}
		return ReadFileText(repo, relPath)
	})
	withRepo("write_file_text", func(_ context.Context, repo string, p bridge.Params) (any, error) {
		relPath, err := bridge.Arg[string](p, "relPath")
		if err != nil {
			return nil, err
		}
		content, err := bridge.Arg[string](p, "content")
		if err != nil {
			return nil, err
		}
		return nil, WriteFileText(repo, relPath, content)
	})

	// The one file operation with no repoPath: the save dialog already authorised the destination.
	r.Add("write_file_bytes", func(_ context.Context, p bridge.Params) (any, error) {
		path, err := bridge.Arg[string](p, "path")
		if err != nil {
			return nil, err
		}
		// A JSON array of numbers, not base64: that is what the renderer builds from a Uint8Array.
		contents, err := bridge.Arg[jsonwire.ByteArray](p, "contents")
		if err != nil {
			return nil, err
		}
		return nil, WriteFileBytes(path, contents)
	})

	withRepo("move_path", func(_ context.Context, repo string, p bridge.Params) (any, error) {
		fromRel, err := bridge.Arg[string](p, "fromRel")
		if err != nil {
			return nil, err
		}
		destDir, err := bridge.ArgOr(p, "destDir", "")
		if err != nil {
			return nil, err
		}
		return MovePath(repo, fromRel, destDir)
	})
	withRepo("create_dir", func(_ context.Context, repo string, p bridge.Params) (any, error) {
		relPath, err := bridge.Arg[string](p, "relPath")
		if err != nil {
			return nil, err
		}
		return nil, CreateDir(repo, relPath)
	})
	withRepo("create_file", func(_ context.Context, repo string, p bridge.Params) (any, error) {
		relPath, err := bridge.Arg[string](p, "relPath")
		if err != nil {
			return nil, err
		}
		return nil, CreateFile(repo, relPath)
	})

	// ---- handing things to the operating system --------------------------------------------------

	withRepo("open_in_default_app", func(_ context.Context, repo string, p bridge.Params) (any, error) {
		relPath, err := bridge.Arg[string](p, "relPath")
		if err != nil {
			return nil, err
		}
		return nil, OpenInDefaultApp(deps.Opener, repo, relPath)
	})
	r.Add("reveal_in_file_manager", func(_ context.Context, p bridge.Params) (any, error) {
		path, err := bridge.Arg[string](p, "path")
		if err != nil {
			return nil, err
		}
		return nil, RevealInFileManager(deps.Opener, path)
	})
	r.Add("open_in_vscode", func(ctx context.Context, p bridge.Params) (any, error) {
		path, err := bridge.Arg[string](p, "path")
		if err != nil {
			return nil, err
		}
		return nil, OpenInVSCode(ctx, path)
	})

	// ---- the palette, search and replace ---------------------------------------------------------

	withRepo("list_repo_files", func(ctx context.Context, repo string, _ bridge.Params) (any, error) {
		return ListRepoFiles(ctx, repo)
	})
	withRepo("search_repo", func(ctx context.Context, repo string, p bridge.Params) (any, error) {
		query, err := bridge.Arg[string](p, "query")
		if err != nil {
			return nil, err
		}
		options, err := bridge.Arg[SearchOptions](p, "options")
		if err != nil {
			return nil, err
		}
		maxResults, err := bridge.ArgOr[int64](p, "maxResults", 500)
		if err != nil {
			return nil, err
		}
		return SearchRepo(ctx, repo, query, options, maxResults)
	})
	withRepo("replace_in_repo", func(ctx context.Context, repo string, p bridge.Params) (any, error) {
		query, err := bridge.Arg[string](p, "query")
		if err != nil {
			return nil, err
		}
		replacement, err := bridge.Arg[string](p, "replacement")
		if err != nil {
			return nil, err
		}
		options, err := bridge.Arg[SearchOptions](p, "options")
		if err != nil {
			return nil, err
		}
		onlyPath, err := bridge.OptionalArg[string](p, "onlyPath")
		if err != nil {
			return nil, err
		}
		return ReplaceInRepo(ctx, deps.Checkpointer, repo, query, replacement, options, onlyPath)
	})
}

// RegisterWatcher adds the two watcher commands.
//
// Separate from Register because the watcher owns state that outlives any one call — the commands
// start and stop something, rather than doing something — and because a caller with no window
// (the smoke test) wants the file operations without starting any watchers.
func RegisterWatcher(r *bridge.Registry, registry *WatcherRegistry) {
	r.Add("start_watching", func(_ context.Context, p bridge.Params) (any, error) {
		repoPath, err := bridge.Arg[string](p, "repoPath")
		if err != nil {
			return nil, err
		}
		// Not the call's context: the watch outlives the call by design, and the context Wails
		// hands a command is cancelled the moment it returns.
		return nil, registry.Start(repoPath)
	})
	r.Add("stop_watching", func(_ context.Context, p bridge.Params) (any, error) {
		repoPath, err := bridge.Arg[string](p, "repoPath")
		if err != nil {
			return nil, err
		}
		registry.Stop(repoPath)
		return nil, nil
	})
}
