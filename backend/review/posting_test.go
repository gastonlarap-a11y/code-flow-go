package review_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/providers"
	"github.com/gastonlarap-a11y/code-flow/backend/review"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/sentinel"
)

// The port of ReviewPostingTests and ReviewPostingFromLinkTests. One property carries the whole
// feature: **one finding, one thread**, for the pull request's whole life. Everything else here is
// in service of it.

// fakeHost records what was published and answers what a test told it to.
type fakeHost struct {
	provider    providers.Provider
	newestFirst bool

	// opened and replies are what went out, in order.
	opened  []openedThread
	replies []reply

	nextThreadID int64
	staleErr     error
	openErr      error
	replyErr     error

	// What the host answers when a review with no clone asks it for the change.
	diff    string
	diffErr error
}

type openedThread struct {
	content  string
	location *providers.CommentLocation
}

type reply struct {
	threadID int64
	content  string
	resolved bool
}

func (f *fakeHost) Provider() providers.Provider { return f.provider }

func (f *fakeHost) List(context.Context) ([]providers.PullRequestSummary, error) { return nil, nil }

func (f *fakeHost) Get(context.Context, int64) (providers.PullRequestSummary, error) {
	return providers.PullRequestSummary{}, nil
}

func (f *fakeHost) Create(context.Context, providers.NewPullRequest) (providers.PullRequestSummary, error) {
	return providers.PullRequestSummary{}, nil
}

func (f *fakeHost) Threads(context.Context, int64) ([]providers.PrCommentThread, error) {
	return nil, nil
}

func (f *fakeHost) Decision(context.Context, int64) (string, error) { return "none", nil }

func (f *fakeHost) Diff(context.Context, int64) (string, error) { return f.diff, f.diffErr }

func (f *fakeHost) Act(context.Context, int64, string, string) error { return nil }

func (f *fakeHost) EnsureUnchanged(context.Context, int64, string) error { return f.staleErr }

func (f *fakeHost) OpenThread(_ context.Context, _ int64, content string, location *providers.CommentLocation) (int64, error) {
	if f.openErr != nil {
		return 0, f.openErr
	}
	f.opened = append(f.opened, openedThread{content: content, location: location})
	f.nextThreadID++
	return f.nextThreadID, nil
}

func (f *fakeHost) Reply(_ context.Context, _, threadID int64, content string, resolved bool) error {
	if f.replyErr != nil {
		return f.replyErr
	}
	f.replies = append(f.replies, reply{threadID: threadID, content: content, resolved: resolved})
	return nil
}

func (f *fakeHost) DiscussionNewestFirst() bool { return f.newestFirst }

func gitHubHost() *fakeHost { return &fakeHost{provider: providers.ProviderGitHub} }
func azureHost() *fakeHost  { return &fakeHost{provider: providers.ProviderAzure, newestFirst: true} }

func item(file, category, content string, location *providers.CommentLocation) review.PostFindingItem {
	return review.PostFindingItem{File: &file, Category: category, Content: content, Location: location}
}

// anchor is a comment's file range. Named for what it does rather than `at`, which this package's
// store tests already use for a timestamp.
func anchor(file string, start, end int64) *providers.CommentLocation {
	return &providers.CommentLocation{File: file, StartLine: start, EndLine: end}
}

const today = "2026-09-18"

// ---- opening, replying, and the one thread per finding ----------------------------------------

func TestAFindingWithNoThreadOpensOneAndRecordsIt(t *testing.T) {
	host := gitHubHost()
	findings := []review.MemoryFinding{stored("F-001", "src/app.ts", "race", "abierto", 1)}

	published, err := review.PublishFindings(t.Context(), host, 7, findings, review.PostBatch{
		Items: []review.PostFindingItem{item("src/app.ts", "race", "el contador se lee sin lock", anchor("src/app.ts", 12, 14))},
		Iter:  2, Today: today,
	}, "")

	require.NoError(t, err)
	require.Len(t, host.opened, 1)
	assert.Equal(t, "el contador se lee sin lock", host.opened[0].content)
	require.NotNil(t, host.opened[0].location)
	assert.Equal(t, int64(12), host.opened[0].location.StartLine)

	require.NotNil(t, published[0].ThreadID)
	assert.Equal(t, int64(1), *published[0].ThreadID, "so the next iteration replies into it")
	assert.Equal(t, "posteado", published[0].Estado)
}

// A finding already posted gets a reply in the thread it owns, not a second copy of itself.
func TestAFindingThatAlreadyHasAThreadIsRepliedTo(t *testing.T) {
	host := gitHubHost()
	finding := stored("F-001", "src/app.ts", "race", "posteado", 1)
	finding.ThreadID = ptr(int64(555))

	_, err := review.PublishFindings(t.Context(), host, 7, []review.MemoryFinding{finding}, review.PostBatch{
		Items: []review.PostFindingItem{item("src/app.ts", "race", "el mismo hallazgo", nil)},
		Iter:  4, Today: today,
	}, "")

	require.NoError(t, err)
	assert.Empty(t, host.opened, "no second conversation for one finding")
	require.Len(t, host.replies, 1)
	assert.Equal(t, int64(555), host.replies[0].threadID)
	assert.False(t, host.replies[0].resolved)
}

// The two hosts' reply wordings genuinely differ, and both are what their threads have said since
// 2.x.
func TestTheReplyWordingIsTheHostsOwn(t *testing.T) {
	tests := []struct {
		name     string
		host     *fakeHost
		estado   string
		expected string
	}{
		{
			name: "github, still there", host: gitHubHost(), estado: "posteado",
			expected: "➡️ Sigue presente en la iteración 4 — 2026-09-18.",
		},
		{
			name: "github, resolved", host: gitHubHost(), estado: "resuelto",
			expected: "✔️ Resuelto en la iteración 4 — 2026-09-18.",
		},
		{
			name: "azure, still there", host: azureHost(), estado: "posteado",
			expected: "➡️ _Sigue presente en la iteración 4 — 2026-09-18._",
		},
		{
			name: "azure, resolved", host: azureHost(), estado: "resuelto",
			expected: "✔️ _Resuelto en la iteración 4 — 2026-09-18. Marcado como fixed._",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			finding := stored("F-001", "src/app.ts", "race", test.estado, 1)
			finding.ThreadID = ptr(int64(9))

			_, err := review.PublishFindings(t.Context(), test.host, 7, []review.MemoryFinding{finding},
				review.PostBatch{
					Items: []review.PostFindingItem{item("src/app.ts", "race", "x", nil)},
					Iter:  4, Today: today,
				}, "")

			require.NoError(t, err)
			require.Len(t, test.host.replies, 1)
			assert.Equal(t, test.expected, test.host.replies[0].content)
			assert.Equal(t, test.estado == "resuelto", test.host.replies[0].resolved,
				"a resolved finding also closes its thread")
		})
	}
}

// A finding that was resolved before anyone posted it stays resolved: the traceability section will
// say so, and flipping it to "posted" would claim it is still open.
func TestPostingAResolvedFindingDoesNotReopenIt(t *testing.T) {
	host := gitHubHost()
	findings := []review.MemoryFinding{stored("F-001", "src/app.ts", "race", "resuelto", 1)}

	published, err := review.PublishFindings(t.Context(), host, 7, findings, review.PostBatch{
		Items: []review.PostFindingItem{item("src/app.ts", "race", "x", nil)},
		Iter:  2, Today: today,
	}, "")

	require.NoError(t, err)
	assert.Equal(t, "resuelto", published[0].Estado)
	require.NotNil(t, published[0].ThreadID, "the thread is still recorded")
}

// Two selected items that share an identity: the second sees the thread the first just opened, and
// replies into it.
func TestTwoItemsSharingAnIdentityShareTheThread(t *testing.T) {
	host := gitHubHost()
	findings := []review.MemoryFinding{stored("F-001", "src/app.ts", "race", "abierto", 1)}

	_, err := review.PublishFindings(t.Context(), host, 7, findings, review.PostBatch{
		Items: []review.PostFindingItem{
			item("src/app.ts", "race", "primero", nil),
			item("src/app.ts", "race", "segundo", nil),
		},
		Iter: 2, Today: today,
	}, "")

	require.NoError(t, err)
	assert.Len(t, host.opened, 1, "one conversation")
	assert.Len(t, host.replies, 1, "the second item replies into it")
}

// An item that matches no stored finding is still posted — the user picked it — but no thread id is
// recorded for it, so a re-post opens another. Preserved: the alternative is inventing a finding.
func TestAnItemThatMatchesNothingIsStillPosted(t *testing.T) {
	host := gitHubHost()
	findings := []review.MemoryFinding{stored("F-001", "src/other.ts", "naming", "abierto", 1)}

	published, err := review.PublishFindings(t.Context(), host, 7, findings, review.PostBatch{
		Items: []review.PostFindingItem{item("src/app.ts", "race", "huérfano", nil)},
		Iter:  2, Today: today,
	}, "")

	require.NoError(t, err)
	require.Len(t, host.opened, 1)
	assert.Nil(t, published[0].ThreadID, "the stored finding is untouched")
	assert.Equal(t, "abierto", published[0].Estado)
}

// ---- the summary, at whichever end reads first (DIVERGENCE-PROV-d) ----------------------------

func TestOnGitHubTheSummaryIsPostedFirst(t *testing.T) {
	host := gitHubHost()
	summary := "En conjunto el cambio se ve bien."

	_, err := review.PublishFindings(t.Context(), host, 7,
		[]review.MemoryFinding{stored("F-001", "src/app.ts", "race", "abierto", 1)},
		review.PostBatch{
			Items:       []review.PostFindingItem{item("src/app.ts", "race", "el hallazgo", nil)},
			PostSummary: true, Summary: &summary, Iter: 2, Today: today,
		}, "")

	require.NoError(t, err)
	require.Len(t, host.opened, 2)
	assert.Equal(t, summary, host.opened[0].content, "GitHub's conversation runs oldest-first")
	assert.Equal(t, "el hallazgo", host.opened[1].content)
}

func TestOnAzureTheSummaryIsPostedLastSoThatItReadsFirst(t *testing.T) {
	host := azureHost()
	summary := "En conjunto el cambio se ve bien."

	_, err := review.PublishFindings(t.Context(), host, 7,
		[]review.MemoryFinding{stored("F-001", "src/app.ts", "race", "abierto", 1)},
		review.PostBatch{
			Items:       []review.PostFindingItem{item("src/app.ts", "race", "el hallazgo", nil)},
			PostSummary: true, Summary: &summary, Iter: 2, Today: today,
		}, "")

	require.NoError(t, err)
	require.Len(t, host.opened, 2)
	assert.Equal(t, "el hallazgo", host.opened[0].content, "Azure's overview shows the newest on top")
	assert.Equal(t, summary, host.opened[1].content)
}

func TestABlankSummaryIsNotPosted(t *testing.T) {
	host := gitHubHost()
	blank := "   "

	_, err := review.PublishFindings(t.Context(), host, 7, nil, review.PostBatch{
		PostSummary: true, Summary: &blank, Iter: 1, Today: today,
	}, "")

	require.NoError(t, err)
	assert.Empty(t, host.opened)
}

// ---- refusing a batch that would land on moved lines (XLANG-014) ------------------------------

func TestAStaleBatchIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	host := gitHubHost()
	host.staleErr = errors.New(sentinel.StaleReview + "the pull request moved from abc1234 to def5678")
	summary := "resumen"

	_, err := review.PublishFindings(t.Context(), host, 7,
		[]review.MemoryFinding{stored("F-001", "src/app.ts", "race", "abierto", 1)},
		review.PostBatch{
			Items:       []review.PostFindingItem{item("src/app.ts", "race", "x", anchor("src/app.ts", 1, 2))},
			PostSummary: true, Summary: &summary, Iter: 2, Today: today,
		}, "abc1234")

	require.Error(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), sentinel.StaleReview))
	assert.Empty(t, host.opened, "not even the summary")
	assert.Empty(t, host.replies)
}

// ---- failures ---------------------------------------------------------------------------------

// Every item is attempted whatever the ones before it did: a publish that stopped halfway leaves a
// pull request in a state nobody chose.
func TestEveryItemIsAttemptedAndTheFailuresComeBackTogether(t *testing.T) {
	host := &fakeHost{provider: providers.ProviderGitHub, openErr: errors.New("GitHub returned 500: boom")}

	_, err := review.PublishFindings(t.Context(), host, 7, nil, review.PostBatch{
		Items: []review.PostFindingItem{
			item("a.ts", "one", "x", nil),
			item("b.ts", "two", "y", nil),
		},
		Iter: 1, Today: today,
	}, "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "2 comment(s) failed to post")
	assert.Contains(t, err.Error(), "#1: ")
	assert.Contains(t, err.Error(), "#2: ")
}

// A failed summary is reported under its own label and does not stop the findings.
func TestAFailedSummaryDoesNotStopTheFindings(t *testing.T) {
	host := gitHubHost()
	host.openErr = errors.New("GitHub returned 403: no")
	summary := "resumen"

	_, err := review.PublishFindings(t.Context(), host, 7, nil, review.PostBatch{
		Items:       []review.PostFindingItem{},
		PostSummary: true, Summary: &summary, Iter: 1, Today: today,
	}, "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "summary: ")
}

// The threads that did open are recorded even when the batch as a whole failed, or a retry would
// open them a second time.
func TestWhatSucceededIsStillRecordedWhenSomethingElseFails(t *testing.T) {
	host := gitHubHost()
	host.replyErr = errors.New("GitHub returned 500: boom")

	withThread := stored("F-002", "src/b.ts", "naming", "posteado", 1)
	withThread.ThreadID = ptr(int64(99))
	findings := []review.MemoryFinding{stored("F-001", "src/a.ts", "race", "abierto", 1), withThread}

	published, err := review.PublishFindings(t.Context(), host, 7, findings, review.PostBatch{
		Items: []review.PostFindingItem{
			item("src/a.ts", "race", "abre hilo", nil),
			item("src/b.ts", "naming", "responde", nil),
		},
		Iter: 2, Today: today,
	}, "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "1 comment(s) failed to post")
	require.NotNil(t, published[0].ThreadID, "the one that worked is recorded")
	assert.Equal(t, "posteado", published[0].Estado)
}

// ---- the two commands, over the wire -----------------------------------------------------------

type fakeHosts struct {
	host       providers.PullRequestHost
	link       providers.PRLink
	projectErr error
	linkErr    error
	askedFor   string
}

func (f *fakeHosts) HostForProject(_ context.Context, projectID string) (providers.PullRequestHost, error) {
	f.askedFor = projectID
	if f.projectErr != nil {
		return nil, f.projectErr
	}
	return f.host, nil
}

func (f *fakeHosts) HostForLink(_ context.Context, url string) (providers.PullRequestHost, providers.PRLink, error) {
	f.askedFor = url
	if f.linkErr != nil {
		return nil, providers.PRLink{}, f.linkErr
	}
	return f.host, f.link, nil
}

func newPublishService(t *testing.T, deps review.PipelineDeps) *bridge.Service {
	t.Helper()
	r := bridge.NewRegistry()
	review.RegisterPublishing(r, deps)
	r.Seal()
	return bridge.NewService(r, nil)
}

// storedFindingsJSON is one open finding, as a run's column holds it.
func storedFindingsJSON(t *testing.T, findings ...review.MemoryFinding) string {
	t.Helper()
	encoded, err := json.Marshal(findings)
	require.NoError(t, err)
	return string(encoded)
}

func findingsOf(t *testing.T, store *review.Store, runID string) []review.MemoryFinding {
	t.Helper()
	run, err := store.GetRun(t.Context(), runID)
	require.NoError(t, err)
	return review.DecodeFindings(run.Findings)
}

func TestPostingASavedRunReadsItsFindingsAndWritesThemBack(t *testing.T) {
	store, db := newStore(t)
	insertRun(t, db, "r1", 7, 2, at(1), `{"head_sha":"abc1234","iter":2}`,
		storedFindingsJSON(t, stored("F-001", "src/app.ts", "race", "abierto", 1)))

	host := gitHubHost()
	svc := newPublishService(t, review.PipelineDeps{
		Store: store, Hosts: &fakeHosts{host: host}, Today: func() string { return today },
	})

	_, err := svc.Invoke(t.Context(), "post_pr_review_comment", json.RawMessage(`{
		"projectId": "p1", "prId": 7, "runId": "r1",
		"items": [{"file":"src/app.ts","category":"race","content":"el hallazgo","location":{"file":"src/app.ts","startLine":12,"endLine":14}}],
		"postSummary": false, "summary": null
	}`))

	require.NoError(t, err)
	require.Len(t, host.opened, 1)
	require.NotNil(t, host.opened[0].location)
	assert.Equal(t, int64(14), host.opened[0].location.EndLine, "the renderer's camelCase location arrives intact")

	// The thread is written back into the run, or the next publish would open a second one.
	written := findingsOf(t, store, "r1")
	require.Len(t, written, 1)
	require.NotNil(t, written[0].ThreadID)
	assert.Equal(t, "posteado", written[0].Estado)
}

func TestPostingUsesTheRunsOwnIterationInItsReplies(t *testing.T) {
	store, db := newStore(t)
	finding := stored("F-001", "src/app.ts", "race", "posteado", 1)
	finding.ThreadID = ptr(int64(42))
	insertRun(t, db, "r1", 7, 5, at(1), `{"head_sha":"","iter":5}`, storedFindingsJSON(t, finding))

	host := gitHubHost()
	svc := newPublishService(t, review.PipelineDeps{
		Store: store, Hosts: &fakeHosts{host: host}, Today: func() string { return today },
	})

	_, err := svc.Invoke(t.Context(), "post_pr_review_comment", json.RawMessage(`{
		"projectId": "p1", "prId": 7, "runId": "r1",
		"items": [{"file":"src/app.ts","category":"race","content":"x","location":null}],
		"postSummary": false, "summary": null
	}`))

	require.NoError(t, err)
	require.Len(t, host.replies, 1)
	assert.Equal(t, int64(42), host.replies[0].threadID)
	assert.Contains(t, host.replies[0].content, "iteración 5")
}

// A run deleted in another window is not a failed publish: the comments the user picked still go
// out, each opening its own thread.
func TestPostingAgainstARunThatIsGoneStillPosts(t *testing.T) {
	store, _ := newStore(t)
	host := gitHubHost()
	svc := newPublishService(t, review.PipelineDeps{
		Store: store, Hosts: &fakeHosts{host: host}, Today: func() string { return today },
	})

	_, err := svc.Invoke(t.Context(), "post_pr_review_comment", json.RawMessage(`{
		"projectId": "p1", "prId": 7, "runId": "does-not-exist",
		"items": [{"file":"a.ts","category":"race","content":"x","location":null}],
		"postSummary": false, "summary": null
	}`))

	require.NoError(t, err)
	assert.Len(t, host.opened, 1)
}

// The link path has no saved run, so every finding opens a fresh thread — even one posted from the
// same link an hour ago.
func TestPostingFromALinkAlwaysOpensFreshThreads(t *testing.T) {
	store, _ := newStore(t)
	host := azureHost()
	summary := "resumen"
	svc := newPublishService(t, review.PipelineDeps{
		Store: store,
		Hosts: &fakeHosts{host: host, link: providers.PRLink{Provider: providers.ProviderAzure, Number: 87266}},
		Today: func() string { return today },
	})

	_, err := svc.Invoke(t.Context(), "post_pr_link_review_comment", json.RawMessage(fmt.Sprintf(`{
		"url": "https://dev.azure.com/contoso/Dev/_git/Dev.prueba/pullrequest/87266",
		"items": [
			{"file":"a.ts","category":"race","content":"uno","location":null},
			{"file":"a.ts","category":"race","content":"dos","location":null}
		],
		"postSummary": true, "summary": %q
	}`, summary)))

	require.NoError(t, err)
	assert.Len(t, host.opened, 3, "two findings and the summary, all new threads")
	assert.Empty(t, host.replies, "there is nothing to reply to")
	assert.Equal(t, summary, host.opened[2].content, "Azure's summary goes last so it reads first")
}

func TestThePublishingCommandsNameAMissingParameter(t *testing.T) {
	store, _ := newStore(t)
	svc := newPublishService(t, review.PipelineDeps{
		Store: store, Hosts: &fakeHosts{host: gitHubHost()},
	})

	tests := map[string]string{
		"post_pr_review_comment":      `{"projectId":"p1","prId":7}`,
		"post_pr_link_review_comment": `{"url":"https://x/y/pull/1"}`,
	}

	for method, params := range tests {
		t.Run(method, func(t *testing.T) {
			_, err := svc.Invoke(t.Context(), method, json.RawMessage(params))

			require.Error(t, err)
			assert.Contains(t, err.Error(), "missing required parameter")
		})
	}
}
