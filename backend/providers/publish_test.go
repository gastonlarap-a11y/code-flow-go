package providers_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/providers"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/sentinel"
)

// The port of GitHubPostingTests and AzurePostingTests: the half of each host that writes a review
// to a pull request. The two diverge in ways that are theirs, and each divergence is pinned here.

// ---- refusing a stale batch (XLANG-014, closing GitHub's half of BUG-REVIEW-a) -----------------

func TestGitHubRefusesABatchWhoseAnchorsHaveMoved(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls/7", http.StatusOK,
		`{"number":7,"head":{"ref":"f","sha":"def5678000000"},"base":{"ref":"main"},"user":{"login":"g"}}`)
	publisher := providers.NewGitHubHost(host.client(), "acme", "widget")

	err := publisher.EnsureUnchanged(t.Context(), 7, "abc1234000000")

	require.Error(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), sentinel.StaleReview),
		"the renderer matches this at position 0 and says 'review it again'")
	assert.Contains(t, err.Error(), "abc1234", "both abbreviated SHAs, so the reader sees what moved")
	assert.Contains(t, err.Error(), "def5678")
}

func TestGitHubAllowsABatchWhoseHeadIsUnchanged(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls/7", http.StatusOK,
		`{"number":7,"head":{"ref":"f","sha":"abc1234"},"base":{"ref":"main"},"user":{"login":"g"}}`)

	err := providers.NewGitHubHost(host.client(), "acme", "widget").EnsureUnchanged(t.Context(), 7, "abc1234")

	assert.NoError(t, err)
}

// A run saved before the SHA was recorded cannot be compared, and refusing every old run would take
// the feature away from the reviews most likely to need it.
func TestARunWithNoRecordedHeadIsNotRefused(t *testing.T) {
	host := newFakeGitHub(t)

	err := providers.NewGitHubHost(host.client(), "acme", "widget").EnsureUnchanged(t.Context(), 7, "")

	require.NoError(t, err)
	assert.Empty(t, host.requests, "nothing to compare, nothing to ask")
}

// Azure anchors by iteration and has no SHA to compare — `BUG-REVIEW-a`'s remaining half, left as
// open as it was rather than papered over.
func TestAzureHasNoStaleCheckToRun(t *testing.T) {
	host := newFakeAzure(t)

	err := providers.NewAzureHost(host.client("contoso"), "Dev", "r").EnsureUnchanged(t.Context(), 7, "abc1234")

	require.NoError(t, err)
	assert.Empty(t, host.seen())
}

// ---- opening a thread ---------------------------------------------------------------------------

func TestGitHubAnchorsAThreadToTheCurrentHead(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls/7", http.StatusOK,
		`{"number":7,"head":{"ref":"f","sha":"tip123"},"base":{"ref":"main"},"user":{"login":"g"}}`)
	host.answer(http.MethodPost, "/api/v3/repos/acme/widget/pulls/7/comments", http.StatusCreated, `{"id":991}`)

	id, err := providers.NewGitHubHost(host.client(), "acme", "widget").OpenThread(t.Context(), 7,
		"el hallazgo", &providers.CommentLocation{File: "src/app.ts", StartLine: 12, EndLine: 14})

	require.NoError(t, err)
	assert.Equal(t, int64(991), id)
	assert.JSONEq(t, `{
		"body": "el hallazgo", "path": "src/app.ts", "line": 14, "side": "RIGHT",
		"commit_id": "tip123", "start_line": 12, "start_side": "RIGHT"
	}`, host.last().Body)
}

func TestAThreadWithNoLocationIsAConversationComment(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodPost, "/api/v3/repos/acme/widget/issues/7/comments", http.StatusCreated, `{"id":5}`)

	id, err := providers.NewGitHubHost(host.client(), "acme", "widget").OpenThread(t.Context(), 7, "el resumen", nil)

	require.NoError(t, err)
	assert.Equal(t, int64(5), id)
	assert.Equal(t, "/api/v3/repos/acme/widget/issues/7/comments", host.last().Path)
	assert.Len(t, host.requests, 1, "an unanchored comment needs no head lookup")
}

func TestAzureOpensAnchoredAndUnanchoredThreads(t *testing.T) {
	t.Run("anchored", func(t *testing.T) {
		host := newFakeAzure(t)
		host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/r/pullRequests/7/iterations",
			http.StatusOK, `{"value":[{"id":3}]}`)
		host.answer(http.MethodPost, "/contoso/Dev/_apis/git/repositories/r/pullRequests/7/threads",
			http.StatusCreated, `{"id":600}`)

		id, err := providers.NewAzureHost(host.client("contoso"), "Dev", "r").OpenThread(t.Context(), 7,
			"el hallazgo", &providers.CommentLocation{File: "src/app.ts", StartLine: 12, EndLine: 14})

		require.NoError(t, err)
		assert.Equal(t, int64(600), id)
		assert.Contains(t, host.last().Body, `"filePath":"/src/app.ts"`, "Azure wants the leading slash")
		assert.Contains(t, host.last().Body, `"secondComparingIteration":3`)
	})

	t.Run("unanchored", func(t *testing.T) {
		host := newFakeAzure(t)
		host.answer(http.MethodPost, "/contoso/Dev/_apis/git/repositories/r/pullRequests/7/threads",
			http.StatusCreated, `{"id":601}`)

		id, err := providers.NewAzureHost(host.client("contoso"), "Dev", "r").OpenThread(t.Context(), 7, "el resumen", nil)

		require.NoError(t, err)
		assert.Equal(t, int64(601), id)
		assert.NotContains(t, host.last().Body, "threadContext")
		assert.Len(t, host.seen(), 1, "no iteration lookup for a thread that is not anchored")
	})
}

// ---- replying, and closing the thread when the finding is gone --------------------------------

func TestGitHubResolvesAThreadThroughGraphQLAfterTheReply(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodPost, "/api/v3/repos/acme/widget/pulls/7/comments/991/replies", http.StatusCreated, `{"id":992}`)
	host.answer(http.MethodPost, "/api/graphql", http.StatusOK, `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[
		{"id":"NODE_A","comments":{"nodes":[{"databaseId":991}]}}
	]}}}}}`)

	err := providers.NewGitHubHost(host.client(), "acme", "widget").
		Reply(t.Context(), 7, 991, "✔️ Resuelto en la iteración 3 — 2026-09-18.", true)

	require.NoError(t, err)
	require.Len(t, host.requests, 3, "the reply, the thread lookup and the mutation")
	assert.Contains(t, host.requests[2].Body, "resolveReviewThread")
}

func TestAReplyThatIsNotAResolutionLeavesTheThreadOpen(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodPost, "/api/v3/repos/acme/widget/pulls/7/comments/991/replies", http.StatusCreated, `{"id":992}`)

	err := providers.NewGitHubHost(host.client(), "acme", "widget").
		Reply(t.Context(), 7, 991, "➡️ Sigue presente en la iteración 3 — 2026-09-18.", false)

	require.NoError(t, err)
	assert.Len(t, host.requests, 1)
}

// The reply is what a person reads; the thread's own state is bookkeeping. A failure closing it is
// not a failed publish.
func TestAFailedResolveDoesNotFailTheReply(t *testing.T) {
	t.Run("github", func(t *testing.T) {
		host := newFakeGitHub(t)
		host.answer(http.MethodPost, "/api/v3/repos/acme/widget/pulls/7/comments/991/replies", http.StatusCreated, `{"id":992}`)
		host.answer(http.MethodPost, "/api/graphql", http.StatusBadGateway, `{"errors":[]}`)

		err := providers.NewGitHubHost(host.client(), "acme", "widget").Reply(t.Context(), 7, 991, "✔️", true)

		assert.NoError(t, err)
	})

	t.Run("azure", func(t *testing.T) {
		host := newFakeAzure(t)
		host.answer(http.MethodPost, "/contoso/Dev/_apis/git/repositories/r/pullRequests/7/threads/555/comments",
			http.StatusCreated, `{"id":2}`)
		host.answer(http.MethodPatch, "/contoso/Dev/_apis/git/repositories/r/pullRequests/7/threads/555",
			http.StatusInternalServerError, `{"message":"boom"}`)

		err := providers.NewAzureHost(host.client("contoso"), "Dev", "r").Reply(t.Context(), 7, 555, "✔️", true)

		assert.NoError(t, err)
	})
}

func TestAzureMarksAResolvedThreadFixed(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodPost, "/contoso/Dev/_apis/git/repositories/r/pullRequests/7/threads/555/comments",
		http.StatusCreated, `{"id":2}`)
	host.answer(http.MethodPatch, "/contoso/Dev/_apis/git/repositories/r/pullRequests/7/threads/555",
		http.StatusOK, `{}`)

	err := providers.NewAzureHost(host.client("contoso"), "Dev", "r").
		Reply(t.Context(), 7, 555, "✔️ _Resuelto en la iteración 3 — 2026-09-18. Marcado como fixed._", true)

	require.NoError(t, err)
	seen := host.seen()
	require.Len(t, seen, 2)
	assert.JSONEq(t, `{"status":2}`, seen[1].Body, "2 is fixed")
}

// A failing reply **is** a failed publish: it is the thing the reviewer came to say.
func TestAFailedReplyIsReported(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodPost, "/api/v3/repos/acme/widget/pulls/7/comments/991/replies",
		http.StatusForbidden, `{"message":"no"}`)

	err := providers.NewGitHubHost(host.client(), "acme", "widget").Reply(t.Context(), 7, 991, "x", true)

	assert.Error(t, err)
}

// ---- which end the summary goes on (DIVERGENCE-PROV-d) ----------------------------------------

// GitHub's conversation runs oldest-first, so the summary is posted first to sit above the
// findings; Azure's overview shows the newest thread on top, so the same goal means posting it last.
// A plain "post it last" would fix Azure and break GitHub — the same defect, moved.
func TestEachHostSaysWhichEndPutsTheSummaryOnTop(t *testing.T) {
	github := newFakeGitHub(t)
	azure := newFakeAzure(t)

	assert.False(t, providers.NewGitHubHost(github.client(), "acme", "widget").DiscussionNewestFirst(),
		"GitHub's conversation runs oldest-first, so the summary is posted first")
	assert.True(t, providers.NewAzureHost(azure.client("contoso"), "Dev", "r").DiscussionNewestFirst(),
		"Azure's overview shows the newest on top, so the summary is posted last")
}
