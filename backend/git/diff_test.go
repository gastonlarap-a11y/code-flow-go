package git_test

import (
	"strings"
	"testing"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge/jsonwire"
	"github.com/gastonlarap-a11y/code-flow/backend/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func findDiff(t *testing.T, diffs []git.FileDiff, path string) git.FileDiff {
	t.Helper()
	for _, diff := range diffs {
		if (diff.NewPath != nil && *diff.NewPath == path) || (diff.OldPath != nil && *diff.OldPath == path) {
			return diff
		}
	}
	t.Fatalf("no diff for %s; got %d diffs", path, len(diffs))
	return git.FileDiff{}
}

func originsOf(hunk git.DiffHunk) string {
	var b strings.Builder
	for _, line := range hunk.Lines {
		b.WriteString(line.Origin)
	}
	return b.String()
}

func TestWorkingDiffShowsModifications(t *testing.T) {
	repo := newTestRepo(t)
	repo.write("a.txt", "one\ntwo\nthree\n")
	repo.commit("initial")

	repo.write("a.txt", "one\nCHANGED\nthree\n")

	diffs, err := git.WorkingDiff(ctx(t), repo.Path)
	require.NoError(t, err)
	require.NoError(t, jsonwire.AssertNoNilSlices(diffs))

	diff := findDiff(t, diffs, "a.txt")
	assert.Equal(t, "modified", diff.Status)
	require.Len(t, diff.Hunks, 1, "-U1000000 gives the whole file as one hunk")

	// Context, removal, addition, context — and the removed and added lines carry a line number on
	// their own side only, which is what lets the renderer align two gutters.
	assert.Equal(t, " -+ ", originsOf(diff.Hunks[0]))
	for _, line := range diff.Hunks[0].Lines {
		switch line.Origin {
		case "-":
			require.NotNil(t, line.OldLineNo)
			assert.Nil(t, line.NewLineNo)
		case "+":
			assert.Nil(t, line.OldLineNo)
			require.NotNil(t, line.NewLineNo)
		case " ":
			require.NotNil(t, line.OldLineNo)
			require.NotNil(t, line.NewLineNo)
		}
	}
}

// Full context is the point of -U1000000: the renderer shows the whole file, not islands.
func TestWorkingDiffCarriesFullContext(t *testing.T) {
	repo := newTestRepo(t)
	var lines []string
	for i := range 50 {
		lines = append(lines, "line "+string(rune('a'+i%26))+"\n")
	}
	repo.write("big.txt", strings.Join(lines, ""))
	repo.commit("initial")

	repo.write("big.txt", strings.Replace(strings.Join(lines, ""), "line a\n", "line CHANGED\n", 1))

	diffs, err := git.WorkingDiff(ctx(t), repo.Path)
	require.NoError(t, err)

	diff := findDiff(t, diffs, "big.txt")
	require.Len(t, diff.Hunks, 1)
	assert.Equal(t, "@@ -1,50 +1,50 @@", diff.Hunks[0].Header)
	assert.Len(t, diff.Hunks[0].Lines, 51, "50 lines plus the one added")
}

// git's own `diff` does not list untracked files; 2.x did, because a user asking "what have I
// changed" means the new files too.
func TestWorkingDiffIncludesUntrackedFiles(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()

	repo.write("brand-new.txt", "hello\nworld\n")

	diffs, err := git.WorkingDiff(ctx(t), repo.Path)
	require.NoError(t, err)

	diff := findDiff(t, diffs, "brand-new.txt")
	assert.Equal(t, "added", diff.Status)
	assert.Nil(t, diff.OldPath)
	require.Len(t, diff.Hunks, 1)
	assert.Equal(t, "++", originsOf(diff.Hunks[0]))
}

func TestStagedDiffShowsOnlyWhatWouldBeCommitted(t *testing.T) {
	repo := newTestRepo(t)
	repo.write("a.txt", "one\n")
	repo.commit("initial")

	repo.write("a.txt", "staged\n")
	repo.git("add", "a.txt")
	repo.write("b.txt", "not staged\n")

	diffs, err := git.StagedDiff(ctx(t), repo.Path)
	require.NoError(t, err)

	require.Len(t, diffs, 1)
	assert.Equal(t, "a.txt", *diffs[0].NewPath)
}

func TestAdditionsAndDeletionsCarryOnlyTheirOwnPath(t *testing.T) {
	repo := newTestRepo(t)
	repo.write("gone.txt", "bye\n")
	repo.commit("initial")

	repo.remove("gone.txt")
	repo.write("fresh.txt", "hi\n")
	repo.git("add", "-A")

	diffs, err := git.StagedDiff(ctx(t), repo.Path)
	require.NoError(t, err)

	deleted := findDiff(t, diffs, "gone.txt")
	assert.Equal(t, "deleted", deleted.Status)
	assert.Nil(t, deleted.NewPath, "a deletion has no new path")
	require.NotNil(t, deleted.OldPath)

	added := findDiff(t, diffs, "fresh.txt")
	assert.Equal(t, "added", added.Status)
	assert.Nil(t, added.OldPath, "an addition has no old path")
}

// -M keeps a move as one entry with both paths rather than a delete plus an add.
func TestRenamesCarryBothPaths(t *testing.T) {
	repo := newTestRepo(t)
	repo.write("old.txt", "content long enough to be detected as a rename\n")
	repo.commit("initial")

	repo.git("mv", "old.txt", "new.txt")

	diffs, err := git.StagedDiff(ctx(t), repo.Path)
	require.NoError(t, err)

	require.Len(t, diffs, 1)
	assert.Equal(t, "renamed", diffs[0].Status)
	require.NotNil(t, diffs[0].OldPath)
	require.NotNil(t, diffs[0].NewPath)
	assert.Equal(t, "old.txt", *diffs[0].OldPath)
	assert.Equal(t, "new.txt", *diffs[0].NewPath)
}

// A binary file has no hunks; the renderer shows "binary file" instead of a diff. Hunks must be an
// empty array rather than nil, or the map it does first crashes the panel.
func TestBinaryFilesHaveNoHunksButStillAnArray(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()
	repo.writeBytes("logo.png", []byte{0x89, 'P', 'N', 'G', 0x00, 0x01, 0x02, 0x00, 0xff})
	repo.git("add", "logo.png")

	diffs, err := git.StagedDiff(ctx(t), repo.Path)
	require.NoError(t, err)

	diff := findDiff(t, diffs, "logo.png")
	assert.Empty(t, diff.Hunks)
	assert.NotNil(t, diff.Hunks, "nil would marshal as null and crash the renderer's map")
	require.NoError(t, jsonwire.AssertNoNilSlices(diffs))
}

func TestCommitDiff(t *testing.T) {
	repo := newTestRepo(t)
	repo.write("a.txt", "one\n")
	repo.commit("initial")
	repo.write("a.txt", "two\n")
	repo.commit("second")

	head := strings.TrimSpace(repo.git("rev-parse", "HEAD"))

	diffs, err := git.CommitDiff(ctx(t), repo.Path, head)
	require.NoError(t, err)

	diff := findDiff(t, diffs, "a.txt")
	assert.Equal(t, "modified", diff.Status)
	assert.Equal(t, "-+", originsOf(diff.Hunks[0]))
}

// A root commit has no parent to diff against. Without the empty-tree comparison the very first
// commit in a repository shows nothing — which is exactly what a new user looks at first.
func TestTheRootCommitDiffsAgainstTheEmptyTree(t *testing.T) {
	repo := newTestRepo(t)
	repo.write("a.txt", "one\ntwo\n")
	repo.commit("initial")

	root := strings.TrimSpace(repo.git("rev-parse", "HEAD"))

	diffs, err := git.CommitDiff(ctx(t), repo.Path, root)
	require.NoError(t, err)

	diff := findDiff(t, diffs, "a.txt")
	assert.Equal(t, "added", diff.Status)
	require.Len(t, diff.Hunks, 1)
	assert.Equal(t, "++", originsOf(diff.Hunks[0]))
}

func TestListCommitFiles(t *testing.T) {
	repo := newTestRepo(t)
	repo.write("keep.txt", "keep\n")
	repo.write("gone.txt", "gone\n")
	repo.commit("initial")

	repo.remove("gone.txt")
	repo.write("added.txt", "new\n")
	repo.write("keep.txt", "changed\n")
	repo.commit("second")

	head := strings.TrimSpace(repo.git("rev-parse", "HEAD"))

	files, err := git.ListCommitFiles(ctx(t), repo.Path, head)
	require.NoError(t, err)
	require.NoError(t, jsonwire.AssertNoNilSlices(files))

	byStatus := map[string]git.CommitFile{}
	for _, file := range files {
		byStatus[file.Status] = file
	}
	assert.Contains(t, byStatus, "modified")
	assert.Contains(t, byStatus, "added")
	assert.Contains(t, byStatus, "deleted")
	assert.Nil(t, byStatus["deleted"].NewPath)
	assert.Nil(t, byStatus["added"].OldPath)
}

// A renamed file needs both names, or its diff comes back as an addition.
func TestCommitFileDiffFollowsARename(t *testing.T) {
	repo := newTestRepo(t)
	repo.write("old.txt", "content long enough to be detected as a rename\n")
	repo.commit("initial")
	repo.git("mv", "old.txt", "new.txt")
	repo.commit("rename it")

	head := strings.TrimSpace(repo.git("rev-parse", "HEAD"))
	oldPath := "old.txt"

	diffs, err := git.CommitFileDiff(ctx(t), repo.Path, head, "new.txt", &oldPath)
	require.NoError(t, err)

	require.Len(t, diffs, 1)
	assert.Equal(t, "renamed", diffs[0].Status)
}

func TestAnEmptyDiffIsAnArrayNotNull(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()

	for name, load := range map[string]func() ([]git.FileDiff, error){
		"working": func() ([]git.FileDiff, error) { return git.WorkingDiff(ctx(t), repo.Path) },
		"staged":  func() ([]git.FileDiff, error) { return git.StagedDiff(ctx(t), repo.Path) },
	} {
		t.Run(name, func(t *testing.T) {
			diffs, err := load()
			require.NoError(t, err)
			assert.Empty(t, diffs)
			assert.NotNil(t, diffs)
		})
	}
}
