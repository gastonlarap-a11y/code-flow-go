package git

import (
	"context"
	"fmt"
	"strings"
)

// BranchContribution is everything a branch has changed relative to where it left its base, in one
// comparison (GIT-039).
//
// The obvious implementation — `BranchDiffFiles` concatenated with `WorkingDiff` — is wrong, and
// wrong in a way that shows: a file touched in a commit of the branch *and* again uncommitted
// appears twice, and a model handed the same file twice reports the same finding twice. So this is
// the merge base compared against the **working tree**, which git does in one command: naming a
// single commit diffs it against the tree, staged content included.
//
// `HEAD` rather than a named branch, as in 2.x: the point of this diff is the working tree, and a
// detached or just-branched `HEAD` still has one.
func BranchContribution(ctx context.Context, repo, baseRef string) ([]FileDiff, error) {
	runner := NewRunner(repo)

	base, err := branchMergeBase(ctx, runner, baseRef)
	if err != nil {
		return nil, err
	}

	result, err := runner.Run(ctx, append(append([]string{"diff"}, fullFileArgs...), base)...)
	if err != nil {
		return nil, err
	}
	if result.Failed() {
		return nil, fmt.Errorf("git diff %s failed: %s", base, result.Detail())
	}
	diffs := parseUnifiedDiff(result.Stdout)

	// Untracked files are part of the contribution: LibGit2Sharp's `DiffTargets.WorkingDirectory`
	// implies `IncludeUntracked`, so a file the branch adds and has not staged was always in this
	// diff. git's own `diff` leaves it out, so it is rendered separately, exactly as `WorkingDiff`
	// does.
	untracked, err := untrackedDiffs(ctx, runner)
	if err != nil {
		return nil, err
	}
	return append(diffs, untracked...), nil
}

// branchMergeBase resolves the base branch by `GIT-030`'s order and answers where it parted from
// `HEAD`.
//
// Both failures are reported rather than degraded: a repository with no commits and two branches
// with no common ancestor each produce a diff that would silently read as "this branch changed
// everything", which is the most misleading answer a review could be given.
func branchMergeBase(ctx context.Context, runner Runner, baseRef string) (string, error) {
	baseSHA, err := ResolveSHA(ctx, runner.repo, baseRef)
	if err != nil {
		return "", err
	}

	head, err := runner.Run(ctx, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil {
		return "", err
	}
	if head.Failed() || strings.TrimSpace(head.Stdout) == "" {
		return "", errNoCommits
	}

	result, err := runner.Run(ctx, "merge-base", baseSHA, strings.TrimSpace(head.Stdout))
	if err != nil {
		return "", err
	}
	if result.Failed() || strings.TrimSpace(result.Stdout) == "" {
		return "", fmt.Errorf("no merge base between '%s' and HEAD", baseRef) //nolint:err113 // names the two refs the caller passed
	}
	return strings.TrimSpace(result.Stdout), nil
}

// errNoCommits is what an empty repository answers. A branch has contributed nothing when there is
// nothing at all.
var errNoCommits = fmt.Errorf("this repository has no commits yet") //nolint:err113 // a leaf state, matched by nobody
