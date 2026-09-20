package providers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// The Azure DevOps REST client (PROV-019…037).
//
// Two things here are not tidiness and must not be tidied:
//
//   - `repo_id` is percent-encoded on three calls and sent raw on the rest (`BUG-PROV-a`). That is
//     preserved call site by call site, each raw one marked, because a repository whose name needs
//     encoding already behaves this way for every existing install.
//   - the organisation is normalised before it is encoded (`PROV-020`), because Azure's server
//     rejects a literal `:` anywhere in the path and a user who saved a whole URL as their
//     "organisation" would otherwise get a 400 that says nothing.

const (
	// azureAPIVersion is appended to every call…
	azureAPIVersion = "7.1"
	// …except connectionData, which never went GA: the server rejects a plain 7.1 on it with a 400
	// demanding the suffix.
	azurePreviewAPIVersion = "7.1-preview"

	// azureMaxDiffFiles is how many changed files a diff carries before it is truncated with a
	// note. A review past this many files is not a review a person reads anyway.
	azureMaxDiffFiles = 80
	// azureMaxBlobBytes is the per-side size past which a file renders as a note instead of a
	// diff. The blob **is** fetched first — the check happens after, as in 2.x.
	azureMaxBlobBytes = 512 * 1024
	// azureDiffConcurrency is how many files are rendered at once. Their order is preserved.
	azureDiffConcurrency = 6

	// azureNullObjectID is Azure's "there is no blob on this side": forty zeros.
	azureNullObjectID = "0000000000000000000000000000000000000000"
)

// The six thread statuses, `VERBATIM` from the API. A resolved finding's thread is set to fixed.
const (
	AzureThreadActive   = 1
	AzureThreadFixed    = 2
	AzureThreadWontFix  = 3
	AzureThreadClosed   = 4
	AzureThreadByDesign = 5
	AzureThreadPending  = 6
)

// The five reviewer votes. `viewer_decision` collapses them into three buckets, and nothing
// translates them into GitHub's review events (`DIVERGENCE-PROV-a`).
const (
	AzureVoteApproved            = 10
	AzureVoteApprovedWithSuggest = 5
	AzureVoteNone                = 0
	AzureVoteWaitingForAuthor    = -5
	AzureVoteRejected            = -10
)

// AzureError is what a failed Azure DevOps call answers with.
type AzureError struct {
	// Status is 0 for a transport failure.
	Status int
	Body   string
	// Unauthorized marks a 401 or a 403 — the one classification this client makes
	// (`DIVERGENCE-PROV-b`). The commands turn it into the `CREDENTIAL_REFUSED: ` sentinel; a 404
	// is still an undifferentiated 404.
	Unauthorized bool
	Err          error
}

func (e *AzureError) Error() string {
	switch {
	case e.Status != 0:
		return fmt.Sprintf("Azure DevOps returned %d: %s", e.Status, e.Body)
	case errors.Is(e.Err, ErrAzureDecode):
		return e.Err.Error()
	case e.Err != nil:
		return fmt.Sprintf("couldn't reach Azure DevOps: %v", e.Err)
	default:
		return "Azure DevOps returned an error with no detail"
	}
}

func (e *AzureError) Unwrap() error { return e.Err }

// ErrAzureDecode marks a 2xx whose body was not the shape the call expected.
var ErrAzureDecode = errors.New("unexpected response from Azure DevOps")

// azureSignInPage is what a non-2xx HTML body is replaced with (`DIVERGENCE-PROV-c`).
//
// Azure answers an unknown organisation — and an unauthenticated request — with its sign-in
// **page**: a whole HTML document with a base64 logo. 1.7.2 interpolated it whole, which put tens of
// kilobytes of markup where an error toast goes and made a mistyped organisation and an expired
// token read identically.
const azureSignInPage = "the server answered with a sign-in page instead of the API. " +
	"Check that the organisation name is right and that its token has not expired."

// readable replaces an HTML error body with a sentence. Deliberately narrow: a JSON error from the
// API is what actually explains a failure, and `TF200016: project does not exist` still reaches the
// user verbatim.
func readable(body string) string {
	trimmed := strings.TrimSpace(body)
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "<!doctype") || strings.HasPrefix(lower, "<html") {
		return azureSignInPage
	}
	return body
}

// AzureClient talks to one organisation with one PAT.
type AzureClient struct {
	client *http.Client
	// org is already normalised: every call encodes it, none re-derives it.
	org string
	pat string
}

// NewAzureClient binds a client to an organisation and its PAT.
func NewAzureClient(client *http.Client, org, pat string) AzureClient {
	if client == nil {
		client = http.DefaultClient
	}
	return AzureClient{client: client, org: NormalizeAzureOrg(org), pat: pat}
}

// Org is the normalised organisation this client talks to.
func (c AzureClient) Org() string { return c.org }

// NormalizeAzureOrg reduces whatever the user saved as "organisation" to the bare name (PROV-020).
//
// Three forms are recognised — a bare name, `https://dev.azure.com/{org}`, and the legacy
// `https://{org}.visualstudio.com` — and anything else is assumed to be a bare name already and
// only trimmed. The reason this exists at all: Azure's server rejects a literal `:` in the request
// path, so a whole URL interpolated into a path segment fails with a 400 that explains nothing.
func NormalizeAzureOrg(org string) string {
	value := strings.TrimSpace(org)
	if value == "" {
		return ""
	}

	for _, prefix := range []string{"https://dev.azure.com/", "http://dev.azure.com/"} {
		if after, found := strings.CutPrefix(value, prefix); found {
			segment, _, _ := strings.Cut(strings.TrimPrefix(after, "/"), "/")
			return segment
		}
	}

	rest, hadScheme := cutScheme(value)
	if !hadScheme {
		return value
	}
	host, _, _ := strings.Cut(rest, "/")
	if strings.HasSuffix(strings.ToLower(host), azureLegacySuffix) {
		return host[:len(host)-len(azureLegacySuffix)]
	}
	return value
}

// encodeSegment percent-encodes one path segment (PROV-021).
//
// Byte by byte: `A-Za-z0-9-._~` pass through and everything else becomes `%XX`. Applied to the
// organisation and the project at every call site — and to `repo_id` at only three of them, which
// is `BUG-PROV-a`.
func encodeSegment(segment string) string {
	out := &strings.Builder{}
	for i := 0; i < len(segment); i++ {
		b := segment[i]
		switch {
		case b >= 'A' && b <= 'Z', b >= 'a' && b <= 'z', b >= '0' && b <= '9',
			b == '-', b == '.', b == '_', b == '~':
			out.WriteByte(b)
		default:
			fmt.Fprintf(out, "%%%02X", b)
		}
	}
	return out.String()
}

// orgRoot is `https://dev.azure.com/{org}`, the root of every call.
func (c AzureClient) orgRoot() string {
	return "https://dev.azure.com/" + encodeSegment(c.org)
}

// projectRoot is the organisation plus the project, both encoded.
func (c AzureClient) projectRoot(project string) string {
	return c.orgRoot() + "/" + encodeSegment(project)
}

// repoRoot is the git-repository root. `repoID` is interpolated **raw**, which is `BUG-PROV-a` for
// every caller that uses it; the three call sites that encode it build their own URL with
// encodedRepoRoot below.
func (c AzureClient) repoRoot(project, repoID string) string {
	return c.projectRoot(project) + "/_apis/git/repositories/" + repoID
}

// encodedRepoRoot is the same root with `repo_id` percent-encoded — the minority behaviour that
// `BUG-PROV-a` describes, kept for exactly the three calls that had it.
func (c AzureClient) encodedRepoRoot(project, repoID string) string {
	return c.projectRoot(project) + "/_apis/git/repositories/" + encodeSegment(repoID)
}

// withVersion appends the api-version, joining with `&` when the URL already has a query.
func withVersion(url, version string) string {
	if strings.Contains(url, "?") {
		return url + "&api-version=" + version
	}
	return url + "?api-version=" + version
}

// ---- the transport ------------------------------------------------------------------------------

func (c AzureClient) get(ctx context.Context, url string, into any) error {
	return c.do(ctx, http.MethodGet, url, nil, into)
}

func (c AzureClient) post(ctx context.Context, url string, body, into any) error {
	return c.do(ctx, http.MethodPost, url, body, into)
}

func (c AzureClient) patch(ctx context.Context, url string, body any) error {
	return c.do(ctx, http.MethodPatch, url, body, nil)
}

func (c AzureClient) put(ctx context.Context, url string, body any) error {
	return c.do(ctx, http.MethodPut, url, body, nil)
}

// do runs one request. into may be nil for a call whose response is not read.
func (c AzureClient) do(ctx context.Context, method, url string, body, into any) error {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode the request for %s: %w", url, err)
		}
		payload = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(ctx, method, url, payload)
	if err != nil {
		return fmt.Errorf("build the request for %s: %w", url, err)
	}
	c.authorize(request)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := c.client.Do(request)
	if err != nil {
		return &AzureError{Err: err}
	}
	defer func() { _ = response.Body.Close() }()

	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return &AzureError{Err: err}
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return newAzureStatusError(response.StatusCode, string(raw))
	}
	if into == nil {
		return nil
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return &AzureError{Err: fmt.Errorf("%w: %w", ErrAzureDecode, err)}
	}
	return nil
}

// authorize sets the one header every call carries: Basic with an empty username and the PAT as
// the password. The PAT reaches nothing else — not a body, not a diff, not a comment (SEC-007).
func (c AzureClient) authorize(request *http.Request) {
	request.Header.Set("Authorization",
		"Basic "+base64.StdEncoding.EncodeToString([]byte(":"+c.pat)))
}

// newAzureStatusError classifies the two statuses that mean "your credential", and nothing else.
func newAzureStatusError(status int, body string) *AzureError {
	return &AzureError{
		Status: status,
		Body:   readable(body),
		// `DIVERGENCE-PROV-b`: 401 and 403 are the states a user can act on — an expired PAT is
		// one every organisation's policy eventually forces — and the sidebar offers Settings
		// instead of a retry that would fail identically. A 404 is still a 404.
		Unauthorized: status == http.StatusUnauthorized || status == http.StatusForbidden,
	}
}
