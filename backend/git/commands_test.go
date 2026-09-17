package git_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeIdentity stands in for the workspace store. Hand-written rather than generated: the
// interface has one method, and what the test needs to know is which path it was asked about.
type fakeIdentity struct {
	name, email *string
	err         error
	askedFor    string
}

func (f *fakeIdentity) ResolveGitIdentity(_ context.Context, repoPath string) (*string, *string, error) {
	f.askedFor = repoPath
	return f.name, f.email, f.err
}

func newGitService(t *testing.T, identity git.IdentityResolver) *bridge.Service {
	t.Helper()
	r := bridge.NewRegistry()
	git.Register(r, git.Deps{Identity: identity})
	r.Seal()
	return bridge.NewService(r, nil)
}

func identityOf(t *testing.T, repo *testRepo) string {
	t.Helper()
	return strings.TrimSpace(repo.git("log", "-1", "--format=%an|%ae|%cn|%ce"))
}

// The workspace's identity is what a commit from a registered repository is attributed to
// (WS-008), even though `commit` sends no author on the wire.
func TestCommitUsesTheWorkspaceIdentity(t *testing.T) {
	repo := newTestRepo(t)
	repo.write("a.txt", "one\n")
	repo.git("add", "a.txt")

	name, email := "Workspace Name", "workspace@example.com"
	identity := &fakeIdentity{name: &name, email: &email}
	svc := newGitService(t, identity)

	_, err := svc.Invoke(t.Context(), "commit",
		json.RawMessage(fmt.Sprintf(`{"repoPath":%q,"message":"from a workspace"}`, repo.Path)))

	require.NoError(t, err)
	assert.Equal(t, repo.Path, identity.askedFor, "resolution is by the repository's path on disk")
	assert.Equal(t, "Workspace Name|workspace@example.com|Workspace Name|workspace@example.com",
		identityOf(t, repo))
}

// Explicit arguments win over the workspace, and the store is not even consulted.
func TestAnExplicitAuthorSkipsTheWorkspaceLookup(t *testing.T) {
	repo := newTestRepo(t)
	repo.write("a.txt", "one\n")
	repo.git("add", "a.txt")

	workspaceName, workspaceEmail := "Workspace Name", "workspace@example.com"
	identity := &fakeIdentity{name: &workspaceName, email: &workspaceEmail}
	svc := newGitService(t, identity)

	_, err := svc.Invoke(t.Context(), "commit", json.RawMessage(fmt.Sprintf(
		`{"repoPath":%q,"message":"explicit","authorName":"Asked For","authorEmail":"asked@example.com"}`,
		repo.Path)))

	require.NoError(t, err)
	assert.Empty(t, identity.askedFor, "the arguments answered the question already")
	assert.Equal(t, "Asked For|asked@example.com|Asked For|asked@example.com", identityOf(t, repo))
}

// No database — the storage stage failed, or the repository belongs to no project. Committing
// still works, under the repository's own configured identity.
func TestCommittingWithoutAnIdentityResolver(t *testing.T) {
	repo := newTestRepo(t)
	repo.write("a.txt", "one\n")
	repo.git("add", "a.txt")
	svc := newGitService(t, nil)

	_, err := svc.Invoke(t.Context(), "commit",
		json.RawMessage(fmt.Sprintf(`{"repoPath":%q,"message":"no store"}`, repo.Path)))

	require.NoError(t, err)
	assert.Equal(t, "Test|test@example.com|Test|test@example.com", identityOf(t, repo),
		"the repository's own config, which is where git looks when nothing else says")
}

// A merge commit is attributed the same way, and merge_branch takes no author on the wire at all.
func TestMergeUsesTheWorkspaceIdentity(t *testing.T) {
	repo := diverged(t, false)
	name, email := "Workspace Name", "workspace@example.com"
	svc := newGitService(t, &fakeIdentity{name: &name, email: &email})

	out, err := svc.Invoke(t.Context(), "merge_branch",
		json.RawMessage(fmt.Sprintf(`{"repoPath":%q,"branchName":"other"}`, repo.Path)))

	require.NoError(t, err)
	assert.JSONEq(t, `{"status":"merged","conflicts":[]}`, string(out))
	assert.Equal(t, "Workspace Name|workspace@example.com|Workspace Name|workspace@example.com",
		identityOf(t, repo))
}

// A failed lookup is not swallowed: committing under the wrong name is worse than not committing.
func TestAFailedIdentityLookupStopsTheCommit(t *testing.T) {
	repo := newTestRepo(t)
	repo.write("a.txt", "one\n")
	repo.git("add", "a.txt")
	svc := newGitService(t, &fakeIdentity{err: errors.New("database is locked")})

	_, err := svc.Invoke(t.Context(), "commit",
		json.RawMessage(fmt.Sprintf(`{"repoPath":%q,"message":"blocked"}`, repo.Path)))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "database is locked")
}

// Conflicts reach the renderer as a successful result with the list filled in, never as an error.
func TestConflictsCrossTheBridgeAsAResult(t *testing.T) {
	repo := diverged(t, true)
	svc := newGitService(t, nil)

	out, err := svc.Invoke(t.Context(), "merge_branch",
		json.RawMessage(fmt.Sprintf(`{"repoPath":%q,"branchName":"other"}`, repo.Path)))

	require.NoError(t, err)
	assert.JSONEq(t, `{"status":"conflicts","conflicts":["shared.txt"]}`, string(out))
}

func TestConflictFileFieldNamesMatchTheRenderer(t *testing.T) {
	repo := diverged(t, true)
	mergeOther(t, repo)
	svc := newGitService(t, nil)

	out, err := svc.Invoke(t.Context(), "list_conflicts",
		json.RawMessage(fmt.Sprintf(`{"repoPath":%q}`, repo.Path)))

	require.NoError(t, err)
	assert.JSONEq(t, `[{"path":"shared.txt"}]`, string(out))
}
