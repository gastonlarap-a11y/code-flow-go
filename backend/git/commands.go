package git

import (
	"context"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
)

// IdentityResolver answers the commit identity a repository on disk should use (WS-008).
//
// Declared here rather than imported so this package stays independent of workspaces: git needs
// "who commits in this directory" and nothing else about the row that answers it. The implementation
// is `workspaces.Store`, wired in by the composition root.
type IdentityResolver interface {
	ResolveGitIdentity(ctx context.Context, repoPath string) (name, email *string, err error)
}

// Deps is what this feature needs from the composition root.
//
// Emitter is here for the network operations of Phase 3's later half: clone, fetch, pull and push
// stream `git:progress` and `git:done` while they run.
//
// Identity is nil when the database did not open. Commits then fall back to the repository's own
// git config, which is the same path an unregistered repository takes — a degraded but correct
// commit beats refusing to commit at all on an install whose storage is broken.
type Deps struct {
	Emitter  bridge.Emitter
	Identity IdentityResolver
}

// Register adds the git commands ported so far.
//
// The parameter names are the renderer's: `repoPath`, `filePath`, `oid`. Every command takes the
// repository path explicitly rather than holding a "current repository" — the user can have
// several projects open, and a stateful current-repo is how an operation lands in the wrong one.
func Register(r *bridge.Registry, deps Deps) {
	r.Add("is_git_repo", func(ctx context.Context, p bridge.Params) (any, error) {
		path, err := bridge.Arg[string](p, "path")
		if err != nil {
			return nil, err
		}
		return IsRepo(ctx, path), nil
	})

	withRepo := func(name string, run func(context.Context, string, bridge.Params) (any, error)) {
		r.Add(name, func(ctx context.Context, p bridge.Params) (any, error) {
			repo, err := bridge.Arg[string](p, "repoPath")
			if err != nil {
				return nil, err
			}
			return run(ctx, repo, p)
		})
	}

	// ---- status and diffs --------------------------------------------------------------------

	withRepo("get_status", func(ctx context.Context, repo string, _ bridge.Params) (any, error) {
		return Status(ctx, repo)
	})
	withRepo("is_merging", func(ctx context.Context, repo string, _ bridge.Params) (any, error) {
		return IsMerging(ctx, repo)
	})
	withRepo("get_working_diff", func(ctx context.Context, repo string, _ bridge.Params) (any, error) {
		return WorkingDiff(ctx, repo)
	})
	withRepo("get_staged_diff", func(ctx context.Context, repo string, _ bridge.Params) (any, error) {
		return StagedDiff(ctx, repo)
	})
	withRepo("get_commit_diff", func(ctx context.Context, repo string, p bridge.Params) (any, error) {
		oid, err := bridge.Arg[string](p, "oid")
		if err != nil {
			return nil, err
		}
		return CommitDiff(ctx, repo, oid)
	})
	withRepo("list_commit_files", func(ctx context.Context, repo string, p bridge.Params) (any, error) {
		oid, err := bridge.Arg[string](p, "oid")
		if err != nil {
			return nil, err
		}
		return ListCommitFiles(ctx, repo, oid)
	})
	withRepo("get_commit_file_diff", func(ctx context.Context, repo string, p bridge.Params) (any, error) {
		oid, err := bridge.Arg[string](p, "oid")
		if err != nil {
			return nil, err
		}
		filePath, err := bridge.Arg[string](p, "filePath")
		if err != nil {
			return nil, err
		}
		oldPath, err := bridge.OptionalArg[string](p, "oldPath")
		if err != nil {
			return nil, err
		}
		return CommitFileDiff(ctx, repo, oid, filePath, oldPath)
	})

	// ---- history -----------------------------------------------------------------------------

	withRepo("list_commits", func(ctx context.Context, repo string, p bridge.Params) (any, error) {
		allRefs, err := bridge.ArgOr(p, "allRefs", false)
		if err != nil {
			return nil, err
		}
		limit, err := bridge.ArgOr[int64](p, "limit", 0)
		if err != nil {
			return nil, err
		}
		return ListCommits(ctx, repo, allRefs, limit)
	})
	withRepo("list_unpushed_commits", func(ctx context.Context, repo string, _ bridge.Params) (any, error) {
		return ListUnpushedCommits(ctx, repo)
	})

	// ---- stash -------------------------------------------------------------------------------

	withRepo("list_stashes", func(ctx context.Context, repo string, _ bridge.Params) (any, error) {
		return ListStashes(ctx, repo)
	})
	withRepo("stash_save", func(ctx context.Context, repo string, p bridge.Params) (any, error) {
		message, err := bridge.OptionalArg[string](p, "message")
		if err != nil {
			return nil, err
		}
		includeUntracked, err := bridge.ArgOr(p, "includeUntracked", false)
		if err != nil {
			return nil, err
		}
		return nil, StashSave(ctx, repo, message, includeUntracked)
	})
	// apply and pop answer an outcome rather than raising: a conflicted apply is a state the user
	// has to act on, not a failure to report.
	withRepo("stash_apply", func(ctx context.Context, repo string, p bridge.Params) (any, error) {
		index, err := bridge.Arg[int64](p, "index")
		if err != nil {
			return nil, err
		}
		return StashApply(ctx, repo, index)
	})
	withRepo("stash_pop", func(ctx context.Context, repo string, p bridge.Params) (any, error) {
		index, err := bridge.Arg[int64](p, "index")
		if err != nil {
			return nil, err
		}
		return StashPop(ctx, repo, index)
	})
	withRepo("stash_drop", func(ctx context.Context, repo string, p bridge.Params) (any, error) {
		index, err := bridge.Arg[int64](p, "index")
		if err != nil {
			return nil, err
		}
		return nil, StashDrop(ctx, repo, index)
	})
	withRepo("rename_stash", func(ctx context.Context, repo string, p bridge.Params) (any, error) {
		index, err := bridge.Arg[int64](p, "index")
		if err != nil {
			return nil, err
		}
		newMessage, err := bridge.Arg[string](p, "newMessage")
		if err != nil {
			return nil, err
		}
		return nil, RenameStash(ctx, repo, index, newMessage)
	})

	// ---- branches ----------------------------------------------------------------------------

	withRepo("list_branches", func(ctx context.Context, repo string, _ bridge.Params) (any, error) {
		return ListBranches(ctx, repo)
	})
	withRepo("create_branch", func(ctx context.Context, repo string, p bridge.Params) (any, error) {
		name, err := bridge.Arg[string](p, "name")
		if err != nil {
			return nil, err
		}
		startPoint, err := bridge.OptionalArg[string](p, "startPoint")
		if err != nil {
			return nil, err
		}
		return nil, CreateBranch(ctx, repo, name, startPoint)
	})
	withRepo("delete_branch", func(ctx context.Context, repo string, p bridge.Params) (any, error) {
		name, err := bridge.Arg[string](p, "name")
		if err != nil {
			return nil, err
		}
		isRemote, err := bridge.ArgOr(p, "isRemote", false)
		if err != nil {
			return nil, err
		}
		return nil, DeleteBranch(ctx, repo, name, isRemote)
	})
	withRepo("checkout_local_branch", func(ctx context.Context, repo string, p bridge.Params) (any, error) {
		name, err := bridge.Arg[string](p, "name")
		if err != nil {
			return nil, err
		}
		// The CHECKOUT_CONFLICT sentinel travels unwrapped from here to the renderer.
		return nil, CheckoutLocalBranch(ctx, repo, name)
	})
	withRepo("checkout_detached", func(ctx context.Context, repo string, p bridge.Params) (any, error) {
		refname, err := bridge.Arg[string](p, "refname")
		if err != nil {
			return nil, err
		}
		return nil, CheckoutDetached(ctx, repo, refname)
	})
	withRepo("checkout_remote_tracking", func(ctx context.Context, repo string, p bridge.Params) (any, error) {
		remoteBranch, err := bridge.Arg[string](p, "remoteBranch")
		if err != nil {
			return nil, err
		}
		return CheckoutRemoteTracking(ctx, repo, remoteBranch)
	})

	// ---- merge and conflicts -------------------------------------------------------------------

	// merge_branch answers an outcome, conflicts included: the repository is left mid-merge and
	// the renderer opens the conflict panel on it. Nothing here raises for that.
	withRepo("merge_branch", func(ctx context.Context, repo string, p bridge.Params) (any, error) {
		branchName, err := bridge.Arg[string](p, "branchName")
		if err != nil {
			return nil, err
		}
		// No author arguments on the wire: the identity is the workspace's or the repository's.
		name, email, err := deps.resolveAuthor(ctx, repo, nil, nil)
		if err != nil {
			return nil, err
		}
		return MergeBranch(ctx, repo, branchName, name, email)
	})
	withRepo("list_conflicts", func(ctx context.Context, repo string, _ bridge.Params) (any, error) {
		return ListConflicts(ctx, repo)
	})
	withRepo("resolve_conflict_side", func(ctx context.Context, repo string, p bridge.Params) (any, error) {
		relPath, err := bridge.Arg[string](p, "relPath")
		if err != nil {
			return nil, err
		}
		side, err := bridge.Arg[string](p, "side")
		if err != nil {
			return nil, err
		}
		return nil, ResolveConflictSide(ctx, repo, relPath, side)
	})
	withRepo("mark_conflict_resolved", func(ctx context.Context, repo string, p bridge.Params) (any, error) {
		relPath, err := bridge.Arg[string](p, "relPath")
		if err != nil {
			return nil, err
		}
		return nil, MarkConflictResolved(ctx, repo, relPath)
	})
	withRepo("complete_merge", func(ctx context.Context, repo string, p bridge.Params) (any, error) {
		message, err := bridge.Arg[string](p, "message")
		if err != nil {
			return nil, err
		}
		name, email, err := deps.resolveAuthor(ctx, repo, nil, nil)
		if err != nil {
			return nil, err
		}
		return CompleteMerge(ctx, repo, message, name, email)
	})
	withRepo("abort_merge", func(ctx context.Context, repo string, _ bridge.Params) (any, error) {
		return nil, AbortMerge(ctx, repo)
	})

	// ---- AI checkpoints --------------------------------------------------------------------------
	//
	// Creating one is not a command: `05-ai-engines.md` and the repo-wide replace take a checkpoint
	// as part of their own work, so the renderer only ever lists, restores and deletes.

	withRepo("list_ai_checkpoints", func(ctx context.Context, repo string, _ bridge.Params) (any, error) {
		return ListCheckpoints(ctx, repo)
	})
	withRepo("restore_ai_checkpoint", func(ctx context.Context, repo string, p bridge.Params) (any, error) {
		checkpointID, err := bridge.Arg[string](p, "checkpointId")
		if err != nil {
			return nil, err
		}
		return RestoreCheckpoint(ctx, repo, checkpointID)
	})
	withRepo("delete_ai_checkpoint", func(ctx context.Context, repo string, p bridge.Params) (any, error) {
		checkpointID, err := bridge.Arg[string](p, "checkpointId")
		if err != nil {
			return nil, err
		}
		return nil, DeleteCheckpoint(ctx, repo, checkpointID)
	})

	// ---- staging and committing --------------------------------------------------------------

	withRepo("stage_file", func(ctx context.Context, repo string, p bridge.Params) (any, error) {
		filePath, err := bridge.Arg[string](p, "filePath")
		if err != nil {
			return nil, err
		}
		return nil, StageFile(ctx, repo, filePath)
	})
	withRepo("stage_all", func(ctx context.Context, repo string, _ bridge.Params) (any, error) {
		return nil, StageAll(ctx, repo)
	})
	withRepo("unstage_file", func(ctx context.Context, repo string, p bridge.Params) (any, error) {
		filePath, err := bridge.Arg[string](p, "filePath")
		if err != nil {
			return nil, err
		}
		return nil, UnstageFile(ctx, repo, filePath)
	})
	withRepo("unstage_all", func(ctx context.Context, repo string, _ bridge.Params) (any, error) {
		return nil, UnstageAll(ctx, repo)
	})
	withRepo("discard_file_changes", func(ctx context.Context, repo string, p bridge.Params) (any, error) {
		filePath, err := bridge.Arg[string](p, "filePath")
		if err != nil {
			return nil, err
		}
		return nil, DiscardFileChanges(ctx, repo, filePath)
	})
	withRepo("discard_all_changes", func(ctx context.Context, repo string, _ bridge.Params) (any, error) {
		return nil, DiscardAllChanges(ctx, repo)
	})
	withRepo("commit", func(ctx context.Context, repo string, p bridge.Params) (any, error) {
		message, err := bridge.Arg[string](p, "message")
		if err != nil {
			return nil, err
		}
		authorName, err := bridge.OptionalArg[string](p, "authorName")
		if err != nil {
			return nil, err
		}
		authorEmail, err := bridge.OptionalArg[string](p, "authorEmail")
		if err != nil {
			return nil, err
		}
		name, email, err := deps.resolveAuthor(ctx, repo, authorName, authorEmail)
		if err != nil {
			return nil, err
		}
		return CreateCommit(ctx, repo, message, name, email)
	})
	withRepo("reset_to_commit", func(ctx context.Context, repo string, p bridge.Params) (any, error) {
		oid, err := bridge.Arg[string](p, "oid")
		if err != nil {
			return nil, err
		}
		mode, err := bridge.ArgOr(p, "mode", "mixed")
		if err != nil {
			return nil, err
		}
		return nil, ResetToCommit(ctx, repo, oid, mode)
	})

	// ---- the network ------------------------------------------------------------------------------
	//
	// The four that talk to a server, and the only ones that report progress while they run. Each
	// emits `git:progress` per line of output and one `git:done` at the end — including on failure,
	// where the call also rejects (GIT-034).

	network := NewNetwork(deps.Emitter)

	// clone is the one git command with no repository: its whole purpose is that the directory does
	// not exist yet.
	r.Add("git_clone", func(ctx context.Context, p bridge.Params) (any, error) {
		url, err := bridge.Arg[string](p, "url")
		if err != nil {
			return nil, err
		}
		dest, err := bridge.Arg[string](p, "dest")
		if err != nil {
			return nil, err
		}
		return nil, network.Clone(ctx, url, dest)
	})
	withRepo("git_fetch", func(ctx context.Context, repo string, p bridge.Params) (any, error) {
		remote, err := bridge.OptionalArg[string](p, "remoteName")
		if err != nil {
			return nil, err
		}
		return nil, network.Fetch(ctx, repo, remote)
	})
	withRepo("git_pull", func(ctx context.Context, repo string, _ bridge.Params) (any, error) {
		return nil, network.Pull(ctx, repo)
	})
	withRepo("git_push", func(ctx context.Context, repo string, p bridge.Params) (any, error) {
		setUpstream, err := bridge.ArgOr(p, "setUpstream", false)
		if err != nil {
			return nil, err
		}
		return nil, network.Push(ctx, repo, setUpstream)
	})

	// ---- remotes and identity ------------------------------------------------------------------

	withRepo("list_remotes", func(ctx context.Context, repo string, _ bridge.Params) (any, error) {
		return ListRemotes(ctx, repo)
	})
	withRepo("set_remote_url", func(ctx context.Context, repo string, p bridge.Params) (any, error) {
		name, err := bridge.Arg[string](p, "name")
		if err != nil {
			return nil, err
		}
		url, err := bridge.Arg[string](p, "url")
		if err != nil {
			return nil, err
		}
		return nil, SetRemoteURL(ctx, repo, name, url)
	})

	// The identity is global rather than per repository, so these two take no repoPath.
	r.Add("get_git_identity", func(ctx context.Context, _ bridge.Params) (any, error) {
		return GetIdentity(ctx)
	})
	r.Add("set_git_identity", func(ctx context.Context, p bridge.Params) (any, error) {
		name, err := bridge.Arg[string](p, "name")
		if err != nil {
			return nil, err
		}
		email, err := bridge.Arg[string](p, "email")
		if err != nil {
			return nil, err
		}
		return nil, SetIdentity(ctx, name, email)
	})
}

// resolveAuthor decides who a commit-creating command commits as (GIT-028, GIT-036, WS-008).
//
// Three steps, in order: the arguments when the renderer sent both, then the workspace owning the
// project registered at this path, then nothing — which the git layer reads as "use the
// repository's configured signature".
//
// Both halves or neither, at every step. A name without an email would otherwise produce commits
// attributed to the machine's address under someone else's name, which is the kind of thing nobody
// notices until it is in a shared history.
func (d Deps) resolveAuthor(ctx context.Context, repo string, name, email *string) (*string, *string, error) {
	if name != nil && email != nil {
		return name, email, nil
	}
	if d.Identity == nil {
		return nil, nil, nil
	}
	return d.Identity.ResolveGitIdentity(ctx, repo)
}
