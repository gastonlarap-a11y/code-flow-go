package git

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/sentinel"
)

// Branch is one local or remote branch as the switcher shows it.
type Branch struct {
	Name     string  `json:"name"`
	IsHead   bool    `json:"is_head"`
	IsRemote bool    `json:"is_remote"`
	Upstream *string `json:"upstream"`
	Ahead    int64   `json:"ahead"`
	Behind   int64   `json:"behind"`
	Target   *string `json:"target"`
}

// ListBranches enumerates local and remote branches (GIT-007).
//
// `%(upstream:track,nobracket)` rather than `%(ahead-behind:%(upstream))`: format atoms **cannot
// nest**, and the nested form fails outright with `fatal: failed to find '%(upstream'` — measured,
// not assumed. The track field gives "ahead 1", "behind 2", "ahead 1, behind 2", "gone", or the
// empty string when a branch is in sync, and parsing those five shapes is the whole job.
func ListBranches(ctx context.Context, repo string) ([]Branch, error) {
	const format = "%(refname)%00%(objectname)%00%(HEAD)%00%(upstream:short)%00%(upstream:track,nobracket)"

	result, err := NewRunner(repo).Run(ctx, "for-each-ref", "--format="+format,
		"refs/heads", "refs/remotes")
	if err != nil {
		return nil, err
	}
	if result.Failed() {
		return nil, fmt.Errorf("git for-each-ref failed: %s", result.Detail())
	}

	branches := make([]Branch, 0, 16)
	for _, line := range strings.Split(result.Stdout, "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\x00")
		if len(fields) < 5 {
			continue
		}

		refname, target, head, upstream, track := fields[0], fields[1], fields[2], fields[3], fields[4]

		// `refs/remotes/origin/HEAD` is a symbolic ref, not a branch anyone can check out, and
		// listing it puts a phantom "origin/HEAD" in the switcher.
		if strings.HasSuffix(refname, "/HEAD") {
			continue
		}

		branch := Branch{
			IsHead:   head == "*",
			IsRemote: strings.HasPrefix(refname, "refs/remotes/"),
			Target:   strPtr(target),
		}
		branch.Name = strings.TrimPrefix(strings.TrimPrefix(refname, "refs/heads/"), "refs/remotes/")

		// A remote branch has no upstream of its own and reports 0/0, which is what 2.x did: the
		// ahead/behind of a remote against itself is not a question the UI asks.
		if !branch.IsRemote && upstream != "" {
			branch.Upstream = strPtr(upstream)
			branch.Ahead, branch.Behind = parseTrack(track)
		}
		branches = append(branches, branch)
	}
	return branches, nil
}

// parseTrack reads `%(upstream:track,nobracket)`.
//
// "gone" — the upstream was deleted on the remote — reports 0/0 rather than an error. The branch
// still exists locally and the user still needs to see it; what they do about the missing upstream
// is their decision.
func parseTrack(track string) (int64, int64) {
	var ahead, behind int64

	for _, part := range strings.Split(track, ",") {
		part = strings.TrimSpace(part)
		switch {
		case strings.HasPrefix(part, "ahead "):
			ahead = parseCount(strings.TrimPrefix(part, "ahead "))
		case strings.HasPrefix(part, "behind "):
			behind = parseCount(strings.TrimPrefix(part, "behind "))
		}
	}
	return ahead, behind
}

func parseCount(field string) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(field), 10, 64)
	if err != nil {
		return 0
	}
	return value
}

// CreateBranch creates a branch, optionally from a start point (GIT-008).
func CreateBranch(ctx context.Context, repo, name string, startPoint *string) error {
	args := []string{"branch", name}
	if startPoint != nil && *startPoint != "" {
		args = append(args, *startPoint)
	}

	result, err := NewRunner(repo).RunWrite(ctx, args...)
	if err != nil {
		return err
	}
	if result.Failed() {
		return fmt.Errorf("git branch failed: %s", result.Detail())
	}
	return nil
}

// DeleteBranch removes a branch (GIT-009).
//
// `-D`, not `-d`: a bare ref delete with no merged check, which is what libgit2's ref deletion did.
// The UI asks the user for confirmation; asking git to second-guess that produces a refusal the
// user has already overruled.
func DeleteBranch(ctx context.Context, repo, name string, isRemote bool) error {
	args := []string{"branch", "-D", name}
	if isRemote {
		args = []string{"branch", "-dr", name}
	}

	result, err := NewRunner(repo).RunWrite(ctx, args...)
	if err != nil {
		return err
	}
	if result.Failed() {
		return fmt.Errorf("git branch failed: %s", result.Detail())
	}
	return nil
}

// checkoutConflictMarkers are the phrases git uses when local changes block a checkout.
//
// Matched in English, which is why every parsed command runs under LC_ALL=C. libgit2 reported this
// as a typed error; git reports it as text, and this is where the translation back happens.
var checkoutConflictMarkers = []string{
	"would be overwritten by checkout",
	"would be overwritten by merge",
	"Your local changes to the following files would be overwritten",
	"Please commit your changes or stash them before you switch branches",
}

// checkoutError maps a failed checkout to the renderer's contract (XLANG-002).
//
// A blocked checkout is a *recoverable state*, not a failure: the renderer offers to carry the
// changes across, stash them, or cancel. It recognises that offer by the sentinel at position 0,
// so nothing may be prepended — which is also why the message is built with errors.New rather than
// fmt.Errorf with a %w.
func checkoutError(result Result) error {
	detail := result.Detail()
	for _, marker := range checkoutConflictMarkers {
		if strings.Contains(detail, marker) {
			return errors.New(sentinel.CheckoutConflict + detail)
		}
	}
	return fmt.Errorf("git checkout failed: %s", detail)
}

// CheckoutLocalBranch switches to an existing local branch (GIT-004).
func CheckoutLocalBranch(ctx context.Context, repo, name string) error {
	result, err := NewRunner(repo).RunWrite(ctx, "checkout", name)
	if err != nil {
		return err
	}
	if result.Failed() {
		return checkoutError(result)
	}
	return nil
}

// CheckoutDetached moves HEAD to a revision without a branch (GIT-005).
func CheckoutDetached(ctx context.Context, repo, refname string) error {
	result, err := NewRunner(repo).RunWrite(ctx, "checkout", "--detach", refname)
	if err != nil {
		return err
	}
	if result.Failed() {
		return checkoutError(result)
	}
	return nil
}

// CheckoutRemoteTracking checks out a remote branch, creating a local one that tracks it (GIT-006).
//
// Returns the local branch name, which the renderer puts in the switcher.
//
// When a local branch of that name already exists it is simply checked out and **its upstream is
// left alone** (`AMBIGUOUS-GIT-a`). That is 2.x's behaviour and the port does not resolve it: a
// local branch pointing somewhere else on purpose must not be silently re-pointed.
func CheckoutRemoteTracking(ctx context.Context, repo, remoteBranch string) (string, error) {
	slash := strings.Index(remoteBranch, "/")
	if slash <= 0 || slash == len(remoteBranch)-1 {
		// The exact text 2.x produced; the renderer shows it verbatim.
		return "", fmt.Errorf("expected a name like 'origin/feature-x'")
	}
	short := remoteBranch[slash+1:]

	runner := NewRunner(repo)
	exists, err := runner.Run(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+short)
	if err != nil {
		return "", err
	}

	args := []string{"checkout", "-b", short, "--track", remoteBranch}
	if !exists.Failed() {
		args = []string{"checkout", short}
	}

	result, err := runner.RunWrite(ctx, args...)
	if err != nil {
		return "", err
	}
	if result.Failed() {
		return "", checkoutError(result)
	}
	return short, nil
}
