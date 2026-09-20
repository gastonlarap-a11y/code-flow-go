package providers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/providers"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/sentinel"
)

// The port of GitHubClientTests. A real HTTP server rather than a fake transport: what this client
// has to get right is all on the wire — the four headers, the query strings, the bodies, the status
// mapping — and a fake that answers structs proves none of it.

// recordedRequest is what the fake host saw.
type recordedRequest struct {
	Method string
	Path   string
	Query  string
	Header http.Header
	Body   string
}

// route answers one path with one canned response.
type route struct {
	status int
	body   string
}

// fakeGitHub serves canned answers and records every request.
type fakeGitHub struct {
	t        *testing.T
	routes   map[string]route
	requests []recordedRequest
	server   *httptest.Server
}

// newFakeGitHub starts a TLS server, because the client builds `https://` roots and nothing in it
// may be relaxed to make a test easier: a GitHub token travelling over plain HTTP is the failure
// this would be hiding. `server.Client()` is what trusts the throwaway certificate.
func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	host := &fakeGitHub{t: t, routes: map[string]route{}}
	host.server = httptest.NewTLSServer(http.HandlerFunc(host.serve))
	t.Cleanup(host.server.Close)
	return host
}

// hostname is what the client is pointed at: the server's address, which puts it on the Enterprise
// branch of the API-root rule — `https://{host}/api/v3`.
func (f *fakeGitHub) hostname() string {
	return strings.TrimPrefix(f.server.URL, "https://")
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	// io.ReadAll, not one Read: a body split across packets would otherwise arrive truncated and
	// the assertion would be about the test, not the client.
	body, err := io.ReadAll(r.Body)
	require.NoError(f.t, err)

	f.requests = append(f.requests, recordedRequest{
		Method: r.Method,
		Path:   r.URL.Path,
		Query:  r.URL.RawQuery,
		Header: r.Header.Clone(),
		Body:   string(body),
	})

	answer, found := f.routes[r.Method+" "+r.URL.Path]
	if !found {
		// A route the test did not set up is a mistake in the test, and saying which one saves
		// reading the client to find out what it asked for.
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"message":"no route for %s %s in this test"}`, r.Method, r.URL.Path)
		return
	}
	w.WriteHeader(answer.status)
	_, _ = io.WriteString(w, answer.body)
}

func (f *fakeGitHub) answer(method, path string, status int, body string) {
	f.routes[method+" "+path] = route{status: status, body: body}
}

// client points a GitHub client at the fake host.
func (f *fakeGitHub) client() providers.GitHubClient {
	return providers.NewGitHubClient(f.server.Client(), f.hostname(), "gho_token")
}

func (f *fakeGitHub) last() recordedRequest {
	f.t.Helper()
	require.NotEmpty(f.t, f.requests)
	return f.requests[len(f.requests)-1]
}

// ---- host resolution and headers (PROV-001, PROV-002, PROV-004) -------------------------------

// The public host and an Enterprise server get different roots, and the GraphQL root is not the
// REST one with a suffix — on Enterprise it is a different path entirely.
func TestTheAPIRootDependsOnTheHost(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodGet, "/api/v3/user", http.StatusOK, `{"login":"gaston"}`)

	login, err := host.client().AuthenticatedUser(t.Context())

	require.NoError(t, err)
	assert.Equal(t, "gaston", login)
	assert.Equal(t, "/api/v3/user", host.last().Path, "any host that is not github.com is an Enterprise server")
}

func TestEveryRestCallCarriesTheFourHeaders(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodGet, "/api/v3/user", http.StatusOK, `{"login":"gaston"}`)

	_, err := host.client().AuthenticatedUser(t.Context())
	require.NoError(t, err)

	header := host.last().Header
	assert.Equal(t, "Bearer gho_token", header.Get("Authorization"), "one scheme for both token kinds")
	assert.Equal(t, "application/vnd.github+json", header.Get("Accept"))
	assert.Equal(t, "CodeFlow", header.Get("User-Agent"), "GitHub answers 403 with no User-Agent")
	assert.Equal(t, "2022-11-28", header.Get("X-GitHub-Api-Version"), "pinned to a dated snapshot")
}

// Every status that is not a self-approval reads alike, as it did in 2.x: the shape carries the
// code and the server's own body, and nothing branches on it.
func TestEveryOtherStatusIsUndifferentiated(t *testing.T) {
	for _, status := range []int{
		http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusUnprocessableEntity,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			host := newFakeGitHub(t)
			host.answer(http.MethodGet, "/api/v3/user", status, `{"message":"nope"}`)

			_, err := host.client().AuthenticatedUser(t.Context())

			require.Error(t, err)
			assert.Equal(t, fmt.Sprintf(`GitHub returned %d: {"message":"nope"}`, status), err.Error())

			var failure *providers.GitHubError
			require.ErrorAs(t, err, &failure)
			assert.Equal(t, status, failure.Status)
			assert.False(t, failure.SelfApproval, "only one 422 is told apart")
		})
	}
}

func TestATransportFailureSaysTheCallNeverLanded(t *testing.T) {
	// A server that is closed before the call: the connection is refused, which is the shape of
	// every "never reached the far side" failure.
	host := newFakeGitHub(t)
	client := host.client()
	host.server.Close()

	_, err := client.AuthenticatedUser(t.Context())

	require.Error(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), "couldn't reach GitHub: "), err.Error())

	var failure *providers.GitHubError
	require.ErrorAs(t, err, &failure)
	assert.Zero(t, failure.Status, "a transport failure has no status to report")
}

func TestABodyThatIsNotTheExpectedShapeSaysSo(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodGet, "/api/v3/user", http.StatusOK, `["not","an","object"]`)

	_, err := host.client().AuthenticatedUser(t.Context())

	require.Error(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), "unexpected response from GitHub: "), err.Error())
	assert.NotContains(t, err.Error(), "couldn't reach GitHub",
		"it reached GitHub — what came back was not the shape the call expected")
	assert.ErrorIs(t, err, providers.ErrGitHubDecode)
}

// ---- the one status that is classified (DIVERGENCE-PROV-c, XLANG-013) -------------------------

func TestApprovingYourOwnPullRequestIsToldApart(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodPost, "/api/v3/repos/acme/widget/pulls/7/reviews", http.StatusUnprocessableEntity,
		`{"message":"Unprocessable Entity","errors":["Review Can not approve your own pull request"]}`)

	err := host.client().SubmitReview(t.Context(), "acme", "widget", 7, providers.GitHubEventApprove, "")

	require.Error(t, err)
	var failure *providers.GitHubError
	require.ErrorAs(t, err, &failure)
	assert.True(t, failure.SelfApproval)
	assert.NotContains(t, err.Error(), sentinel.SelfApproval,
		"the sentinel is added at the command boundary, never here")
}

// GitHub's own sentence, in whatever case it arrives and whichever shape the errors array takes.
func TestTheSelfApprovalSentenceIsMatchedLoosely(t *testing.T) {
	bodies := map[string]string{
		"as a string":     `{"errors":["Review Can not approve your own pull request"]}`,
		"as an object":    `{"errors":[{"message":"Can not approve your own pull request"}]}`,
		"in another case": `{"errors":["can not APPROVE your own pull request"]}`,
		"in the message":  `{"message":"Can not approve your own pull request"}`,
	}

	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			host := newFakeGitHub(t)
			host.answer(http.MethodPost, "/api/v3/repos/acme/widget/pulls/7/reviews",
				http.StatusUnprocessableEntity, body)

			err := host.client().SubmitReview(t.Context(), "acme", "widget", 7, providers.GitHubEventApprove, "")

			var failure *providers.GitHubError
			require.ErrorAs(t, err, &failure)
			assert.True(t, failure.SelfApproval)
		})
	}
}

// A 422 that is not this one stays raw — the `REQUEST_CHANGES` with an empty body, for instance.
func TestAnyOther422StaysRaw(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodPost, "/api/v3/repos/acme/widget/pulls/7/reviews", http.StatusUnprocessableEntity,
		`{"message":"Unprocessable Entity","errors":["Body can't be blank"]}`)

	err := host.client().SubmitReview(t.Context(), "acme", "widget", 7, providers.GitHubEventRequestChanges, "")

	var failure *providers.GitHubError
	require.ErrorAs(t, err, &failure)
	assert.False(t, failure.SelfApproval)
}

// ---- pull requests (PROV-006, PROV-007, PROV-009, PROV-010) -----------------------------------

const onePull = `[{
	"number": 42,
	"title": "Add the thing",
	"body": "why it is needed",
	"state": "open",
	"draft": false,
	"merged_at": null,
	"head": { "ref": "feat/thing", "sha": "abc123" },
	"base": { "ref": "main" },
	"user": { "login": "gaston" },
	"created_at": "2026-09-01T10:00:00Z",
	"html_url": "https://github.com/acme/widget/pull/42"
}]`

func TestListingPullRequestsMapsThemToTheRenderersShape(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls", http.StatusOK, onePull)

	pulls, err := host.client().ListPullRequests(t.Context(), "acme", "widget")

	require.NoError(t, err)
	require.Len(t, pulls, 1)
	assert.Equal(t, providers.PullRequestSummary{
		ID:           42,
		Title:        "Add the thing",
		Description:  "why it is needed",
		Status:       providers.StatusOpen,
		SourceBranch: "feat/thing",
		TargetBranch: "main",
		Author:       "gaston",
		CreatedAt:    "2026-09-01T10:00:00Z",
		URL:          "https://github.com/acme/widget/pull/42",
		Provider:     providers.ProviderGitHub,
	}, pulls[0])
	assert.Equal(t, "state=all&per_page=100&sort=created&direction=desc", host.last().Query,
		"one page of the newest hundred, as in 2.x")
}

// An empty repository answers an empty array, not null: the sidebar maps over it.
func TestAnEmptyPullRequestListIsAnArray(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls", http.StatusOK, `[]`)

	pulls, err := host.client().ListPullRequests(t.Context(), "acme", "widget")

	require.NoError(t, err)
	require.NotNil(t, pulls)
	encoded, err := json.Marshal(pulls)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(encoded))
}

// A null body is an empty description, not a missing field: the renderer types it as a string.
func TestAPullRequestWithNoBodyHasAnEmptyDescription(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls/42", http.StatusOK,
		strings.TrimSuffix(strings.TrimPrefix(onePull, "["), "]"))

	pull, err := host.client().GetPullRequest(t.Context(), "acme", "widget", 42)
	require.NoError(t, err)
	assert.Equal(t, "why it is needed", pull.Description)

	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls/43", http.StatusOK,
		`{"number":43,"title":"t","body":null,"state":"open","head":{"ref":"a","sha":"s"},"base":{"ref":"main"},"user":{"login":"g"},"created_at":"x","html_url":"u"}`)

	pull, err = host.client().GetPullRequest(t.Context(), "acme", "widget", 43)
	require.NoError(t, err)
	assert.Empty(t, pull.Description)
}

// Merge is checked before the state, because a merged pull request is also closed — reporting it as
// closed would file every merged one under the wrong heading.
func TestStatusBucketing(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		expected string
	}{
		{name: "open", body: `{"state":"open","draft":false,"merged_at":null}`, expected: providers.StatusOpen},
		{name: "draft", body: `{"state":"open","draft":true,"merged_at":null}`, expected: providers.StatusDraft},
		{name: "closed", body: `{"state":"closed","draft":false,"merged_at":null}`, expected: providers.StatusClosed},
		{name: "merged", body: `{"state":"closed","draft":false,"merged_at":"2026-09-02T00:00:00Z"}`, expected: providers.StatusMerged},
		{name: "merged while still marked draft", body: `{"state":"closed","draft":true,"merged_at":"2026-09-02T00:00:00Z"}`, expected: providers.StatusMerged},
		// The field being **present** is the question 2.x asked, and GitHub only ever sends a
		// timestamp or null. An empty string is neither, and is bucketed as 2.x would have.
		{name: "a present merged_at counts, whatever is in it", body: `{"state":"open","draft":false,"merged_at":""}`, expected: providers.StatusMerged},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			host := newFakeGitHub(t)
			host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls/1", http.StatusOK, test.body)

			pull, err := host.client().GetPullRequest(t.Context(), "acme", "widget", 1)

			require.NoError(t, err)
			assert.Equal(t, test.expected, pull.Status)
		})
	}
}

func TestTheHeadSHAIsReadFreshAndRefusedWhenEmpty(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls/7", http.StatusOK,
		`{"number":7,"head":{"ref":"f","sha":"deadbeef"},"base":{"ref":"main"},"user":{"login":"g"}}`)

	sha, err := host.client().HeadSHA(t.Context(), "acme", "widget", 7)
	require.NoError(t, err)
	assert.Equal(t, "deadbeef", sha)

	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls/8", http.StatusOK,
		`{"number":8,"head":{"ref":"f","sha":""},"base":{"ref":"main"},"user":{"login":"g"}}`)

	_, err = host.client().HeadSHA(t.Context(), "acme", "widget", 8)
	assert.ErrorIs(t, err, providers.ErrNoHeadCommit, "an empty SHA anchors comments to nothing")
}

func TestCreatingAPullRequestSendsBranchNamesAndReadsBackTheHostsShape(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodPost, "/api/v3/repos/acme/widget/pulls", http.StatusCreated,
		`{"number":9,"title":"Add the thing","body":"b","state":"open","draft":true,"head":{"ref":"feat/x","sha":"s"},"base":{"ref":"main"},"user":{"login":"gaston"},"created_at":"c","html_url":"u"}`)

	pull, err := host.client().CreatePullRequest(t.Context(), "acme", "widget", providers.NewPullRequest{
		Title: "Add the thing", Description: "b", SourceBranch: "feat/x", TargetBranch: "main", Draft: true,
	})

	require.NoError(t, err)
	assert.Equal(t, int64(9), pull.ID)
	assert.Equal(t, providers.StatusDraft, pull.Status)
	assert.JSONEq(t, `{"title":"Add the thing","head":"feat/x","base":"main","body":"b","draft":true}`,
		host.last().Body, "branch names, not refs")
}

// ---- the diff and its fallback (PROV-008) -----------------------------------------------------

func TestTheDiffComesFromGitHubsOwnRendering(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls/7", http.StatusOK,
		"diff --git a/a.txt b/a.txt\n@@ -1 +1 @@\n-uno\n+dos\n")

	diff, err := host.client().PullRequestDiff(t.Context(), "acme", "widget", 7)

	require.NoError(t, err)
	assert.Contains(t, diff, "+dos")
	assert.Equal(t, "application/vnd.github.diff", host.last().Header.Get("Accept"),
		"the media type is what makes GitHub render it")
}

// An empty 2xx body is GitHub declining past its own size limit — not an empty diff.
func TestAnEmptyDiffBodyFallsBackToThePerFileReassembly(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls/7", http.StatusOK, "   \n")
	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls/7/files", http.StatusOK, `[
		{"filename":"src/app.ts","status":"modified","patch":"@@ -1 +1 @@\n-uno\n+dos"},
		{"filename":"new.txt","status":"added","patch":"@@ -0,0 +1 @@\n+hola\n"},
		{"filename":"gone.txt","status":"removed","patch":"@@ -1 +0,0 @@\n-adios\n"},
		{"filename":"moved.txt","previous_filename":"old.txt","status":"renamed","patch":"@@ -1 +1 @@\n-a\n+b\n"},
		{"filename":"logo.png","status":"modified","patch":null}
	]`)

	diff, err := host.client().PullRequestDiff(t.Context(), "acme", "widget", 7)

	require.NoError(t, err)
	assert.Contains(t, diff, "diff --git a/src/app.ts b/src/app.ts\n--- a/src/app.ts\n+++ b/src/app.ts\n@@ -1 +1 @@\n-uno\n+dos\n",
		"a patch with no trailing newline gets one, so the next header starts on its own line")
	assert.Contains(t, diff, "--- /dev/null\n+++ b/new.txt", "an added file has no old side")
	assert.Contains(t, diff, "--- a/gone.txt\n+++ /dev/null", "a removed file has no new side")
	assert.Contains(t, diff, "diff --git a/old.txt b/moved.txt\n--- a/old.txt\n+++ b/moved.txt",
		"a rename names both paths")
	assert.Contains(t, diff, "diff --git a/logo.png b/logo.png\n--- a/logo.png\n+++ b/logo.png\n(binary or too large to display)\n",
		"a file with no patch still tells the reviewer it changed")
}

func TestANon2xxDiffAlsoFallsBack(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls/7", http.StatusNotAcceptable, "no")
	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls/7/files", http.StatusOK,
		`[{"filename":"a.txt","status":"modified","patch":"@@ -1 +1 @@\n-x\n+y\n"}]`)

	diff, err := host.client().PullRequestDiff(t.Context(), "acme", "widget", 7)

	require.NoError(t, err)
	assert.Contains(t, diff, "+y")
}

func TestAPullRequestWithNoChangedFilesIsRefused(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls/7", http.StatusOK, "")
	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls/7/files", http.StatusOK, `[]`)

	_, err := host.client().PullRequestDiff(t.Context(), "acme", "widget", 7)

	assert.ErrorIs(t, err, providers.ErrNoChangedFiles)
}

// Paging stops at three pages — GitHub's own ceiling — and stops early on a short page rather than
// asking for two more it knows are empty.
func TestTheFilesFallbackStopsAtGitHubsCeiling(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls/7", http.StatusOK, "")

	full := make([]string, 0, 100)
	for i := range 100 {
		full = append(full, fmt.Sprintf(`{"filename":"f%d.txt","status":"modified","patch":"@@ -1 +1 @@\n-a\n+b\n"}`, i))
	}
	page := "[" + strings.Join(full, ",") + "]"

	pages := 0
	host.routes = map[string]route{}
	host.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/files") {
			pages++
			_, _ = io.WriteString(w, page)
			return
		}
		_, _ = io.WriteString(w, "")
	})

	_, err := host.client().PullRequestDiff(t.Context(), "acme", "widget", 7)

	require.NoError(t, err)
	assert.Equal(t, 3, pages, "300 files is GitHub's hard ceiling for this endpoint")
}

// ---- comments (PROV-011, PROV-012, PROV-013) --------------------------------------------------

func TestAnAnchoredCommentSendsTheRangeGitHubAccepts(t *testing.T) {
	tests := []struct {
		name      string
		startLine int64
		endLine   int64
		expected  string
	}{
		{
			name: "a single line omits the range entirely", startLine: 12, endLine: 12,
			expected: `{"body":"finding","path":"src/app.ts","line":12,"side":"RIGHT","commit_id":"sha1"}`,
		},
		{
			name: "a real range carries both ends", startLine: 12, endLine: 15,
			expected: `{"body":"finding","path":"src/app.ts","line":15,"side":"RIGHT","commit_id":"sha1","start_line":12,"start_side":"RIGHT"}`,
		},
		{
			// `line` is the larger end whichever way round the caller passed them, and with the
			// two equal the range is dropped: GitHub 422s a comment that carries
			// `start_line == line`.
			name: "an inverted range collapses to the line it ends on", startLine: 15, endLine: 12,
			expected: `{"body":"finding","path":"src/app.ts","line":15,"side":"RIGHT","commit_id":"sha1"}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			host := newFakeGitHub(t)
			host.answer(http.MethodPost, "/api/v3/repos/acme/widget/pulls/7/comments", http.StatusCreated, `{"id":991}`)

			id, err := host.client().PostAnchoredComment(t.Context(), "acme", "widget", 7,
				"finding", "/src/app.ts", test.startLine, test.endLine, "sha1")

			require.NoError(t, err)
			assert.Equal(t, int64(991), id, "the id is what a re-review replies to")
			assert.JSONEq(t, test.expected, host.last().Body)
		})
	}
}

// The inverted range above is the case worth naming: `line` is the larger end, and with
// `start_line == line` GitHub 422s, so the range collapses to a single-line comment instead.
func TestAnInvertedRangeDoesNotSendAnEqualStartLine(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodPost, "/api/v3/repos/acme/widget/pulls/7/comments", http.StatusCreated, `{"id":1}`)

	_, err := host.client().PostAnchoredComment(t.Context(), "acme", "widget", 7, "f", "a.txt", 15, 15, "sha")

	require.NoError(t, err)
	assert.NotContains(t, host.last().Body, "start_line")
}

func TestAConversationCommentGoesThroughTheIssuesPath(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodPost, "/api/v3/repos/acme/widget/issues/7/comments", http.StatusCreated, `{"id":5}`)

	id, err := host.client().PostComment(t.Context(), "acme", "widget", 7, "the summary")

	require.NoError(t, err)
	assert.Equal(t, int64(5), id)
	assert.Equal(t, "/api/v3/repos/acme/widget/issues/7/comments", host.last().Path,
		"GitHub models a pull request as an issue for unanchored comments")
	assert.JSONEq(t, `{"body":"the summary"}`, host.last().Body)
}

func TestAReplyThreadsUnderItsRootComment(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodPost, "/api/v3/repos/acme/widget/pulls/7/comments/991/replies", http.StatusCreated, `{"id":992}`)

	err := host.client().ReplyToComment(t.Context(), "acme", "widget", 7, 991, "still here")

	require.NoError(t, err)
	assert.JSONEq(t, `{"body":"still here"}`, host.last().Body)
}

// ---- the viewer's decision (PROV-015) ---------------------------------------------------------

func TestTheViewersLastVerdictWins(t *testing.T) {
	tests := []struct {
		name     string
		reviews  string
		expected string
	}{
		{
			name:     "nothing recorded",
			reviews:  `[]`,
			expected: providers.DecisionNone,
		},
		{
			name:     "someone else's approval is not the viewer's",
			reviews:  `[{"user":{"login":"otro"},"state":"APPROVED"}]`,
			expected: providers.DecisionNone,
		},
		{
			name:     "an approval",
			reviews:  `[{"user":{"login":"gaston"},"state":"APPROVED"}]`,
			expected: providers.DecisionApproved,
		},
		{
			name:     "a later request for changes replaces it",
			reviews:  `[{"user":{"login":"gaston"},"state":"APPROVED"},{"user":{"login":"gaston"},"state":"CHANGES_REQUESTED"}]`,
			expected: providers.DecisionChangesRequested,
		},
		{
			name:     "a dismissal takes a verdict back",
			reviews:  `[{"user":{"login":"gaston"},"state":"APPROVED"},{"user":{"login":"gaston"},"state":"DISMISSED"}]`,
			expected: providers.DecisionNone,
		},
		{
			name:     "comments and pending reviews leave it alone",
			reviews:  `[{"user":{"login":"gaston"},"state":"APPROVED"},{"user":{"login":"gaston"},"state":"COMMENTED"},{"user":{"login":"gaston"},"state":"PENDING"}]`,
			expected: providers.DecisionApproved,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			host := newFakeGitHub(t)
			host.answer(http.MethodGet, "/api/v3/user", http.StatusOK, `{"login":"gaston"}`)
			host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls/7/reviews", http.StatusOK, test.reviews)

			decision, err := host.client().ViewerDecision(t.Context(), "acme", "widget", 7)

			require.NoError(t, err)
			assert.Equal(t, test.expected, decision)
		})
	}
}

// ---- submitting and closing (PROV-016, PROV-017) ----------------------------------------------

func TestAnApprovalWithNoCommentOmitsTheBody(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodPost, "/api/v3/repos/acme/widget/pulls/7/reviews", http.StatusOK, `{}`)

	require.NoError(t, host.client().SubmitReview(t.Context(), "acme", "widget", 7, providers.GitHubEventApprove, "   "))
	assert.JSONEq(t, `{"event":"APPROVE"}`, host.last().Body, "omitted, not sent empty")

	require.NoError(t, host.client().SubmitReview(t.Context(), "acme", "widget", 7, providers.GitHubEventRequestChanges, "please fix"))
	assert.JSONEq(t, `{"event":"REQUEST_CHANGES","body":"please fix"}`, host.last().Body)
}

func TestClosingIsAPatchNotAMerge(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodPatch, "/api/v3/repos/acme/widget/pulls/7", http.StatusOK, `{}`)

	require.NoError(t, host.client().ClosePullRequest(t.Context(), "acme", "widget", 7))

	assert.Equal(t, http.MethodPatch, host.last().Method)
	assert.JSONEq(t, `{"state":"closed"}`, host.last().Body)
}

// ---- comment threads (PROV-018) ---------------------------------------------------------------

func TestInlineCommentsAreGroupedByTheirRootAndConversationOnesFollow(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls/7/comments", http.StatusOK, `[
		{"id":100,"path":"src/app.ts","line":12,"start_line":10,"body":"first finding","created_at":"t1","user":{"login":"gaston"}},
		{"id":200,"path":"other.ts","line":4,"body":"second finding","created_at":"t2","user":{"login":"gaston"}},
		{"id":101,"in_reply_to_id":100,"path":"src/app.ts","line":12,"body":"a reply","created_at":"t3","user":{"login":"otro"}},
		{"id":102,"in_reply_to_id":100,"path":"src/app.ts","line":12,"body":"   ","created_at":"t4","user":{"login":"otro"}}
	]`)
	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/issues/7/comments", http.StatusOK, `[
		{"id":900,"body":"the summary","created_at":"t5","user":{"login":"gaston"}},
		{"id":901,"body":"","created_at":"t6","user":null}
	]`)

	threads, err := host.client().ListCommentThreads(t.Context(), "acme", "widget", 7)

	require.NoError(t, err)
	require.Len(t, threads, 3, "two inline threads and one conversation comment")

	assert.Equal(t, int64(100), threads[0].ID, "threads keep the order their root was first seen in")
	require.NotNil(t, threads[0].FilePath)
	assert.Equal(t, "src/app.ts", *threads[0].FilePath)
	require.NotNil(t, threads[0].StartLine)
	assert.Equal(t, int64(10), *threads[0].StartLine)
	require.NotNil(t, threads[0].EndLine)
	assert.Equal(t, int64(12), *threads[0].EndLine)
	require.Len(t, threads[0].Comments, 2, "the whitespace-only reply is dropped before grouping")
	assert.Equal(t, "a reply", threads[0].Comments[1].Content)
	assert.Equal(t, "otro", threads[0].Comments[1].Author)

	assert.Equal(t, int64(200), threads[1].ID)
	require.NotNil(t, threads[1].StartLine)
	assert.Equal(t, int64(4), *threads[1].StartLine, "with no start_line the line itself is the start")

	assert.Equal(t, int64(900), threads[2].ID)
	assert.Nil(t, threads[2].FilePath, "a conversation comment has no location")
	assert.Nil(t, threads[2].StartLine)
	require.Len(t, threads[2].Comments, 1)
}

func TestAPullRequestWithNoCommentsAnswersAnArray(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls/7/comments", http.StatusOK, `[]`)
	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/issues/7/comments", http.StatusOK, `[]`)

	threads, err := host.client().ListCommentThreads(t.Context(), "acme", "widget", 7)

	require.NoError(t, err)
	encoded, err := json.Marshal(threads)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(encoded))
}

// ---- resolving a thread, the one GraphQL call (PROV-014) --------------------------------------

func TestResolvingAThreadFindsItsNodeIDThenMutatesIt(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodPost, "/api/graphql", http.StatusOK, `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[
		{"id":"NODE_A","isResolved":false,"comments":{"nodes":[{"databaseId":1},{"databaseId":2}]}},
		{"id":"NODE_B","isResolved":false,"comments":{"nodes":[{"databaseId":991}]}}
	]}}}}}`)

	err := host.client().ResolveReviewThreadForComment(t.Context(), "acme", "widget", 7, 991)

	require.NoError(t, err)
	require.Len(t, host.requests, 2, "find the thread, then resolve it")

	var query struct {
		Query string `json:"query"`
	}
	require.NoError(t, json.Unmarshal([]byte(host.requests[0].Body), &query))
	assert.Equal(t,
		`query { repository(owner: "acme", name: "widget") { pullRequest(number: 7) { reviewThreads(first: 100) { nodes { id isResolved comments(first: 100) { nodes { databaseId } } } } } } }`,
		query.Query, "VERBATIM, including both caps")

	require.NoError(t, json.Unmarshal([]byte(host.requests[1].Body), &query))
	assert.Equal(t,
		`mutation { resolveReviewThread(input: { threadId: "NODE_B" }) { thread { isResolved } } }`,
		query.Query, "the node id from the first call, not the comment's databaseId")

	// The REST headers are deliberately absent on this call.
	assert.Equal(t, "Bearer gho_token", host.requests[0].Header.Get("Authorization"))
	assert.Equal(t, "CodeFlow", host.requests[0].Header.Get("User-Agent"))
	assert.Empty(t, host.requests[0].Header.Get("X-GitHub-Api-Version"), "a REST-only header")
	assert.Empty(t, host.requests[0].Header.Get("Accept"), "a REST-only header")
}

func TestResolvingReportsWhatItCouldNotFind(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		expected error
		contains string
	}{
		{
			name:     "no threads in the response shape",
			status:   http.StatusOK,
			body:     `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}`,
			expected: providers.ErrNoReviewThreads,
		},
		{
			name:     "no thread holds the comment",
			status:   http.StatusOK,
			body:     `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"id":"A","comments":{"nodes":[{"databaseId":1}]}}]}}}}}`,
			expected: providers.ErrThreadNotFound,
		},
		{
			name:     "a non-2xx names the call and the status, with no body",
			status:   http.StatusBadGateway,
			body:     `{"errors":[{"message":"something long"}]}`,
			contains: "GitHub GraphQL returned 502",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			host := newFakeGitHub(t)
			host.answer(http.MethodPost, "/api/graphql", test.status, test.body)

			err := host.client().ResolveReviewThreadForComment(t.Context(), "acme", "widget", 7, 991)

			require.Error(t, err)
			if test.expected != nil {
				assert.ErrorIs(t, err, test.expected)
				return
			}
			assert.Equal(t, test.contains, err.Error(), "the GraphQL errors carry no body, unlike REST's")
		})
	}
}

// ---- the command (PROV-005, PROV-043) ---------------------------------------------------------

func TestAskingWhoATokenBelongsTo(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodGet, "/api/v3/user", http.StatusOK, `{"login":"gaston"}`)
	hostname := host.hostname()

	svc := newProviderService(t, providers.Deps{
		Projects:    &fakeProjects{},
		Credentials: fakeCredentials{githubHosts: map[string]bool{hostname: true}},
		HTTP:        host.server.Client(),
	})

	out := call(t, svc, "github_authenticated_user", fmt.Sprintf(`{"host":%q}`, hostname))

	assert.JSONEq(t, `"gaston"`, string(out))
	assert.Equal(t, "Bearer token-for-"+hostname, host.last().Header.Get("Authorization"),
		"the token comes from the store, keyed by host")
}

func TestAskingWhoATokenBelongsToWithoutOneUsesTheOtherWording(t *testing.T) {
	svc := newProviderService(t, providers.Deps{
		Projects:    &fakeProjects{},
		Credentials: fakeCredentials{},
	})

	_, err := svc.Invoke(t.Context(), "github_authenticated_user", json.RawMessage(`{"host":"github.com"}`))

	require.Error(t, err)
	assert.Equal(t, "No GitHub token saved for this host", err.Error())
}

// A keychain that refuses is not an absent token: the renderer offers "reconnect" on the sentinel
// and "connect" on the sentence above, and the two are different screens.
func TestARefusedCredentialStoreCarriesTheSentinel(t *testing.T) {
	svc := newProviderService(t, providers.Deps{
		Projects: &fakeProjects{},
		Credentials: fakeCredentials{
			readErr: fmt.Errorf("%w: User interaction is not allowed", providers.ErrCredentialRefused),
		},
	})

	_, err := svc.Invoke(t.Context(), "github_authenticated_user", json.RawMessage(`{"host":"github.com"}`))

	require.Error(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), sentinel.CredentialRefused),
		"the renderer matches this with startsWith, at position 0")
	assert.Contains(t, err.Error(), "User interaction is not allowed", "the platform's own words survive")
}

// An empty stored token is the same state as none — a request with an empty Bearer would just 401.
func TestAnEmptyStoredTokenReadsAsNoToken(t *testing.T) {
	svc := newProviderService(t, providers.Deps{
		Projects: &fakeProjects{},
		Credentials: fakeCredentials{
			githubHosts: map[string]bool{"github.com": true},
			tokens:      map[string]string{"github.com": "   "},
		},
	})

	_, err := svc.Invoke(t.Context(), "github_authenticated_user", json.RawMessage(`{"host":"github.com"}`))

	assert.ErrorIs(t, err, providers.ErrNoGitHubToken)
}

func TestTheAuthenticatedUserCommandNeedsItsHost(t *testing.T) {
	svc := newProviderService(t, providers.Deps{Projects: &fakeProjects{}, Credentials: fakeCredentials{}})

	_, err := svc.Invoke(t.Context(), "github_authenticated_user", json.RawMessage(`{}`))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing required parameter 'host'")
}

// The token must not reach anything but the Authorization header (SEC-007).
func TestTheTokenNeverTravelsInABody(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodPost, "/api/v3/repos/acme/widget/pulls", http.StatusCreated,
		`{"number":1,"head":{"ref":"a","sha":"s"},"base":{"ref":"main"},"user":{"login":"g"}}`)
	host.answer(http.MethodPost, "/api/v3/repos/acme/widget/issues/1/comments", http.StatusCreated, `{"id":1}`)

	client := providers.NewGitHubClient(host.server.Client(), host.hostname(), "gho_secret_value")

	_, err := client.CreatePullRequest(t.Context(), "acme", "widget", providers.NewPullRequest{Title: "t"})
	require.NoError(t, err)
	_, err = client.PostComment(t.Context(), "acme", "widget", 1, "a comment")
	require.NoError(t, err)

	for _, request := range host.requests {
		assert.NotContains(t, request.Body, "gho_secret_value", "%s %s", request.Method, request.Path)
		assert.Equal(t, "Bearer gho_secret_value", request.Header.Get("Authorization"))
	}
}

// A cancelled call is reported as a transport failure rather than hanging: the context a Wails call
// hands a handler is cancelled when the call returns, and a client that ignored it would keep a
// request alive past the window that asked for it.
func TestACancelledCallComesBackAsUnreachable(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodGet, "/api/v3/user", http.StatusOK, `{"login":"gaston"}`)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := host.client().AuthenticatedUser(ctx)

	require.Error(t, err)
	var failure *providers.GitHubError
	require.ErrorAs(t, err, &failure)
	assert.Zero(t, failure.Status, "it never reached the far side")
}
