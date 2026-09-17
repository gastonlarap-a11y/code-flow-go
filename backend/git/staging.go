package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Staging, committing and discarding.
//
// The subtleties here are all about *which* tree an operation restores from, and they are the kind
// of thing a user only notices when it costs them work.

// StageFile stages one path (GIT-013).
//
// A path that no longer exists on disk is staged as a deletion instead: `git add` on a missing
// file fails, and the user ticking a deleted file in the Changes panel means "record the deletion".
func StageFile(ctx context.Context, repo, filePath string) error {
	runner := NewRunner(repo)

	args := []string{"add", "--", filePath}
	if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(filePath))); os.IsNotExist(err) {
		args = []string{"rm", "--cached", "--", filePath}
	}

	result, err := runner.RunWrite(ctx, args...)
	if err != nil {
		return err
	}
	if result.Failed() {
		return fmt.Errorf("git add failed: %s", result.Detail())
	}
	return nil
}

// StageAll stages everything, including deletions and untracked files.
func StageAll(ctx context.Context, repo string) error {
	result, err := NewRunner(repo).RunWrite(ctx, "add", "-A")
	if err != nil {
		return err
	}
	if result.Failed() {
		return fmt.Errorf("git add failed: %s", result.Detail())
	}
	return nil
}

// UnstageFile removes one path from the index, leaving the working tree alone.
func UnstageFile(ctx context.Context, repo, filePath string) error {
	result, err := NewRunner(repo).RunWrite(ctx, "reset", "-q", "--", filePath)
	if err != nil {
		return err
	}
	if result.Failed() {
		return fmt.Errorf("git reset failed: %s", result.Detail())
	}
	return nil
}

// UnstageAll clears the index back to HEAD.
func UnstageAll(ctx context.Context, repo string) error {
	result, err := NewRunner(repo).RunWrite(ctx, "reset", "-q")
	if err != nil {
		return err
	}
	if result.Failed() {
		return fmt.Errorf("git reset failed: %s", result.Detail())
	}
	return nil
}

// DiscardFileChanges throws away a file's working-tree changes.
//
// It restores from the **index**, not from HEAD. That is libgit2's behaviour and it matters: a
// file that was staged and then modified again discards back to the staged version, not to the
// last commit. Restoring from HEAD instead would silently destroy staged work the user had
// deliberately kept.
func DiscardFileChanges(ctx context.Context, repo, filePath string) error {
	result, err := NewRunner(repo).RunWrite(ctx, "checkout", "--", filePath)
	if err != nil {
		return err
	}
	if result.Failed() {
		return fmt.Errorf("git checkout failed: %s", result.Detail())
	}
	return nil
}

// DiscardAllChanges throws away every working-tree change and every untracked file (GIT-012).
//
// Deliberately assembled from the status rather than done with `git reset --hard`:
//
//   - Conflicted paths are skipped. A hard reset would resolve a merge by discarding it, which is
//     not what "discard my changes" means while a merge is in progress.
//   - Staged-only changes are left alone, for the same reason DiscardFileChanges restores from the
//     index: the user staged them on purpose.
//   - Untracked files are deleted individually and their empty parents removed, which `git
//     checkout` does not do at all.
func DiscardAllChanges(ctx context.Context, repo string) error {
	status, err := Status(ctx, repo)
	if err != nil {
		return err
	}

	tracked := make([]string, 0, len(status.Unstaged))
	for _, entry := range status.Unstaged {
		// A rename's path is "old → new" for display; discarding one is the rename itself, which
		// lives in the index and is therefore not a working-tree change to throw away.
		if strings.Contains(entry.Path, " → ") {
			continue
		}
		tracked = append(tracked, entry.Path)
	}

	if len(tracked) > 0 {
		args := append([]string{"checkout", "--"}, tracked...)
		result, err := NewRunner(repo).RunWrite(ctx, args...)
		if err != nil {
			return err
		}
		if result.Failed() {
			return fmt.Errorf("git checkout failed: %s", result.Detail())
		}
	}

	for _, entry := range status.Untracked {
		full := filepath.Join(repo, filepath.FromSlash(entry.Path))
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			// The first failure aborts and names the path, as 2.x did: continuing would leave the
			// user with a half-discarded tree and no idea which half.
			return fmt.Errorf("%s: %w", entry.Path, err)
		}
		removeEmptyParents(repo, filepath.Dir(full))
	}
	return nil
}

// removeEmptyParents walks up from a deleted file removing directories that are now empty,
// stopping at the repository root.
//
// Without it, discarding a new `pkg/sub/file.go` leaves `pkg/sub/` behind — which then shows up as
// nothing at all in git (it does not track directories) and as clutter in the editor.
func removeEmptyParents(repo, dir string) {
	root := filepath.Clean(repo)

	for {
		cleaned := filepath.Clean(dir)
		if cleaned == root || !strings.HasPrefix(cleaned, root+string(os.PathSeparator)) {
			return
		}
		// Ignored: a non-empty directory is the normal stopping condition, not a failure.
		if err := os.Remove(cleaned); err != nil {
			return
		}
		dir = filepath.Dir(cleaned)
	}
}

// CreateCommit records the staged changes and returns the new commit's id (GIT-028).
//
// Named for libgit2's own CreateCommit, and not `Commit`, because that name belongs to the history
// entry this package already returns from ListCommits.
//
// Two things are deliberate and both were true of libgit2:
//
//   - `--no-verify`. libgit2 never ran hooks, so a repository with a `pre-commit` hook committed
//     fine from 2.x and would suddenly start failing here without it. Measured: a failing hook
//     blocks a plain `git commit` and does not block `--no-verify`.
//   - The author identity is also the **committer**. libgit2's CreateCommit took one signature for
//     both, so a commit made from CodeFlow has matching author and committer; `--author` alone
//     would leave the committer as whatever the machine's config says.
func CreateCommit(ctx context.Context, repo, message string, authorName, authorEmail *string) (string, error) {
	runner := NewRunner(repo)

	args := []string{"commit", "--no-verify", "-m", message}
	var env []string
	if authorName != nil && *authorName != "" && authorEmail != nil && *authorEmail != "" {
		args = append(args, "--author", fmt.Sprintf("%s <%s>", *authorName, *authorEmail))
		env = []string{
			"GIT_COMMITTER_NAME=" + *authorName,
			"GIT_COMMITTER_EMAIL=" + *authorEmail,
		}
	}

	result, err := runner.RunWithEnv(ctx, env, args...)
	if err != nil {
		return "", err
	}
	if result.Failed() {
		return "", fmt.Errorf("git commit failed: %s", result.Detail())
	}

	head, err := runner.Run(ctx, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if head.Failed() {
		return "", fmt.Errorf("git rev-parse failed: %s", head.Detail())
	}
	return strings.TrimSpace(head.Stdout), nil
}

// ResetToCommit moves HEAD (GIT-002).
//
// An unrecognised mode becomes `--mixed`, which is git's own default and 2.x's behaviour. Refusing
// instead would turn a renderer typo into a dead button; defaulting to `--hard` would destroy work.
func ResetToCommit(ctx context.Context, repo, oid, mode string) error {
	flag := "--mixed"
	switch mode {
	case "soft":
		flag = "--soft"
	case "hard":
		flag = "--hard"
	}

	result, err := NewRunner(repo).RunWrite(ctx, "reset", flag, oid)
	if err != nil {
		return err
	}
	if result.Failed() {
		return fmt.Errorf("git reset failed: %s", result.Detail())
	}
	return nil
}

// IsMerging reports whether a merge is in progress (GIT-019).
//
// `MERGE_HEAD`, not "are there conflicts": a merge with every conflict resolved is still a merge
// waiting to be committed, and a conflicted stash apply has conflicts without being one.
func IsMerging(ctx context.Context, repo string) (bool, error) {
	result, err := NewRunner(repo).Run(ctx, "rev-parse", "-q", "--verify", "MERGE_HEAD")
	if err != nil {
		return false, err
	}
	return !result.Failed(), nil
}

// Remote is one configured remote.
type Remote struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// ListRemotes enumerates the repository's remotes.
func ListRemotes(ctx context.Context, repo string) ([]Remote, error) {
	result, err := NewRunner(repo).Run(ctx, "remote", "-v")
	if err != nil {
		return nil, err
	}
	if result.Failed() {
		return nil, fmt.Errorf("git remote failed: %s", result.Detail())
	}

	remotes := make([]Remote, 0, 2)
	seen := make(map[string]bool, 2)

	// `git remote -v` prints each remote twice, once for fetch and once for push. The UI shows one
	// row per remote, so the second is dropped rather than shown as a duplicate.
	for _, line := range strings.Split(result.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || seen[fields[0]] {
			continue
		}
		seen[fields[0]] = true
		remotes = append(remotes, Remote{Name: fields[0], URL: fields[1]})
	}
	return remotes, nil
}

// SetRemoteURL points a remote somewhere else.
//
// Both the fetch and the push URL, because git keeps them separately and a repository where only
// one was changed pulls from the old host and pushes to the new one — which is the kind of thing
// nobody notices until a push goes to the wrong place.
func SetRemoteURL(ctx context.Context, repo, name, url string) error {
	runner := NewRunner(repo)

	for _, args := range [][]string{
		{"remote", "set-url", name, url},
		{"remote", "set-url", "--push", name, url},
	} {
		result, err := runner.RunWrite(ctx, args...)
		if err != nil {
			return err
		}
		if result.Failed() {
			return fmt.Errorf("git remote set-url failed: %s", result.Detail())
		}
	}
	return nil
}

// Identity is the global git identity.
type Identity struct {
	Name  *string `json:"name"`
	Email *string `json:"email"`
}

// GetIdentity reads the global user.name and user.email.
//
// A missing key is null, not an error: an unconfigured git is the state a fresh machine is in, and
// the Settings screen exists precisely to fix it.
func GetIdentity(ctx context.Context) (Identity, error) {
	identity := Identity{}

	for _, field := range []struct {
		key  string
		into **string
	}{{"user.name", &identity.Name}, {"user.email", &identity.Email}} {
		result, err := RunOutsideRepo(ctx, []string{"LC_ALL=C"}, "config", "--global", "--get", field.key)
		if err != nil {
			return identity, err
		}
		if result.Failed() {
			continue
		}
		if value := strings.TrimSpace(result.Stdout); value != "" {
			*field.into = &value
		}
	}
	return identity, nil
}

// SetIdentity writes the global user.name and user.email.
func SetIdentity(ctx context.Context, name, email string) error {
	for _, field := range []struct{ key, value string }{
		{"user.name", name}, {"user.email", email},
	} {
		result, err := RunOutsideRepo(ctx, []string{"LC_ALL=C"}, "config", "--global", field.key, field.value)
		if err != nil {
			return err
		}
		if result.Failed() {
			return fmt.Errorf("git config failed: %s", result.Detail())
		}
	}
	return nil
}
