package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Undo for AI runs (GIT-022 … GIT-025).
//
// Before an AI action that can write to the working tree, the tree is snapshotted into a commit
// parked outside refs/heads, so "undo what the agent just did" is a real operation. Two properties
// drive the design and both are load-bearing:
//
//   - # It must not disturb git's own state
//
// Nothing lands on a branch, HEAD never moves, and the staging area is left exactly as the user had
// it — `git status` reads the same before and after. Where libgit2 attached a fresh in-memory index
// to its own repository handle, this uses GIT_INDEX_FILE pointing at a temporary file outside the
// repository: git never opens `.git/index`, and it takes its lock on the temporary one, so a
// snapshot cannot even collide with a `git add` the user runs in their own terminal.
//
//   - # Restoring is per file
//
// Rolling the whole tree back would also discard whatever the user typed while the agent worked, so
// the caller sees which paths differ and only those are put back. An undo button, not a time
// machine — which is why nothing here goes near `checkout`, `reset` or HEAD.

// checkpointRefPrefix is the ref namespace, VERBATIM (GIT-023). Refs written by 2.7.x live here and
// this version has to find them.
const checkpointRefPrefix = "refs/codeflow/checkpoints/"

// maxCheckpoints is how many a repository keeps.
//
// Snapshots are cheap because git deduplicates every unchanged blob, but they are refs that would
// otherwise pile up forever and keep their objects from ever being collected — and nobody undoes
// the fortieth-most-recent AI run.
const maxCheckpoints = 20

// checkpointFallbackIdentity is who a snapshot is attributed to when the repository has none.
//
// VERBATIM, and it must be forced rather than left to git: `git commit-tree` does **not** fail
// without a configured identity, it invents one from the OS account — measured, and it wrote the
// machine user's real full name into the commit. libgit2 failed instead, which is what made 2.x
// fall back to this pair.
var checkpointFallbackIdentity = []string{
	"GIT_AUTHOR_NAME=CodeFlow",
	"GIT_AUTHOR_EMAIL=codeflow@local",
	"GIT_COMMITTER_NAME=CodeFlow",
	"GIT_COMMITTER_EMAIL=codeflow@local",
}

// Checkpoint is one snapshot and what restoring it would put back right now.
type Checkpoint struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	CreatedAt int64  `json:"created_at"`
	// ChangedPaths is computed fresh against the current working tree on every call, so it is
	// always "what restoring right now would touch" and never a record of what changed when the
	// checkpoint was taken.
	ChangedPaths []string `json:"changed_paths"`
}

// CreateCheckpoint snapshots the working tree and returns the new checkpoint's id (GIT-022).
//
// kind is a stable action key such as `chat` or `fix-finding`, never a sentence: the UI is
// bilingual, so the wording belongs to the frontend's translations and only this key crosses the
// boundary.
func CreateCheckpoint(ctx context.Context, repo, kind string) (string, error) {
	runner := NewRunner(repo)

	index, cleanup, err := snapshotIndex(ctx, runner)
	if err != nil {
		return "", err
	}
	defer cleanup()

	tree, err := runGit(ctx, runner, index.env(), "write-tree")
	if err != nil {
		return "", err
	}

	// Parented on HEAD so the checkpoint reads as a commit on top of the current state; a
	// repository with no commits gets a parentless one rather than no protection at all.
	args := []string{"commit-tree", tree, "-m", kind}
	if head, err := headCommit(ctx, runner); err == nil {
		args = append(args, "-p", head)
	}

	identity, err := checkpointIdentity(ctx, runner)
	if err != nil {
		return "", err
	}
	commit, err := runGit(ctx, runner, identity, args...)
	if err != nil {
		return "", err
	}

	// Two checkpoints taken in the same second are told apart only by the random suffix — the id is
	// not required to sort by time on its own.
	id := fmt.Sprintf("%d-%s", time.Now().Unix(), strings.ReplaceAll(uuid.NewString(), "-", "")[:8])

	if _, err := runGit(ctx, runner, nil, "update-ref", checkpointRefPrefix+id, commit); err != nil {
		return "", err
	}

	pruneCheckpoints(ctx, runner)
	return id, nil
}

// ListCheckpoints returns every checkpoint, newest first (GIT-023).
func ListCheckpoints(ctx context.Context, repo string) ([]Checkpoint, error) {
	runner := NewRunner(repo)

	refs, err := checkpointRefs(ctx, runner)
	if err != nil {
		return nil, err
	}

	checkpoints := make([]Checkpoint, 0, len(refs))
	if len(refs) == 0 {
		// Nothing to compare against, and building the snapshot index would hash the whole
		// repository for no answer.
		return checkpoints, nil
	}

	// One snapshot index for the whole call rather than one per checkpoint: it describes the
	// working tree, not any particular checkpoint, so all twenty diffs can share it. Building it
	// per checkpoint would hash every file in the repository twenty times.
	index, cleanup, err := snapshotIndex(ctx, runner)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	for _, ref := range refs {
		changed, err := changedPaths(ctx, runner, index, ref.commit)
		if err != nil {
			return nil, err
		}
		checkpoints = append(checkpoints, Checkpoint{
			ID:           ref.id,
			Kind:         ref.subject,
			CreatedAt:    ref.createdAt,
			ChangedPaths: changed,
		})
	}

	// Stable, so checkpoints sharing a second keep the ref order git listed them in.
	sort.SliceStable(checkpoints, func(i, j int) bool {
		return checkpoints[i].CreatedAt > checkpoints[j].CreatedAt
	})
	return checkpoints, nil
}

// RestoreCheckpoint writes every differing path back to its snapshotted content and deletes the
// ones the run created, returning what it touched (GIT-024).
//
// Blobs are written to the working tree directly rather than checked out, so the index and HEAD are
// untouched: a file that was staged before the restore stays staged, now with stale content,
// exactly as any manual edit after staging would leave it.
func RestoreCheckpoint(ctx context.Context, repo, checkpointID string) ([]string, error) {
	runner := NewRunner(repo)

	commit, err := readCheckpoint(ctx, runner, checkpointID)
	if err != nil {
		return nil, err
	}

	index, cleanup, err := snapshotIndex(ctx, runner)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	paths, err := changedPaths(ctx, runner, index, commit)
	if err != nil {
		return nil, err
	}

	for _, relative := range paths {
		target := filepath.Join(repo, filepath.FromSlash(relative))

		content, found, err := blobAt(ctx, runner, commit, relative)
		if err != nil {
			return nil, err
		}
		if !found {
			// Absent from the snapshot: the run created it, so undoing means removing it. The
			// error is ignored exactly as 2.x ignored it — a path the user has already deleted
			// themselves is the state we were trying to reach.
			_ = os.Remove(target)
			continue
		}

		// 0755/0644 rather than platform.DirPerm/FilePerm: these are the user's own source files,
		// not CodeFlow's data. Tightening a repository's permissions because an undo happened to
		// run is not this feature's business.
		//
		// The executable bit is not reapplied, matching 2.x: a file that exists keeps its mode
		// because a truncating write does not change it, and one the snapshot has to recreate comes
		// back non-executable.
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil { //nolint:gosec // G301: see above
			return nil, fmt.Errorf("%s: %w", relative, err)
		}
		if err := os.WriteFile(target, content, 0o644); err != nil { //nolint:gosec // G306: see above
			return nil, fmt.Errorf("%s: %w", relative, err)
		}
	}
	return paths, nil
}

// DeleteCheckpoint forgets a checkpoint (GIT-025).
//
// Deleting one that is not there is not an error — `git update-ref -d` exits 0 for a missing ref,
// measured, which is the same forgiving behaviour 2.x had. Its objects stay in the database until
// git's own gc reaps them.
func DeleteCheckpoint(ctx context.Context, repo, checkpointID string) error {
	_, err := runGit(ctx, NewRunner(repo), nil, "update-ref", "-d", checkpointRefPrefix+checkpointID)
	return err
}

// RemoveCheckpointIfUnchanged deletes a checkpoint only when nothing differs from it — the run it
// protected changed nothing (GIT-025).
//
// Unlike DeleteCheckpoint, a missing id **is** an error here. The asymmetry is deliberate: this one
// is called automatically after an AI run, where a missing checkpoint means something went wrong,
// while the other is a user pressing delete.
//
// Not a registered command: `05-ai-engines.md`'s post-run cleanup calls it so a run that touched
// nothing leaves no undo entry for the user to puzzle over.
func RemoveCheckpointIfUnchanged(ctx context.Context, repo, checkpointID string) (bool, error) {
	runner := NewRunner(repo)

	commit, err := readCheckpoint(ctx, runner, checkpointID)
	if err != nil {
		return false, err
	}

	index, cleanup, err := snapshotIndex(ctx, runner)
	if err != nil {
		return false, err
	}
	defer cleanup()

	paths, err := changedPaths(ctx, runner, index, commit)
	if err != nil {
		return false, err
	}
	if len(paths) > 0 {
		return false, nil
	}
	return true, DeleteCheckpoint(ctx, repo, checkpointID)
}

// ---- the snapshot index -------------------------------------------------------------------------

// tempIndex is a git index file living outside the repository.
type tempIndex struct{ path string }

func (t tempIndex) env() []string { return []string{"GIT_INDEX_FILE=" + t.path} }

// snapshotIndex builds an index describing the working tree as it is right now, without going near
// the real one (GIT-022).
//
// HEAD's tree is the base and `git add -A` then brings every difference in — staged, unstaged or
// untracked, it makes no difference, and ignored files stay out. `-A` rather than named paths
// because recording a *deletion* is half the point: a snapshot that missed a file the user had
// removed would resurrect it on restore.
//
// The file is deliberately outside the working tree. Inside it, `git add -A` would find the index
// itself and snapshot it.
func snapshotIndex(ctx context.Context, runner Runner) (tempIndex, func(), error) {
	dir, err := os.MkdirTemp("", "codeflow-checkpoint-")
	if err != nil {
		return tempIndex{}, func() {}, fmt.Errorf("checkpoint index: %w", err)
	}
	index := tempIndex{path: filepath.Join(dir, "index")}
	cleanup := func() { _ = os.RemoveAll(dir) }

	// No HEAD yet: an empty base, so the snapshot is "everything on disk" rather than nothing.
	if _, err := headCommit(ctx, runner); err == nil {
		if _, err := runGit(ctx, runner, index.env(), "read-tree", "HEAD"); err != nil {
			cleanup()
			return tempIndex{}, func() {}, err
		}
	}

	if _, err := runGit(ctx, runner, index.env(), "add", "-A"); err != nil {
		cleanup()
		return tempIndex{}, func() {}, err
	}
	return index, cleanup, nil
}

// changedPaths lists the paths whose current content differs from a checkpoint's tree, sorted and
// deduplicated (GIT-024).
//
// `--no-renames` is not a detail: this list feeds the restore, which needs a rename's delete **and**
// its add as two entries. A single rename entry would drop the old path from the set and the
// restore would leave the file where the agent moved it.
func changedPaths(ctx context.Context, runner Runner, index tempIndex, commit string) ([]string, error) {
	out, err := runGitRecord(ctx, runner, index.env(),
		"diff", "--cached", "-z", "--no-renames", "--name-only", commit)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool, 8)
	paths := make([]string, 0, 8)
	for _, path := range splitNUL(out) {
		if seen[path] {
			continue
		}
		seen[path] = true
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

// blobAt reads a path's content out of a commit's tree, reporting whether the tree had one there.
//
// `cat-file blob` rather than `show`: it fails rather than printing a listing when the path is a
// directory in that tree, which is the same distinction libgit2's `Target is Blob` made. Raw bytes,
// because a source file's CRLF endings and missing final newline both have to survive an undo.
func blobAt(ctx context.Context, runner Runner, commit, relative string) ([]byte, bool, error) {
	result, err := runner.RunRaw(ctx, "cat-file", "blob", commit+":"+relative)
	if err != nil {
		return nil, false, err
	}
	if result.Failed() {
		return nil, false, nil
	}
	return result.Stdout, true, nil
}

// ---- refs ---------------------------------------------------------------------------------------

// checkpointRef is one ref in the namespace, already peeled.
type checkpointRef struct {
	id        string
	commit    string
	subject   string
	createdAt int64
}

// checkpointRefs enumerates the namespace.
//
// A ref whose target cannot be peeled to a commit is silently excluded rather than reported: it is
// not a checkpoint any more, and failing the whole list because one ref is broken would take the
// user's other nineteen undos with it.
func checkpointRefs(ctx context.Context, runner Runner) ([]checkpointRef, error) {
	const format = "%(refname)%00%(objectname)%00%(contents:subject)%00%(committerdate:unix)"

	result, err := runner.Run(ctx, "for-each-ref", "--format="+format, checkpointRefPrefix)
	if err != nil {
		return nil, err
	}
	if result.Failed() {
		return nil, fmt.Errorf("git for-each-ref failed: %s", result.Detail())
	}

	refs := make([]checkpointRef, 0, maxCheckpoints)
	for line := range strings.SplitSeq(result.Stdout, "\n") {
		fields := strings.Split(line, "\x00")
		if len(fields) < 4 {
			continue
		}
		createdAt, err := strconv.ParseInt(strings.TrimSpace(fields[3]), 10, 64)
		if err != nil {
			continue
		}
		refs = append(refs, checkpointRef{
			id:        strings.TrimPrefix(fields[0], checkpointRefPrefix),
			commit:    fields[1],
			subject:   fields[2],
			createdAt: createdAt,
		})
	}
	return refs, nil
}

// readCheckpoint resolves one checkpoint's commit.
func readCheckpoint(ctx context.Context, runner Runner, checkpointID string) (string, error) {
	result, err := runner.Run(ctx, "rev-parse", "--verify", "--quiet",
		checkpointRefPrefix+checkpointID+"^{commit}")
	if err != nil {
		return "", err
	}
	if result.Failed() {
		// VERBATIM: the renderer shows this one to the user.
		return "", fmt.Errorf("checkpoint '%s' no longer exists", checkpointID)
	}
	return strings.TrimSpace(result.Stdout), nil
}

// pruneCheckpoints drops the oldest checkpoints past maxCheckpoints, best-effort (GIT-023).
//
// Every error is swallowed: failing to prune is never a reason to fail the snapshot the user is
// actually protected by. It runs only as a side effect of creating one — deleting a checkpoint by
// hand never triggers it, and there is no scheduled prune.
func pruneCheckpoints(ctx context.Context, runner Runner) {
	refs, err := checkpointRefs(ctx, runner)
	if err != nil || len(refs) <= maxCheckpoints {
		return
	}

	sort.SliceStable(refs, func(i, j int) bool { return refs[i].createdAt > refs[j].createdAt })
	for _, ref := range refs[maxCheckpoints:] {
		_, _ = runGit(ctx, runner, nil, "update-ref", "-d", checkpointRefPrefix+ref.id)
	}
}

// checkpointIdentity is the environment a snapshot commit is made with.
//
// Nothing when the repository has an identity of its own, the CodeFlow fallback when it does not —
// **both** halves have to be configured, because that is when libgit2 built a signature and when it
// did not. A repository with no identity must still be protected, so this cannot be allowed to fail
// the way committing does.
func checkpointIdentity(ctx context.Context, runner Runner) ([]string, error) {
	for _, key := range []string{"user.name", "user.email"} {
		result, err := runner.Run(ctx, "config", "--get", key)
		if err != nil {
			return nil, err
		}
		if result.Failed() || strings.TrimSpace(result.Stdout) == "" {
			return checkpointFallbackIdentity, nil
		}
	}
	return nil, nil
}

// ---- running ------------------------------------------------------------------------------------

// runGit runs a checkpoint command and returns its single-line output, failing loudly.
//
// These are plumbing commands whose failures are all programming or filesystem errors rather than
// states to classify, so they share one shape instead of repeating the same six lines.
func runGit(ctx context.Context, runner Runner, env []string, args ...string) (string, error) {
	out, err := runGitRecord(ctx, runner, env, args...)
	return strings.TrimSpace(out), err
}

func runGitRecord(ctx context.Context, runner Runner, env []string, args ...string) (string, error) {
	result, err := runner.RunWithEnv(ctx, env, args...)
	if err != nil {
		return "", err
	}
	if result.Failed() {
		return "", fmt.Errorf("git %s failed: %s", args[0], result.Detail())
	}
	return result.Stdout, nil
}
