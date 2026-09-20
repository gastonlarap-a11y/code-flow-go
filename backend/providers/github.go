package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// The GitHub REST client (PROV-001…018).
//
// Every call takes the repository explicitly and the token comes from the caller: there is no
// ambient credential here, and the token is used in exactly one place — the `Authorization` header.
// It never reaches a request body, a diff, a comment or a log (SEC-007).

const (
	// githubAPIVersion pins behaviour to a dated snapshot instead of "latest".
	githubAPIVersion = "2022-11-28"
	// githubUserAgent is required: GitHub answers 403 to a request with no User-Agent.
	githubUserAgent = "CodeFlow"
	// githubAccept is the REST media type. The diff read replaces it (PROV-008).
	githubAccept = "application/vnd.github+json"
	// githubDiffAccept asks GitHub for the literal diff it would show.
	githubDiffAccept = "application/vnd.github.diff"

	// githubMaxPerPage is the page size every list endpoint asks for, and — with one exception —
	// the cap: there is no shared pager, and the endpoints that stop at 100 lose the rest in
	// silence. Preserved: paging them now would change which pull requests a user sees.
	githubMaxPerPage = 100
	// githubDiffFilePages is GitHub's own hard ceiling for the files endpoint: past 300 files it
	// truncates by itself.
	githubDiffFilePages = 3

	// githubSelfApprovalPhrase is **GitHub's** wording, missing space included, and is matched
	// case-insensitively (`XLANG-013`, `DIVERGENCE-PROV-c`). `422` is GitHub's answer to every
	// validation failure — an empty REQUEST_CHANGES body included — so the status alone cannot
	// identify a self-approval and the sentence has to be matched too. If GitHub rewords it this
	// degrades to the raw 422 every other validation failure already shows.
	githubSelfApprovalPhrase = "Can not approve your own pull request"
)

// GitHubError is what a failed GitHub call answers with.
//
// A typed error rather than a formatted string because the boundary has to branch on one case —
// the self-approval 422 — without matching on text. Everything else is deliberately
// undifferentiated: a 401, a 403, a 404 and any other 422 all read alike, as they did in 2.x.
type GitHubError struct {
	// Status is 0 for a transport failure, which is the one case that never reached the far side.
	Status int
	Body   string
	// SelfApproval marks the 422 GitHub returns for approving one's own pull request. The
	// `act_on_*` commands turn it into the `SELF_APPROVAL: ` sentinel; nothing else may.
	SelfApproval bool
	Err          error
}

// Error renders the three shapes 2.x produced, and the three are told apart by what actually
// happened rather than by whether a field is set: a status means the far side answered, a decode
// failure means it answered something else, and anything left never arrived.
func (e *GitHubError) Error() string {
	switch {
	case e.Status != 0:
		return fmt.Sprintf("GitHub returned %d: %s", e.Status, e.Body)
	case errors.Is(e.Err, ErrGitHubDecode):
		// ErrGitHubDecode carries the sentence; wrapping it again would print it twice.
		return e.Err.Error()
	case e.Err != nil:
		return fmt.Sprintf("couldn't reach GitHub: %v", e.Err)
	default:
		return "GitHub returned an error with no detail"
	}
}

func (e *GitHubError) Unwrap() error { return e.Err }

// ErrGitHubDecode marks a 2xx whose body was not the shape the call expected.
var ErrGitHubDecode = errors.New("unexpected response from GitHub")

// GitHubClient talks to one host with one token.
type GitHubClient struct {
	client *http.Client
	host   string
	token  string
}

// NewGitHubClient binds a client to a host and a token.
//
// The host is the one the project is linked to: `github.com` or an Enterprise server the user has
// connected. A nil http.Client falls back to the default one so a test does not have to build a
// transport to exercise a parser.
func NewGitHubClient(client *http.Client, host, token string) GitHubClient {
	if client == nil {
		client = http.DefaultClient
	}
	return GitHubClient{client: client, host: host, token: token}
}

// apiRoot is the REST root for this host (PROV-001).
//
// Any host that is not github.com is treated as a GitHub Enterprise Server, with no check that it
// is reachable or even GitHub — that is what the known-hosts allowlist is for, one layer up.
func (c GitHubClient) apiRoot() string {
	if strings.EqualFold(c.host, DefaultGitHubHost) {
		return "https://api.github.com"
	}
	return "https://" + c.host + "/api/v3"
}

// graphqlRoot is the GraphQL endpoint for this host (PROV-001).
func (c GitHubClient) graphqlRoot() string {
	if strings.EqualFold(c.host, DefaultGitHubHost) {
		return "https://api.github.com/graphql"
	}
	return "https://" + c.host + "/api/graphql"
}

func (c GitHubClient) repoRoot(owner, repo string) string {
	return fmt.Sprintf("%s/repos/%s/%s", c.apiRoot(), owner, repo)
}

// ---- the four verbs -----------------------------------------------------------------------------
//
// One place per verb, each attaching the same four headers and doing the same error mapping, so a
// new endpoint cannot forget one of them.

func (c GitHubClient) get(ctx context.Context, url string, into any) error {
	return c.do(ctx, http.MethodGet, url, nil, into)
}

func (c GitHubClient) post(ctx context.Context, url string, body, into any) error {
	return c.do(ctx, http.MethodPost, url, body, into)
}

func (c GitHubClient) patch(ctx context.Context, url string, body any) error {
	return c.do(ctx, http.MethodPatch, url, body, nil)
}

// do runs one request. into may be nil, for a call whose response is not read.
func (c GitHubClient) do(ctx context.Context, method, url string, body, into any) error {
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
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", githubAccept)
	request.Header.Set("User-Agent", githubUserAgent)
	request.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := c.client.Do(request)
	if err != nil {
		return &GitHubError{Err: err}
	}
	defer func() { _ = response.Body.Close() }()

	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return &GitHubError{Err: err}
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return newGitHubStatusError(response.StatusCode, string(raw))
	}
	if into == nil {
		return nil
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return &GitHubError{Err: fmt.Errorf("%w: %w", ErrGitHubDecode, err)}
	}
	return nil
}

// newGitHubStatusError classifies the one status GitHub answers that the port tells apart.
func newGitHubStatusError(status int, body string) *GitHubError {
	failure := &GitHubError{Status: status, Body: body}
	if status == http.StatusUnprocessableEntity &&
		strings.Contains(strings.ToLower(body), strings.ToLower(githubSelfApprovalPhrase)) {
		// Matched over the whole body rather than over a parsed `errors[]`: GitHub sends that array
		// as strings in some responses and as objects in others, and either way the sentence is
		// what identifies the case.
		failure.SelfApproval = true
	}
	return failure
}

// ---- what the client answers --------------------------------------------------------------------

// githubAuthor is the one field this application reads of any GitHub account: every response that
// names a person names them the same way.
type githubAuthor struct {
	Login string `json:"login"`
}

// AuthenticatedUser is the login the token belongs to (PROV-005).
//
// Used to validate a pasted token and to show whose account it is, and by ViewerDecision to know
// which reviews are the viewer's own.
func (c GitHubClient) AuthenticatedUser(ctx context.Context) (string, error) {
	var user githubAuthor
	if err := c.get(ctx, c.apiRoot()+"/user", &user); err != nil {
		return "", err
	}
	return user.Login, nil
}

// rawPull is the part of GitHub's pull request this application reads.
type rawPull struct {
	Number   int64   `json:"number"`
	Title    string  `json:"title"`
	Body     *string `json:"body"`
	State    string  `json:"state"`
	Draft    bool    `json:"draft"`
	MergedAt *string `json:"merged_at"`
	Head     struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
	User      githubAuthor `json:"user"`
	CreatedAt string       `json:"created_at"`
	HTMLURL   string       `json:"html_url"`
}

// mapPull turns GitHub's shape into the one the renderer reads.
func mapPull(raw rawPull) PullRequestSummary {
	description := ""
	if raw.Body != nil {
		description = *raw.Body
	}
	return PullRequestSummary{
		ID:           raw.Number,
		Title:        raw.Title,
		Description:  description,
		Status:       bucketGitHubStatus(raw.State, raw.Draft, raw.MergedAt),
		SourceBranch: raw.Head.Ref,
		TargetBranch: raw.Base.Ref,
		Author:       raw.User.Login,
		CreatedAt:    raw.CreatedAt,
		URL:          raw.HTMLURL,
		Provider:     ProviderGitHub,
	}
}

// bucketGitHubStatus collapses GitHub's vocabulary into the four buckets (PROV-006).
//
// The merge check runs **first**: a merged pull request is also `state == "closed"`, and reporting
// it as closed would file every merged PR under the wrong heading.
func bucketGitHubStatus(state string, draft bool, mergedAt *string) string {
	switch {
	// Present, not non-empty: 2.x asked whether the field was there at all, and GitHub sends
	// either a timestamp or null. Narrowing it here would be this port inventing a third case.
	case mergedAt != nil:
		return StatusMerged
	case state == "closed":
		return StatusClosed
	case draft:
		return StatusDraft
	default:
		return StatusOpen
	}
}

// ListPullRequests reads a repository's pull requests, newest first (PROV-007).
//
// Capped at one page of 100 with no further paging, as in 2.x: a repository with more than 100
// all-time pull requests loses whichever fall outside the newest 100 by creation date. Preserved
// deliberately — paging here would change which pull requests appear in the sidebar.
func (c GitHubClient) ListPullRequests(ctx context.Context, owner, repo string) ([]PullRequestSummary, error) {
	url := fmt.Sprintf("%s/pulls?state=all&per_page=%d&sort=created&direction=desc",
		c.repoRoot(owner, repo), githubMaxPerPage)

	var raws []rawPull
	if err := c.get(ctx, url, &raws); err != nil {
		return nil, err
	}

	pulls := make([]PullRequestSummary, 0, len(raws))
	for _, raw := range raws {
		pulls = append(pulls, mapPull(raw))
	}
	return pulls, nil
}

// GetPullRequest reads one pull request, however far back in the list it is (PROV-007).
func (c GitHubClient) GetPullRequest(ctx context.Context, owner, repo string, number int64) (PullRequestSummary, error) {
	raw, err := c.rawPullRequest(ctx, owner, repo, number)
	if err != nil {
		return PullRequestSummary{}, err
	}
	return mapPull(raw), nil
}

func (c GitHubClient) rawPullRequest(ctx context.Context, owner, repo string, number int64) (rawPull, error) {
	var raw rawPull
	url := fmt.Sprintf("%s/pulls/%d", c.repoRoot(owner, repo), number)
	if err := c.get(ctx, url, &raw); err != nil {
		return rawPull{}, err
	}
	return raw, nil
}

// ErrNoHeadCommit is refused rather than answering an empty SHA, which would anchor comments to
// nothing at all.
var ErrNoHeadCommit = errors.New("GitHub didn't report a head commit for this pull request") //nolint:staticcheck // ST1005: VERBATIM

// HeadSHA is the pull request's current head commit (PROV-010).
//
// Read fresh immediately before posting anchored comments, so a re-review anchors to the tip rather
// than to the commit the review was computed against.
func (c GitHubClient) HeadSHA(ctx context.Context, owner, repo string, number int64) (string, error) {
	raw, err := c.rawPullRequest(ctx, owner, repo, number)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(raw.Head.SHA) == "" {
		return "", ErrNoHeadCommit
	}
	return raw.Head.SHA, nil
}

// ErrNoChangedFiles is what a pull request with nothing in it answers.
var ErrNoChangedFiles = errors.New("GitHub reported no changed files for this pull request") //nolint:staticcheck // ST1005: VERBATIM

// PullRequestDiff is the pull request's unified diff (PROV-008).
//
// One request with GitHub's diff media type returns the literal diff GitHub itself would show. The
// per-file reassembly below is the fallback for when GitHub declines to render it whole, which it
// signals with a non-2xx **or** with a 2xx and an empty body — the second is GitHub's answer past
// its own internal size limit, and treating it as success would review an empty diff.
func (c GitHubClient) PullRequestDiff(ctx context.Context, owner, repo string, number int64) (string, error) {
	diff, err := c.rawDiff(ctx, owner, repo, number)
	if err == nil && strings.TrimSpace(diff) != "" {
		return diff, nil
	}
	return c.diffFromFiles(ctx, owner, repo, number)
}

// rawDiff asks for the diff media type. Its own failure is never reported: the caller falls back.
func (c GitHubClient) rawDiff(ctx context.Context, owner, repo string, number int64) (string, error) {
	url := fmt.Sprintf("%s/pulls/%d", c.repoRoot(owner, repo), number)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build the diff request for %s: %w", url, err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", githubDiffAccept)
	request.Header.Set("User-Agent", githubUserAgent)
	request.Header.Set("X-GitHub-Api-Version", githubAPIVersion)

	response, err := c.client.Do(request)
	if err != nil {
		return "", &GitHubError{Err: err}
	}
	defer func() { _ = response.Body.Close() }()

	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return "", &GitHubError{Err: err}
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return "", newGitHubStatusError(response.StatusCode, string(raw))
	}
	return string(raw), nil
}

type rawPullFile struct {
	Filename         string  `json:"filename"`
	PreviousFilename *string `json:"previous_filename"`
	Status           string  `json:"status"`
	Patch            *string `json:"patch"`
}

// diffFromFiles reassembles a unified diff from the per-file patches (PROV-008).
//
// Three pages of 100, which is GitHub's own ceiling for this endpoint — past 300 files it truncates
// regardless. A file whose `patch` is absent (binary, or a diff GitHub judged too large to inline)
// contributes its header and a line saying so, rather than being dropped: a reviewer has to know
// the file changed even when its content cannot be shown.
func (c GitHubClient) diffFromFiles(ctx context.Context, owner, repo string, number int64) (string, error) {
	diff := &strings.Builder{}
	seen := 0

	for page := 1; page <= githubDiffFilePages; page++ {
		url := fmt.Sprintf("%s/pulls/%d/files?per_page=%d&page=%d",
			c.repoRoot(owner, repo), number, githubMaxPerPage, page)

		var files []rawPullFile
		if err := c.get(ctx, url, &files); err != nil {
			return "", err
		}
		if len(files) == 0 {
			break
		}
		seen += len(files)

		for _, file := range files {
			writeGitHubFileDiff(diff, file)
		}
		if len(files) < githubMaxPerPage {
			break
		}
	}

	if seen == 0 {
		return "", ErrNoChangedFiles
	}
	return diff.String(), nil
}

func writeGitHubFileDiff(out *strings.Builder, file rawPullFile) {
	previous := file.Filename
	if file.PreviousFilename != nil && *file.PreviousFilename != "" {
		previous = *file.PreviousFilename
	}

	oldPath := "a/" + previous
	if file.Status == "added" {
		oldPath = "/dev/null"
	}
	newPath := "b/" + file.Filename
	if file.Status == "removed" {
		newPath = "/dev/null"
	}

	fmt.Fprintf(out, "diff --git a/%s b/%s\n--- %s\n+++ %s\n", previous, file.Filename, oldPath, newPath)

	if file.Patch == nil || *file.Patch == "" {
		out.WriteString("(binary or too large to display)\n")
		return
	}
	out.WriteString(*file.Patch)
	if !strings.HasSuffix(*file.Patch, "\n") {
		out.WriteString("\n")
	}
}

// CreatePullRequest opens one (PROV-009).
//
// `head` and `base` are branch names rather than full refs, and the branch has to exist on the
// remote already — GitHub refuses a pull request from a branch it cannot see.
func (c GitHubClient) CreatePullRequest(ctx context.Context, owner, repo string, pr NewPullRequest) (PullRequestSummary, error) {
	body := map[string]any{
		"title": pr.Title,
		"head":  pr.SourceBranch,
		"base":  pr.TargetBranch,
		"body":  pr.Description,
		"draft": pr.Draft,
	}

	var raw rawPull
	if err := c.post(ctx, c.repoRoot(owner, repo)+"/pulls", body, &raw); err != nil {
		return PullRequestSummary{}, err
	}
	return mapPull(raw), nil
}

type githubCommentCreated struct {
	ID int64 `json:"id"`
}

// PostAnchoredComment attaches a comment to a file range on the pull request's right-hand side
// (PROV-011).
//
// `start_line` and `start_side` travel **only** when the range really spans more than one line:
// GitHub answers 422 to a single-line comment that also carries `start_line == line`.
//
// The returned comment id is kept by the caller: it is what a later re-review replies to, and what
// identifies the thread to resolve.
func (c GitHubClient) PostAnchoredComment(
	ctx context.Context,
	owner, repo string,
	number int64,
	content, filePath string,
	startLine, endLine int64,
	commitID string,
) (int64, error) {
	line := max(startLine, endLine)

	body := map[string]any{
		"body": content,
		// GitHub's REST API wants a bare repository-relative path — the opposite of Azure's
		// `threadContext.filePath`, which wants a leading slash. Each side normalises toward its
		// own API; this is not an inconsistency to unify.
		"path":      strings.TrimPrefix(filePath, "/"),
		"line":      line,
		"side":      "RIGHT",
		"commit_id": commitID,
	}
	if startLine < line {
		body["start_line"] = startLine
		body["start_side"] = "RIGHT"
	}

	var created githubCommentCreated
	url := fmt.Sprintf("%s/pulls/%d/comments", c.repoRoot(owner, repo), number)
	if err := c.post(ctx, url, body, &created); err != nil {
		return 0, err
	}
	return created.ID, nil
}

// PostComment adds a conversation-level comment (PROV-012).
//
// The `/issues/` path is not a mistake: GitHub models a pull request as an issue for comments that
// are not attached to a line. Used for the review summary and for any finding whose location could
// not be parsed.
func (c GitHubClient) PostComment(ctx context.Context, owner, repo string, number int64, content string) (int64, error) {
	var created githubCommentCreated
	url := fmt.Sprintf("%s/issues/%d/comments", c.repoRoot(owner, repo), number)
	if err := c.post(ctx, url, map[string]any{"body": content}, &created); err != nil {
		return 0, err
	}
	return created.ID, nil
}

// ReplyToComment threads a reply under an existing review comment (PROV-013).
func (c GitHubClient) ReplyToComment(ctx context.Context, owner, repo string, number, commentID int64, content string) error {
	url := fmt.Sprintf("%s/pulls/%d/comments/%d/replies", c.repoRoot(owner, repo), number, commentID)
	return c.post(ctx, url, map[string]any{"body": content}, nil)
}

type rawReview struct {
	User  githubAuthor `json:"user"`
	State string       `json:"state"`
}

// ViewerDecision is what the signed-in user has already decided on this pull request (PROV-015).
//
// A vote cast on the website counts the same as one cast here, which is the point: the panel hides
// the approve button for a pull request the user already approved elsewhere.
//
// The **last** review that carries a verdict wins, in whatever order GitHub returned them — the
// source assumed submission order and did not assert it, and that assumption is preserved rather
// than replaced by a sort this port would be inventing. A dismissal takes a verdict back; a comment
// or a pending review leaves the running decision alone.
func (c GitHubClient) ViewerDecision(ctx context.Context, owner, repo string, number int64) (string, error) {
	login, err := c.AuthenticatedUser(ctx)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("%s/pulls/%d/reviews?per_page=%d", c.repoRoot(owner, repo), number, githubMaxPerPage)
	var reviews []rawReview
	if err := c.get(ctx, url, &reviews); err != nil {
		return "", err
	}

	decision := DecisionNone
	for _, review := range reviews {
		if review.User.Login != login {
			continue
		}
		switch review.State {
		case "APPROVED":
			decision = DecisionApproved
		case "CHANGES_REQUESTED":
			decision = DecisionChangesRequested
		case "DISMISSED":
			decision = DecisionNone
		}
	}
	return decision, nil
}

// The two review verbs GitHub accepts from this application.
const (
	GitHubEventApprove        = "APPROVE"
	GitHubEventRequestChanges = "REQUEST_CHANGES"
)

// SubmitReview records an approval or a request for changes (PROV-016).
//
// The body is omitted rather than sent empty — an approval can carry no comment. GitHub itself
// requires one for REQUEST_CHANGES and refuses it with a 422, which is left to surface as the
// undifferentiated error every other validation failure produces.
//
// Reviewer identity comes from the token; there is no user lookup. Approving one's own pull request
// is the one 422 this client tells apart (`GitHubError.SelfApproval`).
func (c GitHubClient) SubmitReview(ctx context.Context, owner, repo string, number int64, event, content string) error {
	body := map[string]any{"event": event}
	if strings.TrimSpace(content) != "" {
		body["body"] = content
	}
	url := fmt.Sprintf("%s/pulls/%d/reviews", c.repoRoot(owner, repo), number)
	return c.post(ctx, url, body, nil)
}

// ClosePullRequest closes it without merging (PROV-017).
func (c GitHubClient) ClosePullRequest(ctx context.Context, owner, repo string, number int64) error {
	url := fmt.Sprintf("%s/pulls/%d", c.repoRoot(owner, repo), number)
	return c.patch(ctx, url, map[string]any{"state": "closed"})
}

type rawReviewComment struct {
	ID          int64         `json:"id"`
	InReplyToID *int64        `json:"in_reply_to_id"`
	Path        string        `json:"path"`
	Line        *int64        `json:"line"`
	StartLine   *int64        `json:"start_line"`
	Body        string        `json:"body"`
	CreatedAt   string        `json:"created_at"`
	User        *githubAuthor `json:"user"`
}

type rawIssueComment struct {
	ID        int64         `json:"id"`
	Body      string        `json:"body"`
	CreatedAt string        `json:"created_at"`
	User      *githubAuthor `json:"user"`
}

// ListCommentThreads reads the pull request's conversations (PROV-018).
//
// Inline review comments are grouped by their root — a reply carries the id of the comment it
// answers — and the threads come back in the order their root was **first seen** in GitHub's
// response, not sorted by id or date. `AMBIGUOUS-PROV-a`: that grouping assumes each reply arrives
// after its root, which GitHub does not document and this call does not enforce; it held in the
// live run and is left as it was rather than resolved by inventing a sort.
//
// Conversation comments are appended afterwards as one thread each, with no location: GitHub does
// not thread them.
func (c GitHubClient) ListCommentThreads(ctx context.Context, owner, repo string, number int64) ([]PrCommentThread, error) {
	inlineURL := fmt.Sprintf("%s/pulls/%d/comments?per_page=%d", c.repoRoot(owner, repo), number, githubMaxPerPage)
	var inline []rawReviewComment
	if err := c.get(ctx, inlineURL, &inline); err != nil {
		return nil, err
	}

	issueURL := fmt.Sprintf("%s/issues/%d/comments?per_page=%d", c.repoRoot(owner, repo), number, githubMaxPerPage)
	var conversation []rawIssueComment
	if err := c.get(ctx, issueURL, &conversation); err != nil {
		return nil, err
	}

	threads := make([]PrCommentThread, 0, len(inline)+len(conversation))
	index := make(map[int64]int, len(inline))

	for _, comment := range inline {
		// A comment with no text is dropped before grouping: an empty body is a review event's
		// leftover, and a thread made of one would render as a blank card.
		if strings.TrimSpace(comment.Body) == "" {
			continue
		}

		root := comment.ID
		if comment.InReplyToID != nil {
			root = *comment.InReplyToID
		}

		if at, found := index[root]; found {
			threads[at].Comments = append(threads[at].Comments, githubThreadComment(comment.User, comment.Body, comment.CreatedAt))
			continue
		}

		path := comment.Path
		start := comment.StartLine
		if start == nil {
			start = comment.Line
		}
		index[root] = len(threads)
		threads = append(threads, PrCommentThread{
			ID:        root,
			FilePath:  &path,
			StartLine: start,
			EndLine:   comment.Line,
			Comments:  []PrThreadComment{githubThreadComment(comment.User, comment.Body, comment.CreatedAt)},
		})
	}

	for _, comment := range conversation {
		if strings.TrimSpace(comment.Body) == "" {
			continue
		}
		threads = append(threads, PrCommentThread{
			ID:       comment.ID,
			Comments: []PrThreadComment{githubThreadComment(comment.User, comment.Body, comment.CreatedAt)},
		})
	}
	return threads, nil
}

// githubThreadComment maps one comment, tolerating an absent author: a comment left by a bot or by
// a deleted account comes back with `user: null`, and an empty name reads better than a crash.
func githubThreadComment(user *githubAuthor, body, createdAt string) PrThreadComment {
	author := ""
	if user != nil {
		author = user.Login
	}
	return PrThreadComment{Author: author, Content: body, PublishedDate: createdAt}
}
