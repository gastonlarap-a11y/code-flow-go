package providers_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/providers"
)

// The port of AzureClientTests, AzureDiffTests and AzurePostingTests. Same shape as the GitHub
// tests: a real server, and every assertion about what actually went over the wire.
//
// Azure's client is pointed at `dev.azure.com` by construction — the URLs are built from that host,
// not from a configurable root — so the fake server is reached by giving the client a transport
// that redirects every request to it. That is the seam that keeps the URL-building under test
// rather than replaced.

type fakeAzure struct {
	t      *testing.T
	routes map[string]route
	server *httptest.Server

	// The diff renders several files at once, so the recorder is written from several goroutines.
	// The mutex is the test's own; the client needs none.
	mutex    sync.Mutex
	requests []recordedRequest
}

func newFakeAzure(t *testing.T) *fakeAzure {
	t.Helper()
	host := &fakeAzure{t: t, routes: map[string]route{}}
	host.server = httptest.NewTLSServer(http.HandlerFunc(host.serve))
	t.Cleanup(host.server.Close)
	return host
}

func (f *fakeAzure) serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	require.NoError(f.t, err)

	// EscapedPath, not Path: the assertions here are about what was percent-encoded and what was
	// not (`BUG-PROV-a`), and Path hands back the decoded form, in which the two are identical.
	path := r.URL.EscapedPath()

	f.mutex.Lock()
	f.requests = append(f.requests, recordedRequest{
		Method: r.Method,
		Path:   path,
		Query:  r.URL.RawQuery,
		Header: r.Header.Clone(),
		Body:   string(body),
	})
	answer, found := f.routes[r.Method+" "+path]
	f.mutex.Unlock()

	if !found {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"message":"no route for %s %s in this test"}`, r.Method, path)
		return
	}
	w.WriteHeader(answer.status)
	_, _ = io.WriteString(w, answer.body)
}

// answer registers a route. The path is the **escaped** one, as the client sends it.
func (f *fakeAzure) answer(method, path string, status int, body string) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.routes[method+" "+path] = route{status: status, body: body}
}

// redirectToFake sends every request to the fake server while leaving the URL the client built
// intact, so the path and query it chose are what the assertions read.
type redirectToFake struct {
	target string
	next   http.RoundTripper
}

func (r redirectToFake) RoundTrip(request *http.Request) (*http.Response, error) {
	routed := request.Clone(request.Context())
	routed.URL.Scheme = "https"
	routed.URL.Host = strings.TrimPrefix(r.target, "https://")
	return r.next.RoundTrip(routed)
}

func (f *fakeAzure) client(org string) providers.AzureClient {
	transport := f.server.Client().Transport
	return providers.NewAzureClient(
		&http.Client{Transport: redirectToFake{target: f.server.URL, next: transport}},
		org, "the-pat")
}

func (f *fakeAzure) last() recordedRequest {
	f.t.Helper()
	f.mutex.Lock()
	defer f.mutex.Unlock()
	require.NotEmpty(f.t, f.requests)
	return f.requests[len(f.requests)-1]
}

// seen is a copy of every request, for the assertions that walk them all.
func (f *fakeAzure) seen() []recordedRequest {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return append([]recordedRequest{}, f.requests...)
}

// ---- auth, organisation and encoding (PROV-019, PROV-020, PROV-021) ---------------------------

func TestTheAzureHeaderIsBasicWithAnEmptyUsername(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodGet, "/contoso/_apis/projects", http.StatusOK, `{"value":[]}`)

	_, err := host.client("contoso").ListProjects(t.Context())
	require.NoError(t, err)

	header := host.last().Header.Get("Authorization")
	encoded := strings.TrimPrefix(header, "Basic ")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	require.NoError(t, err)
	assert.Equal(t, ":the-pat", string(decoded), "an empty username and the PAT as the password")
}

// Whatever the user saved as "organisation" reduces to the bare name — a raw `:` in the path is
// rejected by Azure's server with a 400 that explains nothing.
func TestTheOrganisationIsNormalisedWhateverWasSaved(t *testing.T) {
	tests := map[string]string{
		"contoso":                            "contoso",
		"  contoso  ":                        "contoso",
		"https://dev.azure.com/contoso":      "contoso",
		"https://dev.azure.com/contoso/":     "contoso",
		"http://dev.azure.com/contoso/Web":   "contoso",
		"https://contoso.visualstudio.com":   "contoso",
		"https://contoso.visualstudio.com/":  "contoso",
		"https://Contoso.VisualStudio.com/x": "Contoso",
		"":                                   "",
	}

	for saved, expected := range tests {
		t.Run(saved, func(t *testing.T) {
			assert.Equal(t, expected, providers.NormalizeAzureOrg(saved))
		})
	}
}

func TestTheOrganisationAndProjectAreEncodedIntoThePath(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodGet, "/my%20org/Marketing%20Website/_apis/git/repositories", http.StatusOK, `{"value":[]}`)

	_, err := host.client("my org").ListRepos(t.Context(), "Marketing Website")

	require.NoError(t, err)
	assert.Equal(t, "/my%20org/Marketing%20Website/_apis/git/repositories", host.last().Path)
}

// `BUG-PROV-a`, preserved: the repository id is percent-encoded on three calls and interpolated raw
// on every other one. What that *looks* like changes in Go (`DIVERGENCE-PROV-f`) — the URL layer
// escapes a space by itself, so the character the bug row names now behaves the same on both paths
// — but the inconsistency itself is intact, and a reserved character still shows it.
func TestTheRepositoryIdIsEncodedOnThreeCallsAndRawOnTheRest(t *testing.T) {
	t.Run("encoded: a name with a space arrives escaped", func(t *testing.T) {
		host := newFakeAzure(t)
		host.answer(http.MethodGet, "/contoso/Web/_apis/git/repositories/my%20repo/pullRequests/7",
			http.StatusOK, `{"pullRequestId":7}`)

		_, err := host.client("contoso").GetPullRequest(t.Context(), "Web", "my repo", 7)

		require.NoError(t, err)
		assert.Contains(t, host.last().Path, "/repositories/my%20repo/")
	})

	t.Run("raw: a space is escaped anyway, by Go rather than by this code", func(t *testing.T) {
		host := newFakeAzure(t)
		host.answer(http.MethodGet, "/contoso/Web/_apis/git/repositories/my%20repo/pullrequests",
			http.StatusOK, `{"value":[]}`)

		_, err := host.client("contoso").ListPullRequests(t.Context(), "Web", "my repo")

		require.NoError(t, err)
		assert.Contains(t, host.last().Path, "/repositories/my%20repo/",
			"net/url escapes what it can — the space case of BUG-PROV-a is invisible here")
	})

	t.Run("raw: a reserved character still goes wrong", func(t *testing.T) {
		host := newFakeAzure(t)
		host.answer(http.MethodGet, "/contoso/Web/_apis/git/repositories/my", http.StatusOK, `{"value":[]}`)

		_, err := host.client("contoso").ListPullRequests(t.Context(), "Web", "my#repo")

		// The `#` starts a fragment, so the request lands on a path that is not the repository's:
		// the bug, in the shape Go gives it.
		require.NoError(t, err)
		assert.Equal(t, "/contoso/Web/_apis/git/repositories/my", host.last().Path)
	})

	t.Run("encoded: the same reserved character is safe", func(t *testing.T) {
		host := newFakeAzure(t)
		host.answer(http.MethodGet, "/contoso/Web/_apis/git/repositories/my%23repo/pullRequests/7",
			http.StatusOK, `{"pullRequestId":7}`)

		_, err := host.client("contoso").GetPullRequest(t.Context(), "Web", "my#repo", 7)

		require.NoError(t, err)
		assert.Contains(t, host.last().Path, "/repositories/my%23repo/")
	})
}

// ---- error mapping (DIVERGENCE-PROV-b, DIVERGENCE-PROV-c) -------------------------------------

func TestACredentialRefusalIsToldApartFromEveryOtherStatus(t *testing.T) {
	tests := map[int]bool{
		http.StatusUnauthorized: true,
		http.StatusForbidden:    true,
		http.StatusNotFound:     false,
		http.StatusBadRequest:   false,
	}

	for status, unauthorized := range tests {
		t.Run(http.StatusText(status), func(t *testing.T) {
			host := newFakeAzure(t)
			host.answer(http.MethodGet, "/contoso/_apis/projects", status, `{"message":"nope"}`)

			_, err := host.client("contoso").ListProjects(t.Context())

			require.Error(t, err)
			var failure *providers.AzureError
			require.ErrorAs(t, err, &failure)
			assert.Equal(t, unauthorized, failure.Unauthorized)
			assert.Equal(t, status, failure.Status)
			assert.Equal(t, fmt.Sprintf(`Azure DevOps returned %d: {"message":"nope"}`, status), err.Error())
		})
	}
}

// Azure answers an unknown organisation with its **sign-in page** — a whole HTML document. 1.7.2
// interpolated it into the toast; this summarises it, and nothing else.
func TestASignInPageIsSummarisedRatherThanInterpolated(t *testing.T) {
	host := newFakeAzure(t)
	page := `<!DOCTYPE html><html><head><title>Sign In</title></head><body>` +
		strings.Repeat("<div>markup</div>", 500) + `</body></html>`
	host.answer(http.MethodGet, "/contoso/_apis/projects", http.StatusNotFound, page)

	_, err := host.client("contoso").ListProjects(t.Context())

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "<div>")
	assert.Contains(t, err.Error(), "the server answered with a sign-in page instead of the API")
	assert.Contains(t, err.Error(), "Azure DevOps returned 404: ", "the status prefix is untouched")
}

// A JSON error is what actually explains a failure and is never replaced.
func TestAJsonErrorReachesTheUserVerbatim(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodGet, "/contoso/_apis/projects", http.StatusNotFound,
		`{"message":"TF200016: The following project does not exist: Web"}`)

	_, err := host.client("contoso").ListProjects(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "TF200016: The following project does not exist: Web")
}

// ---- pull requests (PROV-023…027) -------------------------------------------------------------

const azurePullBody = `{
	"pullRequestId": 87266,
	"title": "Add the thing",
	"description": "why",
	"status": "active",
	"isDraft": false,
	"sourceRefName": "refs/heads/feat/thing",
	"targetRefName": "refs/heads/main",
	"createdBy": { "id": "user-guid", "displayName": "Gastón Lara P." },
	"creationDate": "2026-08-01T10:00:00Z",
	"reviewers": [{ "id": "user-guid", "vote": 10 }],
	"repository": {
		"id": "repo-guid",
		"name": "Dev.prueba",
		"project": { "id": "project-guid", "name": "Dev" }
	}
}`

func TestListingAzurePullRequestsMapsThemToTheRenderersShape(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/Dev.prueba/pullrequests",
		http.StatusOK, `{"value":[`+azurePullBody+`]}`)

	pulls, err := host.client("contoso").ListPullRequests(t.Context(), "Dev", "Dev.prueba")

	require.NoError(t, err)
	require.Len(t, pulls, 1)
	assert.Equal(t, providers.PullRequestSummary{
		ID:           87266,
		Title:        "Add the thing",
		Description:  "why",
		Status:       providers.StatusOpen,
		SourceBranch: "feat/thing",
		TargetBranch: "main",
		Author:       "Gastón Lara P.",
		CreatedAt:    "2026-08-01T10:00:00Z",
		URL:          "https://dev.azure.com/contoso/Dev/_git/Dev.prueba/pullrequest/87266",
		Provider:     providers.ProviderAzure,
	}, pulls[0])
	assert.Equal(t, "searchCriteria.status=all&api-version=7.1", host.last().Query,
		"every state in one call, and no page size — AMBIGUOUS-PROV-c")
}

// The web address is built, not read: Azure's response carries an API URL, not the page a person
// opens, and the names in it need encoding.
func TestTheAzureWebURLIsSynthesisedFromEncodedNames(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodGet, "/my%20org/Marketing%20Website/_apis/git/repositories/site/pullrequests",
		http.StatusOK, `{"value":[{"pullRequestId":9,"repository":{"name":"my site"}}]}`)

	pulls, err := host.client("my org").ListPullRequests(t.Context(), "Marketing Website", "site")

	require.NoError(t, err)
	require.Len(t, pulls, 1)
	assert.Equal(t, "https://dev.azure.com/my%20org/Marketing%20Website/_git/my%20site/pullrequest/9",
		pulls[0].URL)
}

func TestAzureStatusBucketing(t *testing.T) {
	tests := []struct {
		status   string
		draft    bool
		expected string
	}{
		{status: "active", draft: false, expected: providers.StatusOpen},
		{status: "active", draft: true, expected: providers.StatusDraft},
		{status: "completed", draft: false, expected: providers.StatusMerged},
		{status: "abandoned", draft: false, expected: providers.StatusClosed},
		{status: "completed", draft: true, expected: providers.StatusMerged},
		{status: "abandoned", draft: true, expected: providers.StatusClosed},
	}

	for _, test := range tests {
		t.Run(test.status+fmt.Sprint(test.draft), func(t *testing.T) {
			host := newFakeAzure(t)
			host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/r/pullrequests", http.StatusOK,
				fmt.Sprintf(`{"value":[{"pullRequestId":1,"status":%q,"isDraft":%t}]}`, test.status, test.draft))

			pulls, err := host.client("contoso").ListPullRequests(t.Context(), "Dev", "r")

			require.NoError(t, err)
			require.Len(t, pulls, 1)
			assert.Equal(t, test.expected, pulls[0].Status)
		})
	}
}

// A link that carries GUIDs has no names to match against a local clone's remote. Recovering them
// from the response is what makes "review this link" find the checkout.
func TestReadingOnePullRequestRecoversTheProjectAndRepositoryNames(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodGet, "/contoso/project-guid/_apis/git/repositories/repo-guid/pullRequests/87266",
		http.StatusOK, azurePullBody)

	pull, err := host.client("contoso").GetPullRequest(t.Context(), "project-guid", "repo-guid", 87266)

	require.NoError(t, err)
	assert.Equal(t, "Dev", pull.ProjectName)
	assert.Equal(t, "Dev.prueba", pull.RepoName)
	assert.Equal(t, "https://dev.azure.com/contoso/Dev/_git/Dev.prueba/pullrequest/87266", pull.Summary.URL,
		"the names, not the GUIDs, are what a web address is built from")
}

// With nothing to recover, the arguments stand.
func TestNamesFallBackToWhatTheCallerPassed(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodGet, "/contoso/Web/_apis/git/repositories/api/pullRequests/1",
		http.StatusOK, `{"pullRequestId":1}`)

	pull, err := host.client("contoso").GetPullRequest(t.Context(), "Web", "api", 1)

	require.NoError(t, err)
	assert.Equal(t, "Web", pull.ProjectName)
	assert.Equal(t, "api", pull.RepoName)
}

func TestCreatingAnAzurePullRequestAddsTheRefsHeadsPrefix(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodPost, "/contoso/Dev/_apis/git/repositories/Dev.prueba/pullrequests",
		http.StatusCreated, azurePullBody)

	pull, err := host.client("contoso").CreatePullRequest(t.Context(), "Dev", "Dev.prueba", providers.NewPullRequest{
		Title: "Add the thing", Description: "why", SourceBranch: "feat/thing", TargetBranch: "main",
	})

	require.NoError(t, err)
	assert.Equal(t, int64(87266), pull.ID)
	assert.JSONEq(t, `{
		"sourceRefName": "refs/heads/feat/thing",
		"targetRefName": "refs/heads/main",
		"title": "Add the thing",
		"description": "why",
		"isDraft": false
	}`, host.last().Body)
}

func TestTheLatestIterationFallsBackToOne(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/r/pullRequests/7/iterations",
		http.StatusOK, `{"value":[{"id":1},{"id":2},{"id":7}]}`)

	iteration, err := host.client("contoso").LatestIterationID(t.Context(), "Dev", "r", 7)
	require.NoError(t, err)
	assert.Equal(t, int64(7), iteration, "the last one listed")

	host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/r/pullRequests/8/iterations",
		http.StatusOK, `{"value":[]}`)

	iteration, err = host.client("contoso").LatestIterationID(t.Context(), "Dev", "r", 8)
	require.NoError(t, err)
	assert.Equal(t, int64(1), iteration, "a comment on iteration 1 beats the review failing to post")
}

// ---- the diff (PROV-028) ----------------------------------------------------------------------

func TestTheAzureDiffIsAssembledFromBlobs(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/r/pullRequests/7/iterations/3/changes",
		http.StatusOK, `{"changeEntries":[
			{"changeType":"edit","item":{"path":"/src/app.ts","objectId":"new1","originalObjectId":"old1"}},
			{"changeType":"add","item":{"path":"/nuevo.txt","objectId":"new2","originalObjectId":"0000000000000000000000000000000000000000"}},
			{"changeType":"delete","item":{"path":"/viejo.txt","objectId":"","originalObjectId":"old3"}},
			{"changeType":"edit","item":{"path":"/carpeta","isFolder":true}}
		]}`)
	host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/r/blobs/old1", http.StatusOK, "linea uno\nlinea dos\n")
	host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/r/blobs/new1", http.StatusOK, "linea uno\nlinea DOS\n")
	host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/r/blobs/new2", http.StatusOK, "hola\n")
	host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/r/blobs/old3", http.StatusOK, "adios\n")

	diff, err := host.client("contoso").PullRequestDiff(t.Context(), "Dev", "r", 7, 3)

	require.NoError(t, err)
	assert.Contains(t, diff, "diff --git a/src/app.ts b/src/app.ts", "the leading slash is stripped")
	assert.Contains(t, diff, "-linea dos\n+linea DOS")
	assert.Contains(t, diff, "+hola", "an added file has no old side to fetch")
	assert.Contains(t, diff, "-adios", "a deleted file has no new side")
	assert.NotContains(t, diff, "carpeta", "folders are not changed files")

	// The blob for an added file's all-zero original id is never requested.
	for _, request := range host.seen() {
		assert.NotContains(t, request.Path, "0000000000000000000000000000000000000000")
	}
}

func TestAPullRequestWithNoChangesIsRefused(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/r/pullRequests/7/iterations/3/changes",
		http.StatusOK, `{"changeEntries":[]}`)

	_, err := host.client("contoso").PullRequestDiff(t.Context(), "Dev", "r", 7, 3)

	assert.ErrorIs(t, err, providers.ErrNoFileChanges)
}

// One unreadable file must not fail the review of the other seventy-nine.
func TestAFileThatCannotBeReadSaysSoInPlaceOfItsDiff(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/r/pullRequests/7/iterations/1/changes",
		http.StatusOK, `{"changeEntries":[
			{"changeType":"edit","item":{"path":"/roto.ts","objectId":"new1","originalObjectId":"old1"}},
			{"changeType":"edit","item":{"path":"/bien.ts","objectId":"new2","originalObjectId":"old2"}}
		]}`)
	host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/r/blobs/old1", http.StatusInternalServerError, "boom")
	host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/r/blobs/old2", http.StatusOK, "a\n")
	host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/r/blobs/new2", http.StatusOK, "b\n")

	diff, err := host.client("contoso").PullRequestDiff(t.Context(), "Dev", "r", 7, 1)

	require.NoError(t, err)
	assert.Contains(t, diff, "diff --git a/roto.ts b/roto.ts\n(couldn't read this file from Azure DevOps)\n")
	assert.Contains(t, diff, "-a\n+b", "the other file is still reviewed")
}

func TestBinaryContentIsNamedRatherThanDiffed(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/r/pullRequests/7/iterations/1/changes",
		http.StatusOK, `{"changeEntries":[{"changeType":"edit","item":{"path":"/logo.png","objectId":"n","originalObjectId":"o"}}]}`)
	host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/r/blobs/o", http.StatusOK, "\x89PNG\x00\x01")
	host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/r/blobs/n", http.StatusOK, "\x89PNG\x00\x02")

	diff, err := host.client("contoso").PullRequestDiff(t.Context(), "Dev", "r", 7, 1)

	require.NoError(t, err)
	assert.Contains(t, diff, "diff --git a/logo.png b/logo.png\n(edit, binary)\n")
}

func TestAFileTooLargeToDisplayIsNamed(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/r/pullRequests/7/iterations/1/changes",
		http.StatusOK, `{"changeEntries":[{"changeType":"edit","item":{"path":"/big.txt","objectId":"n","originalObjectId":"o"}}]}`)
	host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/r/blobs/o", http.StatusOK, "small\n")
	host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/r/blobs/n", http.StatusOK,
		strings.Repeat("x", 512*1024+1))

	diff, err := host.client("contoso").PullRequestDiff(t.Context(), "Dev", "r", 7, 1)

	require.NoError(t, err)
	assert.Contains(t, diff, "(edit, too large to display)")
}

// Past eighty files the rest are dropped, and the diff says so at the end rather than silently.
func TestADiffPastEightyFilesIsTruncatedWithANote(t *testing.T) {
	host := newFakeAzure(t)

	entries := make([]string, 0, 100)
	for i := range 100 {
		entries = append(entries, fmt.Sprintf(
			`{"changeType":"edit","item":{"path":"/f%d.txt","objectId":"n%d","originalObjectId":"o%d"}}`, i, i, i))
		host.answer(http.MethodGet, fmt.Sprintf("/contoso/Dev/_apis/git/repositories/r/blobs/o%d", i), http.StatusOK, "a\n")
		host.answer(http.MethodGet, fmt.Sprintf("/contoso/Dev/_apis/git/repositories/r/blobs/n%d", i), http.StatusOK, "b\n")
	}
	host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/r/pullRequests/7/iterations/1/changes",
		http.StatusOK, `{"changeEntries":[`+strings.Join(entries, ",")+`]}`)

	diff, err := host.client("contoso").PullRequestDiff(t.Context(), "Dev", "r", 7, 1)

	require.NoError(t, err)
	assert.Contains(t, diff, "(only the first 80 of 100 changed files are included)\n")
	assert.Contains(t, diff, "a/f79.txt")
	assert.NotContains(t, diff, "a/f80.txt")
	// Order is the order Azure listed them in, whatever order the fetches finished in.
	assert.Less(t, strings.Index(diff, "a/f1.txt"), strings.Index(diff, "a/f2.txt"))
}

// ---- comments (PROV-030…033) ------------------------------------------------------------------

func TestAnAnchoredAzureThreadCarriesItsPositionAndIteration(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/r/pullRequests/7/iterations",
		http.StatusOK, `{"value":[{"id":1},{"id":4}]}`)
	host.answer(http.MethodPost, "/contoso/Dev/_apis/git/repositories/r/pullRequests/7/threads",
		http.StatusCreated, `{"id":555}`)

	id, err := host.client("contoso").PostAnchoredComment(t.Context(), "Dev", "r", 7,
		"the finding", "src/app.ts", 12, 14)

	require.NoError(t, err)
	assert.Equal(t, int64(555), id)
	assert.JSONEq(t, `{
		"comments": [{ "parentCommentId": 0, "content": "the finding", "commentType": 1 }],
		"status": 1,
		"threadContext": {
			"filePath": "/src/app.ts",
			"rightFileStart": { "line": 12, "offset": 1 },
			"rightFileEnd": { "line": 14, "offset": 1 }
		},
		"pullRequestThreadContext": {
			"iterationContext": { "firstComparingIteration": 1, "secondComparingIteration": 4 }
		}
	}`, host.last().Body)
}

// Azure wants the leading slash — the opposite of GitHub, which strips it. Each side normalises
// toward its own API, and neither is the other's inconsistency.
func TestTheAzureFilePathKeepsOrGainsItsLeadingSlash(t *testing.T) {
	for _, given := range []string{"src/app.ts", "/src/app.ts"} {
		t.Run(given, func(t *testing.T) {
			host := newFakeAzure(t)
			host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/r/pullRequests/7/iterations",
				http.StatusOK, `{"value":[{"id":1}]}`)
			host.answer(http.MethodPost, "/contoso/Dev/_apis/git/repositories/r/pullRequests/7/threads",
				http.StatusCreated, `{"id":1}`)

			_, err := host.client("contoso").PostAnchoredComment(t.Context(), "Dev", "r", 7, "f", given, 1, 1)

			require.NoError(t, err)
			assert.Contains(t, host.last().Body, `"filePath":"/src/app.ts"`)
		})
	}
}

func TestAnInvertedAzureRangeIsPutBackInOrder(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/r/pullRequests/7/iterations",
		http.StatusOK, `{"value":[{"id":1}]}`)
	host.answer(http.MethodPost, "/contoso/Dev/_apis/git/repositories/r/pullRequests/7/threads",
		http.StatusCreated, `{"id":1}`)

	_, err := host.client("contoso").PostAnchoredComment(t.Context(), "Dev", "r", 7, "f", "a.ts", 20, 12)

	require.NoError(t, err)
	assert.Contains(t, host.last().Body, `"rightFileStart":{"line":20,"offset":1}`)
	assert.Contains(t, host.last().Body, `"rightFileEnd":{"line":20,"offset":1}`)
}

func TestAPullRequestLevelThreadCarriesNoContext(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodPost, "/contoso/Dev/_apis/git/repositories/r/pullRequests/7/threads",
		http.StatusCreated, `{"id":600}`)

	id, err := host.client("contoso").PostComment(t.Context(), "Dev", "r", 7, "the summary")

	require.NoError(t, err)
	assert.Equal(t, int64(600), id)
	assert.JSONEq(t, `{
		"comments": [{ "parentCommentId": 0, "content": "the summary", "commentType": 1 }],
		"status": 1
	}`, host.last().Body)
	assert.NotContains(t, host.last().Body, "threadContext")
}

// The parent is hardcoded to 1, not read from the thread: every thread this app creates starts with
// exactly one comment, and Azure numbers a thread's comments from 1.
func TestAReplyAddressesCommentOne(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodPost, "/contoso/Dev/_apis/git/repositories/r/pullRequests/7/threads/555/comments",
		http.StatusCreated, `{"id":2}`)

	err := host.client("contoso").ReplyToThread(t.Context(), "Dev", "r", 7, 555, "still here")

	require.NoError(t, err)
	assert.JSONEq(t, `{"parentCommentId":1,"content":"still here","commentType":1}`, host.last().Body)
}

func TestMarkingAThreadFixed(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodPatch, "/contoso/Dev/_apis/git/repositories/r/pullRequests/7/threads/555",
		http.StatusOK, `{}`)

	err := host.client("contoso").SetThreadStatus(t.Context(), "Dev", "r", 7, 555, providers.AzureThreadFixed)

	require.NoError(t, err)
	assert.Equal(t, http.MethodPatch, host.last().Method)
	assert.JSONEq(t, `{"status":2}`, host.last().Body)
}

// ---- votes and decisions (PROV-034, PROV-035, PROV-036) ---------------------------------------

func TestTheConnectionDataCallIsTheOnePreviewVersion(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodGet, "/contoso/_apis/connectionData", http.StatusOK,
		`{"authenticatedUser":{"id":"user-guid"}}`)

	id, err := host.client("contoso").AuthenticatedUserID(t.Context())

	require.NoError(t, err)
	assert.Equal(t, "user-guid", id)
	assert.Equal(t, "api-version=7.1-preview", host.last().Query,
		"this endpoint never went GA and rejects a plain 7.1")
}

func TestVotingAddsTheCallerAsAReviewerInOneCall(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodGet, "/contoso/_apis/connectionData", http.StatusOK,
		`{"authenticatedUser":{"id":"user-guid"}}`)
	host.answer(http.MethodPut, "/contoso/Dev/_apis/git/repositories/r/pullRequests/7/reviewers/user-guid",
		http.StatusOK, `{}`)

	err := host.client("contoso").SetReviewerVote(t.Context(), "Dev", "r", 7, providers.AzureVoteApproved)

	require.NoError(t, err)
	assert.Equal(t, http.MethodPut, host.last().Method)
	assert.JSONEq(t, `{"vote":10}`, host.last().Body)
}

func TestTheAzureViewerDecisionCollapsesFiveVotesIntoThree(t *testing.T) {
	tests := []struct {
		vote     int
		expected string
	}{
		{vote: 10, expected: providers.DecisionApproved},
		{vote: 5, expected: providers.DecisionApproved},
		{vote: 0, expected: providers.DecisionNone},
		{vote: -5, expected: providers.DecisionChangesRequested},
		{vote: -10, expected: providers.DecisionChangesRequested},
	}

	for _, test := range tests {
		t.Run(fmt.Sprint(test.vote), func(t *testing.T) {
			host := newFakeAzure(t)
			host.answer(http.MethodGet, "/contoso/_apis/connectionData", http.StatusOK,
				`{"authenticatedUser":{"id":"USER-GUID"}}`)
			host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/r/pullRequests/7",
				http.StatusOK, fmt.Sprintf(`{"pullRequestId":7,"reviewers":[{"id":"user-guid","vote":%d}]}`, test.vote))

			decision, err := host.client("contoso").ViewerDecision(t.Context(), "Dev", "r", 7)

			require.NoError(t, err)
			assert.Equal(t, test.expected, decision, "the id match is case-insensitive")
		})
	}
}

func TestSomeoneElsesVoteIsNotTheViewers(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodGet, "/contoso/_apis/connectionData", http.StatusOK,
		`{"authenticatedUser":{"id":"mine"}}`)
	host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/r/pullRequests/7",
		http.StatusOK, `{"pullRequestId":7,"reviewers":[{"id":"someone-else","vote":10}]}`)

	decision, err := host.client("contoso").ViewerDecision(t.Context(), "Dev", "r", 7)

	require.NoError(t, err)
	assert.Equal(t, providers.DecisionNone, decision)
}

func TestAbandoningIsAzuresClose(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodPatch, "/contoso/Dev/_apis/git/repositories/r/pullRequests/7", http.StatusOK, `{}`)

	err := host.client("contoso").AbandonPullRequest(t.Context(), "Dev", "r", 7)

	require.NoError(t, err)
	assert.JSONEq(t, `{"status":"abandoned"}`, host.last().Body)
}

// ---- comment threads (PROV-037) ---------------------------------------------------------------

func TestClosedThreadsAndSystemCommentsAreDropped(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/r/pullRequests/7/threads", http.StatusOK, `{"value":[
		{
			"id": 1, "status": "active",
			"comments": [
				{"content":"the finding","commentType":"text","publishedDate":"t1","author":{"displayName":"Gastón"}},
				{"content":"voted 10","commentType":"system","publishedDate":"t2","author":{"displayName":"Azure"}},
				{"content":"   ","commentType":"text","publishedDate":"t3","author":{"displayName":"x"}}
			],
			"threadContext": {
				"filePath": "/src/app.ts",
				"rightFileStart": { "line": 10 },
				"rightFileEnd": { "line": 12 }
			}
		},
		{ "id": 2, "status": "fixed", "comments": [{"content":"resuelto","commentType":"text"}] },
		{ "id": 3, "status": "pending", "comments": [{"content":"pendiente","publishedDate":"t4","author":{"displayName":"y"}}] },
		{ "id": 4, "comments": [{"content":"sin estado","commentType":"text","publishedDate":"t5","author":{"displayName":"z"}}] },
		{ "id": 5, "status": "active", "comments": [{"content":"voted","commentType":"system"}] }
	]}`)

	threads, err := host.client("contoso").ListCommentThreads(t.Context(), "Dev", "r", 7)

	require.NoError(t, err)
	require.Len(t, threads, 3, "fixed is done with, and a thread with only system comments is empty")

	assert.Equal(t, int64(1), threads[0].ID)
	require.Len(t, threads[0].Comments, 1, "the system comment and the blank one are dropped")
	assert.Equal(t, "Gastón", threads[0].Comments[0].Author)
	assert.Equal(t, "t1", threads[0].Comments[0].PublishedDate)
	require.NotNil(t, threads[0].FilePath)
	assert.Equal(t, "/src/app.ts", *threads[0].FilePath, "carried through as Azure wrote it")
	require.NotNil(t, threads[0].StartLine)
	assert.Equal(t, int64(10), *threads[0].StartLine)

	assert.Equal(t, int64(3), threads[1].ID, "pending is still open")
	assert.Equal(t, "pendiente", threads[1].Comments[0].Content, "an absent commentType is a text comment")

	assert.Equal(t, int64(4), threads[2].ID, "an absent status is open too")
	assert.Nil(t, threads[2].FilePath, "no threadContext means no location")
}

func TestAnAzurePullRequestWithNoThreadsAnswersAnArray(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/r/pullRequests/7/threads",
		http.StatusOK, `{"value":[]}`)

	threads, err := host.client("contoso").ListCommentThreads(t.Context(), "Dev", "r", 7)

	require.NoError(t, err)
	encoded, err := json.Marshal(threads)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(encoded))
}

// The PAT must not reach anything but the Authorization header (SEC-007).
func TestThePatNeverTravelsInABody(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodPost, "/contoso/Dev/_apis/git/repositories/r/pullRequests/7/threads",
		http.StatusCreated, `{"id":1}`)

	_, err := host.client("contoso").PostComment(t.Context(), "Dev", "r", 7, "a comment")
	require.NoError(t, err)

	for _, request := range host.seen() {
		assert.NotContains(t, request.Body, "the-pat")
		assert.NotContains(t, request.Path, "the-pat")
		assert.NotContains(t, request.Query, "the-pat")
		assert.Contains(t, request.Header.Get("Authorization"), "Basic ")
	}
}
