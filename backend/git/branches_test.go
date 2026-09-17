package git_test

import (
	"strings"
	"testing"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge/jsonwire"
	"github.com/gastonlarap-a11y/code-flow/backend/git"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/sentinel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func branchNamed(t *testing.T, branches []git.Branch, name string) git.Branch {
	t.Helper()
	for _, branch := range branches {
		if branch.Name == name {
			return branch
		}
	}
	t.Fatalf("no branch %q; got %d", name, len(branches))
	return git.Branch{}
}

// withRemote gives a repository a real remote to track, which is the only way ahead/behind can be
// exercised — a fabricated upstream ref would not produce the track field git actually emits.
func withRemote(t *testing.T) *testRepo {
	t.Helper()
	origin := newTestRepo(t)
	origin.seed()
	origin.git("config", "receive.denyCurrentBranch", "ignore")

	clone := newTestRepo(t)
	clone.git("remote", "add", "origin", origin.Path)
	clone.git("fetch", "--quiet", "origin")
	clone.git("checkout", "--quiet", "-B", "main", "--track", "origin/main")
	return clone
}

func TestListBranches(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()
	repo.git("branch", "feature")

	branches, err := git.ListBranches(ctx(t), repo.Path)
	require.NoError(t, err)
	require.NoError(t, jsonwire.AssertNoNilSlices(branches))

	assert.Len(t, branches, 2)
	assert.True(t, branchNamed(t, branches, "main").IsHead)
	assert.False(t, branchNamed(t, branches, "feature").IsHead)
	assert.False(t, branchNamed(t, branches, "main").IsRemote)
	assert.NotNil(t, branchNamed(t, branches, "main").Target)
}

func TestAheadAndBehindAgainstARealUpstream(t *testing.T) {
	repo := withRemote(t)

	inSync := branchNamed(t, listBranches(t, repo), "main")
	require.NotNil(t, inSync.Upstream)
	assert.Equal(t, "origin/main", *inSync.Upstream)
	assert.EqualValues(t, 0, inSync.Ahead)
	assert.EqualValues(t, 0, inSync.Behind)

	repo.write("local.txt", "mine\n")
	repo.commit("a local commit")

	ahead := branchNamed(t, listBranches(t, repo), "main")
	assert.EqualValues(t, 1, ahead.Ahead)
	assert.EqualValues(t, 0, ahead.Behind)
}

func listBranches(t *testing.T, repo *testRepo) []git.Branch {
	t.Helper()
	branches, err := git.ListBranches(ctx(t), repo.Path)
	require.NoError(t, err)
	return branches
}

// A remote branch has no upstream of its own and reports 0/0: the ahead/behind of a remote against
// itself is not a question the UI asks.
func TestRemoteBranchesReportNoUpstream(t *testing.T) {
	repo := withRemote(t)

	remote := branchNamed(t, listBranches(t, repo), "origin/main")
	assert.True(t, remote.IsRemote)
	assert.Nil(t, remote.Upstream)
	assert.EqualValues(t, 0, remote.Ahead)
	assert.EqualValues(t, 0, remote.Behind)
}

// refs/remotes/origin/HEAD is a symbolic ref, not a branch anyone can check out; listing it puts a
// phantom "origin/HEAD" in the switcher.
func TestTheRemoteHeadSymbolicRefIsNotListed(t *testing.T) {
	repo := withRemote(t)
	repo.git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")

	for _, branch := range listBranches(t, repo) {
		assert.NotEqual(t, "origin/HEAD", branch.Name)
	}
}

// A deleted upstream reports 0/0 rather than an error: the branch still exists locally and the
// user still needs to see it.
func TestAGoneUpstreamIsNotAnError(t *testing.T) {
	repo := withRemote(t)
	repo.git("update-ref", "-d", "refs/remotes/origin/main")

	branches, err := git.ListBranches(ctx(t), repo.Path)
	require.NoError(t, err)

	main := branchNamed(t, branches, "main")
	assert.EqualValues(t, 0, main.Ahead)
	assert.EqualValues(t, 0, main.Behind)
}

func TestCreateAndDeleteBranch(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()

	require.NoError(t, git.CreateBranch(ctx(t), repo.Path, "feature", nil))
	assert.Len(t, listBranches(t, repo), 2)

	require.NoError(t, git.DeleteBranch(ctx(t), repo.Path, "feature", false))
	assert.Len(t, listBranches(t, repo), 1)
}

func TestCreateBranchFromAStartPoint(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()
	first := strings.TrimSpace(repo.git("rev-parse", "HEAD"))
	repo.write("second.txt", "x\n")
	repo.commit("second")

	require.NoError(t, git.CreateBranch(ctx(t), repo.Path, "from-first", &first))

	assert.Equal(t, first, *branchNamed(t, listBranches(t, repo), "from-first").Target)
}

// -D, not -d: a bare ref delete with no merged check. The UI already asked the user; asking git to
// second-guess that produces a refusal the user has overruled.
func TestDeletingAnUnmergedBranchSucceeds(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()
	repo.git("checkout", "--quiet", "-b", "unmerged")
	repo.write("only-here.txt", "x\n")
	repo.commit("work nobody merged")
	repo.git("checkout", "--quiet", "main")

	assert.NoError(t, git.DeleteBranch(ctx(t), repo.Path, "unmerged", false))
}

func TestCreatingADuplicateBranchFails(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()
	require.NoError(t, git.CreateBranch(ctx(t), repo.Path, "feature", nil))

	err := git.CreateBranch(ctx(t), repo.Path, "feature", nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "git branch failed")
}

func TestCheckoutLocalBranch(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()
	repo.git("branch", "feature")

	require.NoError(t, git.CheckoutLocalBranch(ctx(t), repo.Path, "feature"))

	status, err := git.Status(ctx(t), repo.Path)
	require.NoError(t, err)
	require.NotNil(t, status.CurrentBranch)
	assert.Equal(t, "feature", *status.CurrentBranch)
}

// XLANG-002: a blocked checkout is a recoverable state, not a failure. The renderer offers to
// carry the changes across or stash them, and it recognises the offer by the sentinel at
// position 0 — so nothing may be prepended.
func TestABlockedCheckoutCarriesTheSentinelAtPositionZero(t *testing.T) {
	repo := newTestRepo(t)
	repo.write("shared.txt", "main version\n")
	repo.commit("initial")
	repo.git("checkout", "--quiet", "-b", "feature")
	repo.write("shared.txt", "feature version\n")
	repo.commit("feature change")
	repo.git("checkout", "--quiet", "main")

	// An uncommitted change to a file that differs between the branches is what blocks a switch.
	repo.write("shared.txt", "uncommitted work\n")

	err := git.CheckoutLocalBranch(ctx(t), repo.Path, "feature")

	require.Error(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), sentinel.CheckoutConflict),
		"got %q", err.Error())
	// git's own words survive after the marker: they name the file the user has to deal with.
	assert.Contains(t, err.Error(), "shared.txt")
}

// A checkout that fails for any other reason must not carry the sentinel, or the renderer offers
// to stash changes that are not the problem.
func TestAnOrdinaryCheckoutFailureIsNotASentinel(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()

	err := git.CheckoutLocalBranch(ctx(t), repo.Path, "no-such-branch")

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "CHECKOUT_CONFLICT")
	assert.Contains(t, err.Error(), "git checkout failed")
}

func TestCheckoutDetached(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()
	head := strings.TrimSpace(repo.git("rev-parse", "HEAD"))

	require.NoError(t, git.CheckoutDetached(ctx(t), repo.Path, head))

	status, err := git.Status(ctx(t), repo.Path)
	require.NoError(t, err)
	assert.True(t, status.IsDetached)
	assert.Nil(t, status.CurrentBranch)
}

func TestCheckoutRemoteTrackingCreatesALocalBranch(t *testing.T) {
	repo := withRemote(t)
	repo.git("checkout", "--quiet", "-b", "scratch")
	repo.git("branch", "-D", "main")

	name, err := git.CheckoutRemoteTracking(ctx(t), repo.Path, "origin/main")
	require.NoError(t, err)

	assert.Equal(t, "main", name)
	local := branchNamed(t, listBranches(t, repo), "main")
	require.NotNil(t, local.Upstream)
	assert.Equal(t, "origin/main", *local.Upstream)
}

// AMBIGUOUS-GIT-a, preserved: when a local branch of that name exists it is checked out and its
// upstream is left alone. A branch pointing elsewhere on purpose must not be silently re-pointed.
func TestCheckoutRemoteTrackingLeavesAnExistingBranchesUpstreamAlone(t *testing.T) {
	repo := withRemote(t)
	repo.git("checkout", "--quiet", "-b", "other")
	repo.git("branch", "--unset-upstream", "main")

	name, err := git.CheckoutRemoteTracking(ctx(t), repo.Path, "origin/main")
	require.NoError(t, err)

	assert.Equal(t, "main", name)
	assert.Nil(t, branchNamed(t, listBranches(t, repo), "main").Upstream,
		"the upstream this port found unset must stay unset")
}

func TestCheckoutRemoteTrackingRejectsANameWithoutARemote(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()

	for _, name := range []string{"main", "origin/", "/main", ""} {
		_, err := git.CheckoutRemoteTracking(ctx(t), repo.Path, name)
		require.Error(t, err, name)
		assert.Equal(t, "expected a name like 'origin/feature-x'", err.Error())
	}
}
