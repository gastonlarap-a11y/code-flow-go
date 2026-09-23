package git

import (
	"context"
	"fmt"
	"strings"
)

// What a pull-request review asks of git: resolve a ref, list what changed between two, and diff a
// branch into the shape a prompt reads (`GIT-030`, consumed by `07-review-pipeline.md`).

// ErrBranchNotFound names the ref that did not resolve and what to do about it.
func branchNotFound(name string) error {
	return fmt.Errorf("Could not find branch '%s' locally or on origin — try fetching this repository first.", name) //nolint:staticcheck,err113 // ST1005: VERBATIM
}

// ResolveSHA resolves a branch name or ref to its commit (GIT-030).
//
// The order is the rule: a name that already starts with `refs/` is used verbatim and nothing else
// is tried; otherwise `origin/{name}`, then `refs/remotes/origin/{name}`, then the bare local
// branch — first that peels to a commit wins.
//
// **The remote-tracking branch is preferred over a same-named local one on purpose.** A stale local
// branch is exactly what makes an up-to-date pull request diff come back empty, and that failure
// looks like "the PR has no changes" rather than like a stale checkout.
func ResolveSHA(ctx context.Context, repo, name string) (string, error) {
	runner := NewRunner(repo)

	for _, candidate := range resolutionCandidates(name) {
		result, err := runner.Run(ctx, "rev-parse", "--verify", "--quiet", candidate+"^{commit}")
		if err != nil {
			return "", err
		}
		if !result.Failed() {
			if sha := strings.TrimSpace(result.Stdout); sha != "" {
				return sha, nil
			}
		}
	}
	return "", branchNotFound(name)
}

// resolutionCandidates is GIT-030's order, and only ever names `origin`.
func resolutionCandidates(name string) []string {
	if strings.HasPrefix(name, "refs/") {
		return []string{name}
	}
	return []string{"origin/" + name, "refs/remotes/origin/" + name, name}
}

// ChangedFilesBetween lists the files that differ between two refs.
//
// Used to tell a re-review which files it actually looked at: a finding in a file this run never
// diffed is not resolved by the model's silence about it (`REVIEW-028`).
func ChangedFilesBetween(ctx context.Context, repo, from, to string) ([]string, error) {
	fromSHA, err := ResolveSHA(ctx, repo, from)
	if err != nil {
		return nil, err
	}
	toSHA, err := ResolveSHA(ctx, repo, to)
	if err != nil {
		return nil, err
	}

	result, err := NewRunner(repo).Run(ctx, "diff", "--name-only", fromSHA+"..."+toSHA)
	if err != nil {
		return nil, err
	}
	if result.Failed() {
		return nil, fmt.Errorf("git diff --name-only failed: %s", result.Detail())
	}

	files := make([]string, 0, 16)
	for line := range strings.SplitSeq(result.Stdout, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			files = append(files, trimmed)
		}
	}
	return files, nil
}

// BranchDiffFiles is what a branch brings in, parsed, with whole-file context.
//
// Three dots, like `BranchDiff`: against the merge base rather than the other tip, so what comes
// back is what *this* branch changed and not what the target branch did meanwhile.
//
// Whole-file context (`-U1000000`) is what `GIT-033` needs to quote the code around each change
// without touching the filesystem, and `GIT-031` trims it back down before any of it reaches a
// model.
func BranchDiffFiles(ctx context.Context, repo, base, head string) ([]FileDiff, error) {
	baseSHA, err := ResolveSHA(ctx, repo, base)
	if err != nil {
		return nil, err
	}
	headSHA, err := ResolveSHA(ctx, repo, head)
	if err != nil {
		return nil, err
	}

	result, err := NewRunner(repo).Run(ctx, "diff", "--no-color", "-U1000000", baseSHA+"..."+headSHA)
	if err != nil {
		return nil, err
	}
	if result.Failed() {
		return nil, fmt.Errorf("git diff failed: %s", result.Detail())
	}
	return parseUnifiedDiff(result.Stdout), nil
}

// PullRequestHeadRefspec is the one fetch a GitHub review makes for itself.
//
// A pull request's head can live in a fork, where it is not on `origin` under any branch name at
// all — so it is fetched into a local tracking ref of its own. Best-effort at the call site: a
// review of what is already local beats no review.
func PullRequestHeadRefspec(prID int64) (refspec, localRef string) {
	return fmt.Sprintf("+refs/pull/%d/head:refs/remotes/origin/codeflow-pr-%d", prID, prID),
		fmt.Sprintf("refs/remotes/origin/codeflow-pr-%d", prID)
}
