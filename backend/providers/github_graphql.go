package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// Resolving a review thread (PROV-014, `VERIFIED-LIVE` 2026-08-01).
//
// There is no REST endpoint for it, so this one call goes through GraphQL — and it is the only
// GraphQL in the application. Two sequential requests: find the thread that owns a comment, then
// resolve it.

// The two query templates are `VERBATIM`: the text, the field order and the `first: 100` caps are
// the contract, interpolated exactly as 2.x interpolated them — unescaped, straight from the
// caller's arguments.
const (
	githubThreadQuery = `query { repository(owner: "%s", name: "%s") { pullRequest(number: %d) { reviewThreads(first: 100) { nodes { id isResolved comments(first: 100) { nodes { databaseId } } } } } } }`

	githubResolveMutation = `mutation { resolveReviewThread(input: { threadId: "%s" }) { thread { isResolved } } }`
)

// The three failures this call can report that the REST mapping has no equivalent for. All three
// are best-effort at the call site: a thread that stays open is a worse review, never a failed one.
var (
	ErrNoReviewThreads = errors.New("no review threads in GraphQL response")
	ErrThreadNotFound  = errors.New("couldn't find the review thread for this comment") //nolint:staticcheck // ST1005: VERBATIM
)

// graphQLThreadsResponse is the shape of the first query's answer.
//
// `databaseId` is the REST comment id and `id` is the GraphQL node id: two different identifiers
// for the same comment, and the mutation needs the second one while the caller only has the first.
type graphQLThreadsResponse struct {
	Data struct {
		Repository struct {
			PullRequest struct {
				ReviewThreads struct {
					Nodes []struct {
						ID         string `json:"id"`
						IsResolved bool   `json:"isResolved"`
						Comments   struct {
							Nodes []struct {
								DatabaseID int64 `json:"databaseId"`
							} `json:"nodes"`
						} `json:"comments"`
					} `json:"nodes"`
				} `json:"reviewThreads"`
			} `json:"pullRequest"`
		} `json:"repository"`
	} `json:"data"`
}

// ResolveReviewThreadForComment marks the thread a comment belongs to as resolved (PROV-014).
//
// Capped at the first 100 threads and the first 100 comments in each, as in 2.x: a pull request
// past either cap can hold a thread this call cannot find, and it then reports that rather than
// resolving the wrong one.
func (c GitHubClient) ResolveReviewThreadForComment(
	ctx context.Context,
	owner, repo string,
	number, commentID int64,
) error {
	query := fmt.Sprintf(githubThreadQuery, owner, repo, number)
	raw, err := c.graphQL(ctx, query, "GitHub GraphQL")
	if err != nil {
		return err
	}

	var found graphQLThreadsResponse
	if err := json.Unmarshal(raw, &found); err != nil {
		return &GitHubError{Err: fmt.Errorf("%w: %w", ErrGitHubDecode, err)}
	}
	nodes := found.Data.Repository.PullRequest.ReviewThreads.Nodes
	if len(nodes) == 0 {
		return ErrNoReviewThreads
	}

	threadID := ""
	for _, thread := range nodes {
		for _, comment := range thread.Comments.Nodes {
			if comment.DatabaseID == commentID {
				threadID = thread.ID
				break
			}
		}
		if threadID != "" {
			break
		}
	}
	if threadID == "" {
		return ErrThreadNotFound
	}

	// Success is not read back: 2.x ignored the returned `isResolved`, and the call site treats the
	// whole operation as best-effort anyway.
	_, err = c.graphQL(ctx, fmt.Sprintf(githubResolveMutation, threadID), "GitHub GraphQL resolve")
	return err
}

// graphQL posts one query and returns the raw body.
//
// The headers are not the REST ones: `Accept` and `X-GitHub-Api-Version` are REST-only and 2.x does
// not send them here. The status error carries **no body**, unlike every REST error in this
// package — preserved, because those two messages are what an old log holds and what a reader of
// one matches against. `failurePrefix` is which of the two calls failed.
func (c GitHubClient) graphQL(ctx context.Context, query, failurePrefix string) ([]byte, error) {
	body, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		return nil, fmt.Errorf("encode the GraphQL query: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.graphqlRoot(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build the GraphQL request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("User-Agent", githubUserAgent)
	request.Header.Set("Content-Type", "application/json")

	response, err := c.client.Do(request)
	if err != nil {
		return nil, &GitHubError{Err: err}
	}
	defer func() { _ = response.Body.Close() }()

	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, &GitHubError{Err: err}
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, fmt.Errorf("%s returned %d", failurePrefix, response.StatusCode)
	}
	return raw, nil
}
