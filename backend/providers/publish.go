package providers

import (
	"context"
	"fmt"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/sentinel"
)

// Publishing a review's findings, the half of each host that writes.
//
// The two hosts diverge here in ways that are theirs rather than this port's, and each one is
// pinned by its own test: GitHub anchors a comment to a commit and Azure to an iteration, GitHub
// resolves a thread through GraphQL and Azure by setting a status, GitHub's conversation reads
// oldest-first and Azure's newest-first.

// ---- GitHub -------------------------------------------------------------------------------------

// EnsureUnchanged refuses the batch when the pull request has moved on (`XLANG-014`).
//
// The anchors in a review were computed against the diff as it stood when the review ran. If the
// branch has been pushed to since, those line numbers now point at whatever occupies them today —
// and a comment on the wrong line reads as a reviewer who did not understand the code, which a
// person then has to delete by hand, one at a time. Refusing is the kinder failure.
func (h gitHubHost) EnsureUnchanged(ctx context.Context, number int64, analysedHead string) error {
	if strings.TrimSpace(analysedHead) == "" {
		// A run saved before the SHA was recorded. There is nothing to compare, and refusing every
		// old run would take away the feature for the reviews most likely to need it.
		return nil
	}

	current, err := h.client.HeadSHA(ctx, h.owner, h.repo, number)
	if err != nil {
		return err
	}
	if current == analysedHead {
		return nil
	}

	// The sentinel is built here rather than at the command boundary, unlike every other one: it is
	// the host that knows the two SHAs, and the message names both because "review it again" is
	// only actionable when the reader can see what moved.
	return fmt.Errorf("%sthe pull request moved from %s to %s since this review ran — review it again before publishing", //nolint:err113 // the prefix is the contract
		sentinel.StaleReview, abbreviate(analysedHead), abbreviate(current))
}

func abbreviate(sha string) string {
	if len(sha) <= 7 {
		return sha
	}
	return sha[:7]
}

func (h gitHubHost) OpenThread(ctx context.Context, number int64, content string, location *CommentLocation) (int64, error) {
	if location == nil {
		return h.client.PostComment(ctx, h.owner, h.repo, number, content)
	}

	// Anchoring needs the commit the comment hangs off, read fresh: a re-review anchors to the tip,
	// not to what the review was computed against. `EnsureUnchanged` has already refused the batch
	// if those two differ.
	commit, err := h.client.HeadSHA(ctx, h.owner, h.repo, number)
	if err != nil {
		return 0, err
	}
	return h.client.PostAnchoredComment(ctx, h.owner, h.repo, number,
		content, location.File, location.StartLine, location.EndLine, commit)
}

func (h gitHubHost) Reply(ctx context.Context, number, threadID int64, content string, resolved bool) error {
	if err := h.client.ReplyToComment(ctx, h.owner, h.repo, number, threadID, content); err != nil {
		return err
	}
	if !resolved {
		return nil
	}
	// Best effort, deliberately: the reply — the thing a person reads — already landed, and a
	// thread that stays open is a worse review rather than a failed publish.
	_ = h.client.ResolveReviewThreadForComment(ctx, h.owner, h.repo, number, threadID)
	return nil
}

// DiscussionNewestFirst is false: GitHub's conversation runs oldest-first, so the summary is posted
// **before** the findings to sit above them.
func (h gitHubHost) DiscussionNewestFirst() bool { return false }

// ---- Azure DevOps -------------------------------------------------------------------------------

// EnsureUnchanged does nothing on Azure, and that is `BUG-REVIEW-a`'s remaining half.
//
// Azure anchors a comment to an **iteration**, not to a commit, so there is no SHA to compare
// against the one the review recorded. The gap is left exactly as open as it was rather than
// papered over with a check that would not mean the same thing.
func (h azureHost) EnsureUnchanged(context.Context, int64, string) error { return nil }

func (h azureHost) OpenThread(ctx context.Context, number int64, content string, location *CommentLocation) (int64, error) {
	if location == nil {
		return h.client.PostComment(ctx, h.project, h.repoID, number, content)
	}
	return h.client.PostAnchoredComment(ctx, h.project, h.repoID, number,
		content, location.File, location.StartLine, location.EndLine)
}

func (h azureHost) Reply(ctx context.Context, number, threadID int64, content string, resolved bool) error {
	if err := h.client.ReplyToThread(ctx, h.project, h.repoID, number, threadID, content); err != nil {
		return err
	}
	if !resolved {
		return nil
	}
	// Same rule as GitHub's resolve: the reply is what matters, the status is bookkeeping.
	_ = h.client.SetThreadStatus(ctx, h.project, h.repoID, number, threadID, AzureThreadFixed)
	return nil
}

// DiscussionNewestFirst is true: Azure's overview shows the newest thread on top, so the summary is
// posted **after** the findings to end up above them.
func (h azureHost) DiscussionNewestFirst() bool { return true }
