package providers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/safego"
)

// What the Azure client answers (PROV-023…037).
//
// Azure's JSON says `status: "active"` on a read and takes `status: 1` on a write, and the same for
// a comment's type. Both directions are modelled as they are rather than as one type: a client that
// sent back what it read would be rejected, and one that read what it sends would find nothing.

// AdoRef is an Azure entity reduced to what a picker shows.
type AdoRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// azureList is the envelope every Azure list endpoint answers in.
type azureList[T any] struct {
	Value []T `json:"value"`
}

// ListProjects is the organisation's projects (PROV-023).
func (c AzureClient) ListProjects(ctx context.Context) ([]AdoRef, error) {
	var found azureList[AdoRef]
	url := withVersion(c.orgRoot()+"/_apis/projects", azureAPIVersion)
	if err := c.get(ctx, url, &found); err != nil {
		return nil, err
	}
	return nonNil(found.Value), nil
}

// ListRepos is a project's git repositories (PROV-023).
func (c AzureClient) ListRepos(ctx context.Context, project string) ([]AdoRef, error) {
	var found azureList[AdoRef]
	url := withVersion(c.projectRoot(project)+"/_apis/git/repositories", azureAPIVersion)
	if err := c.get(ctx, url, &found); err != nil {
		return nil, err
	}
	return nonNil(found.Value), nil
}

// nonNil keeps an empty list an empty list: a nil slice marshals as null and the panel that maps
// over it crashes on an organisation with no projects.
func nonNil[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}

// rawAzurePullRequest is the part of Azure's pull request this application reads.
type rawAzurePullRequest struct {
	PullRequestID int64  `json:"pullRequestId"`
	Title         string `json:"title"`
	Description   string `json:"description"`
	Status        string `json:"status"`
	IsDraft       bool   `json:"isDraft"`
	SourceRefName string `json:"sourceRefName"`
	TargetRefName string `json:"targetRefName"`
	CreatedBy     struct {
		ID          string `json:"id"`
		DisplayName string `json:"displayName"`
	} `json:"createdBy"`
	CreationDate string `json:"creationDate"`
	// Reviewers only travel on the single-pull-request read; the list endpoint omits them.
	Reviewers []struct {
		ID   string `json:"id"`
		Vote int    `json:"vote"`
	} `json:"reviewers"`
	Repository struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Project struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"project"`
	} `json:"repository"`
}

// AzurePullRequest is one pull request plus the two names a link cannot supply.
//
// A pull request reached through a GUID-carrying link — Azure's own notification e-mails are full
// of them — has no project or repository *name* to match against a local clone's remote, which only
// ever spells out names. Recovering them here is what makes "review this link" find the checkout.
type AzurePullRequest struct {
	Summary     PullRequestSummary
	ProjectName string
	RepoName    string
}

// stripRef removes the `refs/heads/` Azure puts on every branch name.
func stripRef(ref string) string { return strings.TrimPrefix(ref, "refs/heads/") }

// bucketAzureStatus collapses Azure's vocabulary into the four buckets (PROV-024).
func bucketAzureStatus(status string, isDraft bool) string {
	switch {
	case status == "completed":
		return StatusMerged
	case status == "abandoned":
		return StatusClosed
	case isDraft:
		return StatusDraft
	default:
		return StatusOpen
	}
}

// mapAzurePullRequest turns Azure's shape into the renderer's (PROV-024).
//
// The URL is **synthesised**, not read: Azure's response carries an API URL, not the web page a
// person opens, so the web address is rebuilt from the names — which is also why the repository's
// name is needed rather than the id the rest of the calls use.
func (c AzureClient) mapAzurePullRequest(project, repoName string, raw rawAzurePullRequest) PullRequestSummary {
	if raw.Repository.Name != "" {
		repoName = raw.Repository.Name
	}
	return PullRequestSummary{
		ID:           raw.PullRequestID,
		Title:        raw.Title,
		Description:  raw.Description,
		Status:       bucketAzureStatus(raw.Status, raw.IsDraft),
		SourceBranch: stripRef(raw.SourceRefName),
		TargetBranch: stripRef(raw.TargetRefName),
		Author:       raw.CreatedBy.DisplayName,
		CreatedAt:    raw.CreationDate,
		URL: fmt.Sprintf("https://dev.azure.com/%s/%s/_git/%s/pullrequest/%d",
			encodeSegment(c.org), encodeSegment(project), encodeSegment(repoName), raw.PullRequestID),
		Provider: ProviderAzure,
	}
}

// ListPullRequests reads a repository's pull requests in every state (PROV-025).
//
// `searchCriteria.status=all` covers active, completed and abandoned in one call. No page size is
// set, as in 2.x — `AMBIGUOUS-PROV-c`: what the server defaults to is not established, and setting
// one here would change which pull requests an existing user sees.
func (c AzureClient) ListPullRequests(ctx context.Context, project, repoID string) ([]PullRequestSummary, error) {
	// `BUG-PROV-a`: raw repo id.
	url := withVersion(c.repoRoot(project, repoID)+"/pullrequests?searchCriteria.status=all", azureAPIVersion)

	var found azureList[rawAzurePullRequest]
	if err := c.get(ctx, url, &found); err != nil {
		return nil, err
	}

	pulls := make([]PullRequestSummary, 0, len(found.Value))
	for _, raw := range found.Value {
		pulls = append(pulls, c.mapAzurePullRequest(project, repoID, raw))
	}
	return pulls, nil
}

// GetPullRequest reads one pull request, recovering the project and repository names (PROV-025).
//
// This is one of the three calls that percent-encode the repository id (`BUG-PROV-a`).
func (c AzureClient) GetPullRequest(ctx context.Context, project, repoID string, prID int64) (AzurePullRequest, error) {
	raw, err := c.rawPullRequest(ctx, project, repoID, prID)
	if err != nil {
		return AzurePullRequest{}, err
	}

	projectName := project
	if raw.Repository.Project.Name != "" {
		projectName = raw.Repository.Project.Name
	}
	repoName := repoID
	if raw.Repository.Name != "" {
		repoName = raw.Repository.Name
	}

	return AzurePullRequest{
		Summary:     c.mapAzurePullRequest(projectName, repoName, raw),
		ProjectName: projectName,
		RepoName:    repoName,
	}, nil
}

func (c AzureClient) rawPullRequest(ctx context.Context, project, repoID string, prID int64) (rawAzurePullRequest, error) {
	url := withVersion(fmt.Sprintf("%s/pullRequests/%d", c.encodedRepoRoot(project, repoID), prID), azureAPIVersion)
	var raw rawAzurePullRequest
	if err := c.get(ctx, url, &raw); err != nil {
		return rawAzurePullRequest{}, err
	}
	return raw, nil
}

// CreatePullRequest opens one (PROV-026).
//
// Azure wants the full `refs/heads/` prefix, which is the inverse of what it answers with — and the
// prefix is added unconditionally, as in 2.x, so an already-prefixed branch name would double it.
func (c AzureClient) CreatePullRequest(ctx context.Context, project, repoID string, pr NewPullRequest) (PullRequestSummary, error) {
	body := map[string]any{
		"sourceRefName": "refs/heads/" + pr.SourceBranch,
		"targetRefName": "refs/heads/" + pr.TargetBranch,
		"title":         pr.Title,
		"description":   pr.Description,
		"isDraft":       pr.Draft,
	}

	// `BUG-PROV-a`: raw repo id.
	url := withVersion(c.repoRoot(project, repoID)+"/pullrequests", azureAPIVersion)
	var raw rawAzurePullRequest
	if err := c.post(ctx, url, body, &raw); err != nil {
		return PullRequestSummary{}, err
	}
	return c.mapAzurePullRequest(project, repoID, raw), nil
}

// LatestIterationID is the pull request's newest iteration (PROV-027).
//
// An empty list falls back to iteration 1 rather than failing: it should not happen for a real pull
// request, and a comment landing on iteration 1 beats the whole review failing to post.
func (c AzureClient) LatestIterationID(ctx context.Context, project, repoID string, prID int64) (int64, error) {
	// `BUG-PROV-a`: raw repo id.
	url := withVersion(fmt.Sprintf("%s/pullRequests/%d/iterations", c.repoRoot(project, repoID), prID), azureAPIVersion)

	var found azureList[struct {
		ID int64 `json:"id"`
	}]
	if err := c.get(ctx, url, &found); err != nil {
		return 0, err
	}
	if len(found.Value) == 0 {
		return 1, nil
	}
	return found.Value[len(found.Value)-1].ID, nil
}

// Blob reads one file's bytes. Its error is the one in this client that drops the response body:
// a blob's failure body is not a sentence anybody reads.
func (c AzureClient) Blob(ctx context.Context, project, repoID, sha string) ([]byte, error) {
	url := withVersion(fmt.Sprintf("%s/blobs/%s", c.encodedRepoRoot(project, repoID), sha), azureAPIVersion)

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build the blob request: %w", err)
	}
	c.authorize(request)
	request.Header.Set("Accept", "application/octet-stream")

	response, err := c.client.Do(request)
	if err != nil {
		return nil, &AzureError{Err: err}
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode < 200 || response.StatusCode > 299 {
		failure := newAzureStatusError(response.StatusCode, "")
		failure.Body = ""
		return nil, fmt.Errorf("Azure DevOps returned %d reading a file", response.StatusCode) //nolint:staticcheck // ST1005: VERBATIM
	}
	return io.ReadAll(response.Body)
}

// ErrNoFileChanges is what a pull request with nothing in it answers.
var ErrNoFileChanges = errors.New("This pull request has no file changes to review") //nolint:staticcheck // ST1005: VERBATIM

// azureChange is one changed file, reduced to the two blob ids a diff needs.
type azureChange struct {
	path  string
	oldID string
	newID string
	kind  string
}

// PullRequestDiff assembles a unified diff from the pull request's changed files (PROV-028).
//
// Azure has no diff endpoint at all — this is the whole reason `UnifiedPatch` exists. Each file's
// two blobs are fetched and rendered, a few files at a time, and the order of the result is the
// order Azure listed them in.
func (c AzureClient) PullRequestDiff(ctx context.Context, project, repoID string, prID, iterationID int64) (string, error) {
	changes, total, err := c.changedFiles(ctx, project, repoID, prID, iterationID)
	if err != nil {
		return "", err
	}
	if total == 0 {
		return "", ErrNoFileChanges
	}

	rendered := make([]string, len(changes))
	semaphore := make(chan struct{}, azureDiffConcurrency)
	wait := &sync.WaitGroup{}

	for i, change := range changes {
		wait.Add(1)
		semaphore <- struct{}{}

		safego.Go("azure-diff-file", func() {
			defer wait.Done()
			defer func() { <-semaphore }()
			rendered[i] = c.renderChange(ctx, project, repoID, change)
		})
	}
	wait.Wait()

	diff := strings.Join(rendered, "")
	if total > len(changes) {
		diff += fmt.Sprintf("(only the first %d of %d changed files are included)\n", azureMaxDiffFiles, total)
	}
	return diff, nil
}

// changedFiles lists the iteration's changes, dropping folders and capping the count.
func (c AzureClient) changedFiles(ctx context.Context, project, repoID string, prID, iterationID int64) (changes []azureChange, total int, err error) {
	url := withVersion(fmt.Sprintf("%s/pullRequests/%d/iterations/%d/changes?$top=1000",
		c.encodedRepoRoot(project, repoID), prID, iterationID), azureAPIVersion)

	// `isFolder` is not in Microsoft's reference for this resource — it belongs to the Items API —
	// but 2.x read it here and the payload carries it on directory entries. Absent, it decodes as
	// false and nothing is dropped, so keeping the check costs nothing and preserves the behaviour.
	var found struct {
		ChangeEntries []struct {
			ChangeType string `json:"changeType"`
			Item       struct {
				Path             string `json:"path"`
				ObjectID         string `json:"objectId"`
				OriginalObjectID string `json:"originalObjectId"`
				IsFolder         bool   `json:"isFolder"`
			} `json:"item"`
		} `json:"changeEntries"`
	}
	if err := c.get(ctx, url, &found); err != nil {
		return nil, 0, err
	}

	changes = make([]azureChange, 0, len(found.ChangeEntries))
	for _, entry := range found.ChangeEntries {
		if entry.Item.IsFolder {
			continue
		}
		kind := strings.ToLower(entry.ChangeType)

		change := azureChange{
			// Azure's paths are absolute within the repository; a diff's are not.
			path: strings.TrimPrefix(entry.Item.Path, "/"),
			kind: entry.ChangeType,
		}
		if !strings.Contains(kind, "add") {
			change.oldID = realObjectID(entry.Item.OriginalObjectID)
		}
		if !strings.Contains(kind, "delete") {
			change.newID = realObjectID(entry.Item.ObjectID)
		}
		changes = append(changes, change)
	}

	total = len(changes)
	if total > azureMaxDiffFiles {
		changes = changes[:azureMaxDiffFiles]
	}
	return changes, total, nil
}

// realObjectID drops the two ids that mean "there is nothing on this side".
func realObjectID(id string) string {
	if id == "" || id == azureNullObjectID {
		return ""
	}
	return id
}

// renderChange fetches one file's two sides and renders them, or says why it could not.
//
// Never returns an error: one unreadable file must not fail a review of the other seventy-nine, and
// the reviewer is told which file it was and why.
func (c AzureClient) renderChange(ctx context.Context, project, repoID string, change azureChange) string {
	header := fmt.Sprintf("diff --git a/%s b/%s\n", change.path, change.path)

	old, err := c.blobOrEmpty(ctx, project, repoID, change.oldID)
	if err != nil {
		return header + "(couldn't read this file from Azure DevOps)\n"
	}
	current, err := c.blobOrEmpty(ctx, project, repoID, change.newID)
	if err != nil {
		return header + "(couldn't read this file from Azure DevOps)\n"
	}

	// The size check runs **after** both fetches, as in 2.x: the cost is already paid, and the
	// decision is about what a model and a reader can use, not about bandwidth.
	if len(old) > azureMaxBlobBytes || len(current) > azureMaxBlobBytes {
		return header + fmt.Sprintf("(%s, too large to display)\n", change.kind)
	}

	patch, ok := UnifiedPatch(change.path, old, current)
	if !ok {
		return header + fmt.Sprintf("(%s, binary)\n", change.kind)
	}
	return patch
}

func (c AzureClient) blobOrEmpty(ctx context.Context, project, repoID, sha string) ([]byte, error) {
	if sha == "" {
		return nil, nil
	}
	return c.Blob(ctx, project, repoID, sha)
}

// azureThreadCreated is what a created thread answers with.
type azureThreadCreated struct {
	ID int64 `json:"id"`
}

// PostAnchoredComment opens a thread attached to a file range (PROV-030).
//
// The range is always on the **right** file — the version being reviewed — at column 1, and the
// iteration context pins the comparison to the whole pull request rather than to one push.
func (c AzureClient) PostAnchoredComment(
	ctx context.Context,
	project, repoID string,
	prID int64,
	content, filePath string,
	startLine, endLine int64,
) (int64, error) {
	iteration, err := c.LatestIterationID(ctx, project, repoID, prID)
	if err != nil {
		return 0, err
	}

	body := map[string]any{
		"comments": []map[string]any{{"parentCommentId": 0, "content": content, "commentType": 1}},
		"status":   AzureThreadActive,
		"threadContext": map[string]any{
			// Azure wants an absolute-from-repository-root path: a leading slash is added when
			// missing and never stripped — the opposite of GitHub's normaliser, and deliberately so.
			"filePath":       azureThreadPath(filePath),
			"rightFileStart": map[string]any{"line": startLine, "offset": 1},
			"rightFileEnd":   map[string]any{"line": max(startLine, endLine), "offset": 1},
		},
		"pullRequestThreadContext": map[string]any{
			"iterationContext": map[string]any{
				"firstComparingIteration":  1,
				"secondComparingIteration": iteration,
			},
		},
	}
	return c.postThread(ctx, project, repoID, prID, body)
}

func azureThreadPath(filePath string) string {
	if strings.HasPrefix(filePath, "/") {
		return filePath
	}
	return "/" + filePath
}

// PostComment opens a pull-request-level thread, with no file attached (PROV-031).
func (c AzureClient) PostComment(ctx context.Context, project, repoID string, prID int64, content string) (int64, error) {
	body := map[string]any{
		"comments": []map[string]any{{"parentCommentId": 0, "content": content, "commentType": 1}},
		"status":   AzureThreadActive,
	}
	return c.postThread(ctx, project, repoID, prID, body)
}

// postThread is the one POST both comment kinds share.
func (c AzureClient) postThread(ctx context.Context, project, repoID string, prID int64, body map[string]any) (int64, error) {
	// `BUG-PROV-a`: raw repo id.
	url := withVersion(fmt.Sprintf("%s/pullRequests/%d/threads", c.repoRoot(project, repoID), prID), azureAPIVersion)

	var created azureThreadCreated
	if err := c.post(ctx, url, body, &created); err != nil {
		return 0, err
	}
	return created.ID, nil
}

// ReplyToThread adds a comment under a thread's root (PROV-032).
//
// `parentCommentId` is **1**, not the root comment's real id: every thread this application creates
// starts with exactly one comment, and Azure numbers a thread's comments from 1. It is not
// re-derived from the thread, and the live run confirmed it holds for every thread this app made.
func (c AzureClient) ReplyToThread(ctx context.Context, project, repoID string, prID, threadID int64, content string) error {
	// `BUG-PROV-a`: raw repo id.
	url := withVersion(fmt.Sprintf("%s/pullRequests/%d/threads/%d/comments",
		c.repoRoot(project, repoID), prID, threadID), azureAPIVersion)

	return c.post(ctx, url, map[string]any{
		"parentCommentId": 1,
		"content":         content,
		"commentType":     1,
	}, nil)
}

// SetThreadStatus marks a thread (PROV-033). A resolved finding's thread is set to fixed.
//
// `UNVERIFIED`: this is the one write path the 2026-08-01 live run could not reach — it fires only
// when a re-review resolves a posted finding, which needs a second push to the pull request's
// remote branch, and the throwaway repository allowed one.
func (c AzureClient) SetThreadStatus(ctx context.Context, project, repoID string, prID, threadID int64, status int) error {
	// `BUG-PROV-a`: raw repo id.
	url := withVersion(fmt.Sprintf("%s/pullRequests/%d/threads/%d",
		c.repoRoot(project, repoID), prID, threadID), azureAPIVersion)

	return c.patch(ctx, url, map[string]any{"status": status})
}

// AuthenticatedUserID is the signed-in user's Azure DevOps id (PROV-034).
//
// Organisation-scoped, and the one call that uses the preview api-version: this endpoint never went
// GA and the server rejects a plain 7.1 on it with a 400 demanding the suffix.
//
// Azure needs the id because its votes are keyed by reviewer, unlike GitHub's reviews, which are
// attributed to whoever the token belongs to.
func (c AzureClient) AuthenticatedUserID(ctx context.Context) (string, error) {
	url := withVersion(c.orgRoot()+"/_apis/connectionData", azurePreviewAPIVersion)

	var found struct {
		AuthenticatedUser struct {
			ID string `json:"id"`
		} `json:"authenticatedUser"`
	}
	if err := c.get(ctx, url, &found); err != nil {
		return "", err
	}
	return found.AuthenticatedUser.ID, nil
}

// SetReviewerVote records the caller's vote (PROV-034).
//
// One PUT does both jobs: it adds the caller as a reviewer if they were not one, and sets the vote.
// Azure, unlike GitHub, accepts a vote on one's own pull request.
func (c AzureClient) SetReviewerVote(ctx context.Context, project, repoID string, prID int64, vote int) error {
	userID, err := c.AuthenticatedUserID(ctx)
	if err != nil {
		return err
	}

	// `BUG-PROV-a`: raw repo id.
	url := withVersion(fmt.Sprintf("%s/pullRequests/%d/reviewers/%s",
		c.repoRoot(project, repoID), prID, userID), azureAPIVersion)

	return c.put(ctx, url, map[string]any{"vote": vote})
}

// ViewerDecision is what the signed-in user has already voted (PROV-035).
//
// Votes 10 and 5 both read as approved, and -10 and -5 both as changes requested: the renderer
// offers three states, and "approved with suggestions" is an approval as far as the button is
// concerned.
func (c AzureClient) ViewerDecision(ctx context.Context, project, repoID string, prID int64) (string, error) {
	userID, err := c.AuthenticatedUserID(ctx)
	if err != nil {
		return "", err
	}

	// One of the three calls that encode the repository id (`BUG-PROV-a`). The single-pull-request
	// read is used because it carries `reviewers`, which the list endpoint omits.
	raw, err := c.rawPullRequest(ctx, project, repoID, prID)
	if err != nil {
		return "", err
	}

	for _, reviewer := range raw.Reviewers {
		if !strings.EqualFold(reviewer.ID, userID) {
			continue
		}
		switch {
		case reviewer.Vote > 0:
			return DecisionApproved, nil
		case reviewer.Vote < 0:
			return DecisionChangesRequested, nil
		}
		return DecisionNone, nil
	}
	return DecisionNone, nil
}

// AbandonPullRequest is Azure's close-without-merging (PROV-036).
func (c AzureClient) AbandonPullRequest(ctx context.Context, project, repoID string, prID int64) error {
	// `BUG-PROV-a`: raw repo id.
	url := withVersion(fmt.Sprintf("%s/pullRequests/%d", c.repoRoot(project, repoID), prID), azureAPIVersion)
	return c.patch(ctx, url, map[string]any{"status": "abandoned"})
}

// ListCommentThreads reads the pull request's open conversations (PROV-037).
//
// Two filters, and both matter: a thread that is fixed, closed, wontFix or byDesign is done with,
// and inside a kept thread only real text comments survive — Azure files vote changes and iteration
// notices as comments of other types, and a panel that showed them would be mostly noise. A thread
// left with nothing after that is dropped entirely.
//
// The file path is carried through exactly as Azure wrote it, leading slash included: normalising
// on read is the write path's job, not this one's.
func (c AzureClient) ListCommentThreads(ctx context.Context, project, repoID string, prID int64) ([]PrCommentThread, error) {
	// `BUG-PROV-a`: raw repo id. Not named in the specification's enumeration of either group; the
	// encoded set is enumerated exhaustively and this is not in it, so it is raw like the rest.
	url := withVersion(fmt.Sprintf("%s/pullRequests/%d/threads", c.repoRoot(project, repoID), prID), azureAPIVersion)

	var found azureList[struct {
		ID       int64  `json:"id"`
		Status   string `json:"status"`
		Comments []struct {
			Content     string `json:"content"`
			CommentType string `json:"commentType"`
			PublishedAt string `json:"publishedDate"`
			Author      struct {
				DisplayName string `json:"displayName"`
			} `json:"author"`
		} `json:"comments"`
		ThreadContext *struct {
			FilePath       string `json:"filePath"`
			RightFileStart *struct {
				Line int64 `json:"line"`
			} `json:"rightFileStart"`
			RightFileEnd *struct {
				Line int64 `json:"line"`
			} `json:"rightFileEnd"`
		} `json:"threadContext"`
	}]
	if err := c.get(ctx, url, &found); err != nil {
		return nil, err
	}

	threads := make([]PrCommentThread, 0, len(found.Value))
	for _, raw := range found.Value {
		if !azureThreadIsOpen(raw.Status) {
			continue
		}

		comments := make([]PrThreadComment, 0, len(raw.Comments))
		for _, comment := range raw.Comments {
			// An absent commentType is a text comment, which is what Azure omits it for.
			if comment.CommentType != "" && !strings.EqualFold(comment.CommentType, "text") {
				continue
			}
			if strings.TrimSpace(comment.Content) == "" {
				continue
			}
			comments = append(comments, PrThreadComment{
				Author:        comment.Author.DisplayName,
				Content:       comment.Content,
				PublishedDate: comment.PublishedAt,
			})
		}
		if len(comments) == 0 {
			continue
		}

		thread := PrCommentThread{ID: raw.ID, Comments: comments}
		if raw.ThreadContext != nil && raw.ThreadContext.FilePath != "" {
			path := raw.ThreadContext.FilePath
			thread.FilePath = &path
			if raw.ThreadContext.RightFileStart != nil {
				start := raw.ThreadContext.RightFileStart.Line
				thread.StartLine = &start
			}
			if raw.ThreadContext.RightFileEnd != nil {
				end := raw.ThreadContext.RightFileEnd.Line
				thread.EndLine = &end
			}
		}
		threads = append(threads, thread)
	}
	return threads, nil
}

// azureThreadIsOpen keeps the statuses that mean the conversation is still live. An absent status
// counts as open, which is the same bucket as active and pending.
func azureThreadIsOpen(status string) bool {
	switch strings.ToLower(status) {
	case "", "active", "pending":
		return true
	default:
		return false
	}
}
