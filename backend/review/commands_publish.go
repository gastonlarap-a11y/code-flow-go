package review

import (
	"context"
	"errors"
	"time"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/platform"
	"github.com/gastonlarap-a11y/code-flow/backend/providers"
)

// The two publishing commands.
//
// They differ in one thing that changes everything downstream: the project-linked one has a saved
// run to reconcile against, so a finding keeps its thread across iterations; the link-only one has
// none, so every finding opens a fresh thread every time.

// Hosts resolves a pull-request host, either from a project or from a pasted link.
//
// Declared at the consumer, as everything else in this repository is, and satisfied by
// `providers.Deps` — which is also what the provider commands dispatch through, so a review can
// never publish to a host the sidebar would not have listed from.
type Hosts interface {
	HostForProject(ctx context.Context, projectID string) (providers.PullRequestHost, error)
	HostForLink(ctx context.Context, url string) (providers.PullRequestHost, providers.PRLink, error)
}

// PipelineDeps is what the review pipeline needs beyond the store.
//
// Publishing needs the first three; running a review needs the rest. They are one struct because
// they are one feature — a review that cannot be published is half a feature — and because the
// composition root wires them together anyway.
type PipelineDeps struct {
	Store *Store
	Hosts Hosts

	// Projects reads the project row and the workspace's own review settings. Nil in a test that
	// only publishes.
	Projects Projects
	// AI runs the model. Nil means no review can be started, which is the state an install whose
	// storage failed is already in.
	AI Reviewer
	// Fetcher brings refs in first. Nil skips every fetch, which is what an offline review does
	// anyway.
	Fetcher Fetcher
	// Activity files the finished run in the project's history.
	Activity Activity
	// Paths is where the MCP config and a link review's working directory live.
	Paths platform.Paths

	// Today is the date a reply carries, in the posting machine's own local time. A seam rather
	// than a call to time.Now, so a test can pin what the comment says.
	Today func() string
	// Now is the clock the footer's timestamp reads. Same reason.
	Now func() time.Time
}

func (d PipelineDeps) today() string {
	if d.Today != nil {
		return d.Today()
	}
	// Local, not UTC: the date belongs to the person posting, and a comment dated "tomorrow" reads
	// as a clock nobody can find.
	return time.Now().Format(time.DateOnly)
}

// RegisterPublishing adds the two commands that write a review's findings to its pull request.
func RegisterPublishing(r *bridge.Registry, deps PipelineDeps) {
	r.Add("post_pr_review_comment", func(ctx context.Context, p bridge.Params) (any, error) {
		projectID, err := bridge.Arg[string](p, "projectId")
		if err != nil {
			return nil, err
		}
		prID, err := bridge.Arg[int64](p, "prId")
		if err != nil {
			return nil, err
		}
		runID, err := bridge.Arg[string](p, "runId")
		if err != nil {
			return nil, err
		}
		batch, err := postBatch(p)
		if err != nil {
			return nil, err
		}

		host, err := deps.Hosts.HostForProject(ctx, projectID)
		if err != nil {
			return nil, err
		}
		return nil, deps.publishSavedRun(ctx, host, prID, runID, batch)
	})

	// The repo-less twin. No saved run means no reconciliation: each finding opens a brand-new
	// thread, even one that was posted from this same link an hour ago.
	r.Add("post_pr_link_review_comment", func(ctx context.Context, p bridge.Params) (any, error) {
		url, err := bridge.Arg[string](p, "url")
		if err != nil {
			return nil, err
		}
		batch, err := postBatch(p)
		if err != nil {
			return nil, err
		}

		host, link, err := deps.Hosts.HostForLink(ctx, url)
		if err != nil {
			return nil, err
		}

		// Iteration 1 and no stored findings: a link review saves no run, so there is nothing to
		// reconcile against and no analysed head to compare — the stale check has nothing to check.
		batch.Iter = 1
		batch.Today = deps.today()

		_, err = PublishFindings(ctx, host, link.Number, nil, batch, "")
		return nil, err
	})
}

// publishSavedRun posts a batch against the run that produced it and writes back what changed.
func (d PipelineDeps) publishSavedRun(
	ctx context.Context,
	host providers.PullRequestHost,
	prID int64,
	runID string,
	batch PostBatch,
) error {
	// A run the user deleted in another window is not a failure to post: the findings they picked
	// are in the batch, and they go out against an empty memory — each one opening its own thread.
	findings := []MemoryFinding{}
	analysedHead := ""
	batch.Iter = 1
	batch.Today = d.today()

	run, err := d.Store.GetRun(ctx, runID)
	switch {
	case err == nil:
		findings = DecodeFindings(run.Findings)
		analysedHead = AnalysedHead(run.Meta)
		batch.Iter = run.Iter
	case !errors.Is(err, ErrNotFound):
		return err
	}

	published, publishErr := PublishFindings(ctx, host, prID, findings, batch, analysedHead)

	// The write-back happens whatever the batch reported: the threads that did open are recorded,
	// or a retry would open them a second time. Partial success is not rolled back — the pull
	// request already has those comments.
	if len(published) > 0 {
		if err := d.Store.WriteFindings(ctx, runID, EncodeFindings(published)); err != nil && publishErr == nil {
			return err
		}
	}
	return publishErr
}

// postBatch reads the three parameters both publishing commands share.
func postBatch(p bridge.Params) (PostBatch, error) {
	items, err := bridge.Arg[[]PostFindingItem](p, "items")
	if err != nil {
		return PostBatch{}, err
	}
	postSummary, err := bridge.Arg[bool](p, "postSummary")
	if err != nil {
		return PostBatch{}, err
	}
	summary, err := bridge.OptionalArg[string](p, "summary")
	if err != nil {
		return PostBatch{}, err
	}
	return PostBatch{Items: items, PostSummary: postSummary, Summary: summary}, nil
}
