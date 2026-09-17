package files_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge/jsonwire"
	"github.com/gastonlarap-a11y/code-flow/backend/files"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// searchRepo builds a real git repository, because the walk asks git what is ignored and the whole
// point of the pruning rule is that git's answer is the one that counts.
func searchRepo(t *testing.T, tree map[string]string) string {
	t.Helper()
	dir := t.TempDir()

	cmd := exec.CommandContext(t.Context(), "git", "init", "--quiet", "--initial-branch=main", dir) //nolint:gosec
	cmd.Env = append(os.Environ(),
		"HOME="+dir,
		"GIT_CONFIG_GLOBAL="+filepath.Join(dir, "gitconfig"),
		"GIT_CONFIG_NOSYSTEM=1",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	for rel, content := range tree {
		write(t, dir, rel, content)
	}
	return dir
}

// theFixture is the tree the first four search vectors share.
func theFixture(t *testing.T) string {
	t.Helper()
	return searchRepo(t, map[string]string{
		"src/app.ts":                "const answer = 42;\nexport { answer };\n",
		"src/util.ts":               "// the ANSWER helper\n",
		"node_modules/pkg/index.js": "const answer = 1;\n",
		"debug.log":                 "answer\n",
		".gitignore":                "node_modules/\n*.log\n",
	})
}

func defaults() files.SearchOptions { return files.SearchOptions{} }

// hitRefs renders hits as "path:line", which is how the vectors write them.
func hitRefs(outcome files.SearchOutcome) []string {
	out := make([]string, 0, len(outcome.Hits))
	for _, hit := range outcome.Hits {
		out = append(out, hit.Path+":"+strconv.FormatInt(hit.LineNo, 10))
	}
	return out
}

func search(t *testing.T, repo, query string, options files.SearchOptions, max int64) files.SearchOutcome {
	t.Helper()
	outcome, err := files.SearchRepo(t.Context(), repo, query, options, max)
	require.NoError(t, err)
	return outcome
}

// ---- the search vectors ---------------------------------------------------------------------------

// search.vectors.json#lists-and-prunes-gitignored
func TestListsAndPrunesGitignored(t *testing.T) {
	repo := theFixture(t)

	listed, err := files.ListRepoFiles(t.Context(), repo)

	require.NoError(t, err)
	assert.Contains(t, listed, "src/app.ts")
	assert.Contains(t, listed, ".gitignore")
	assert.NotContains(t, listed, "debug.log")
	for _, path := range listed {
		assert.False(t, strings.HasPrefix(path, "node_modules"),
			"the directory is pruned during the walk, never descended into")
	}
	require.NoError(t, jsonwire.AssertNoNilSlices(listed))
}

// search.vectors.json#case-insensitive-default
func TestSearchIsCaseInsensitiveByDefault(t *testing.T) {
	repo := theFixture(t)

	outcome := search(t, repo, "answer", defaults(), 100)

	// The distinct files, not one entry per hit: src/app.ts mentions it on both of its lines.
	seen := map[string]bool{}
	paths := make([]string, 0, 2)
	for _, hit := range outcome.Hits {
		if !seen[hit.Path] {
			seen[hit.Path] = true
			paths = append(paths, hit.Path)
		}
	}
	sort.Strings(paths)
	assert.Equal(t, []string{"src/app.ts", "src/util.ts"}, paths)
	assert.NotContains(t, paths, "debug.log", "gitignored, so it is never read")
	assert.EqualValues(t, 1, outcome.Hits[0].LineNo)
}

// search.vectors.json#case-sensitive-respects-case
func TestSearchCaseSensitive(t *testing.T) {
	repo := theFixture(t)

	outcome := search(t, repo, "ANSWER", files.SearchOptions{CaseSensitive: true}, 100)

	require.Len(t, outcome.Hits, 1)
	assert.Equal(t, "src/util.ts", outcome.Hits[0].Path)
}

// search.vectors.json#truncation-flag
func TestTruncationIsFlaggedNotSilent(t *testing.T) {
	repo := theFixture(t)

	outcome := search(t, repo, "answer", defaults(), 1)

	assert.Len(t, outcome.Hits, 1)
	assert.True(t, outcome.Truncated, "a partial list with no signal is the alternative")
}

// search.vectors.json#whole-word-boundary
func TestWholeWordBoundary(t *testing.T) {
	repo := searchRepo(t, map[string]string{"a.ts": "const set = 1;\nconst offset = 2;\n"})

	assert.Equal(t, []string{"a.ts:1", "a.ts:2"}, hitRefs(search(t, repo, "set", defaults(), 50)))
	assert.Equal(t, []string{"a.ts:1"},
		hitRefs(search(t, repo, "set", files.SearchOptions{WholeWord: true}, 50)))
}

// search.vectors.json#regex-mode-vs-literal
func TestRegexModeVersusLiteral(t *testing.T) {
	repo := searchRepo(t, map[string]string{"a.ts": "fn one() {}\nfn two() {}\nconst three = 3;\n"})
	const pattern = `fn \w+\(`

	assert.Equal(t, []string{"a.ts:1", "a.ts:2"},
		hitRefs(search(t, repo, pattern, files.SearchOptions{Regex: true}, 50)))

	assert.Empty(t, search(t, repo, pattern, defaults(), 50).Hits,
		"escaped and searched for literally, and no line contains those backslashes")
}

// search.vectors.json#invalid-regex-error
func TestInvalidRegex(t *testing.T) {
	repo := searchRepo(t, map[string]string{"a.ts": "x\n"})

	_, err := files.SearchRepo(t.Context(), repo, "foo(", files.SearchOptions{Regex: true}, 50)

	require.Error(t, err)
	// Only the prefix is the contract; the suffix is whatever the engine said, reduced to one line.
	assert.True(t, strings.HasPrefix(err.Error(), "invalid regular expression"), err.Error())
	assert.NotContains(t, err.Error(), "\n", "one line, so it fits the find box")
}

// search.vectors.json#include-exclude-globs
func TestIncludeAndExcludeGlobs(t *testing.T) {
	repo := searchRepo(t, map[string]string{
		"src/a.ts":      "needle\n",
		"src/a.test.ts": "needle\n",
		"docs/a.md":     "needle\n",
	})

	t.Run("a bare pattern matches by name at any depth", func(t *testing.T) {
		outcome := search(t, repo, "needle", files.SearchOptions{Include: "*.ts"}, 50)
		assert.Equal(t, []string{"src/a.test.ts:1", "src/a.ts:1"}, hitRefs(outcome))
	})

	t.Run("exclude is applied after include and can only remove", func(t *testing.T) {
		outcome := search(t, repo, "needle",
			files.SearchOptions{Include: "*.ts", Exclude: "*.test.ts"}, 50)
		assert.Equal(t, []string{"src/a.ts:1"}, hitRefs(outcome))
	})

	t.Run("a pattern with a slash matches the whole path", func(t *testing.T) {
		outcome := search(t, repo, "needle", files.SearchOptions{Include: "docs/**"}, 50)
		assert.Equal(t, []string{"docs/a.md:1"}, hitRefs(outcome))
	})
}

// search.vectors.json#replace-and-checkpoint-undo
func TestReplaceAcrossTheRepositoryAndUndoIt(t *testing.T) {
	repo := searchRepo(t, map[string]string{
		"src/a.ts": "const oldName = 1;\nuse(oldName);\n",
		"src/b.ts": "nothing here\n",
	})
	checkpointer := &recordingCheckpointer{id: "1700000000-abcdef12"}

	outcome, err := files.ReplaceInRepo(t.Context(), checkpointer, repo,
		"oldName", "newName", defaults(), nil)

	require.NoError(t, err)
	assert.EqualValues(t, 2, outcome.Replacements)
	assert.EqualValues(t, 1, outcome.Files, "src/b.ts has no match, so it is never touched")
	require.NotNil(t, outcome.CheckpointID)
	assert.Equal(t, "1700000000-abcdef12", *outcome.CheckpointID)

	content, err := os.ReadFile(filepath.Join(repo, "src", "a.ts"))
	require.NoError(t, err)
	assert.Equal(t, "const newName = 1;\nuse(newName);\n", string(content))

	assert.Equal(t, "replace-all", checkpointer.kind, "the stable action key the modal translates")
	assert.Equal(t, "const oldName = 1;\nuse(oldName);\n", checkpointer.sawOnDisk["src/a.ts"],
		"the snapshot has to see the state before the first write, or undo restores the replacement")
}

// search.vectors.json#replace-scoped-with-capture-groups
func TestReplaceScopedWithCaptureGroups(t *testing.T) {
	repo := searchRepo(t, map[string]string{"a.ts": "call(1, 2);\n", "b.ts": "call(3, 4);\n"})
	only := "a.ts"

	outcome, err := files.ReplaceInRepo(t.Context(), nil, repo,
		`call\((\d+), (\d+)\)`, "call($2, $1)", files.SearchOptions{Regex: true}, &only)

	require.NoError(t, err)
	assert.EqualValues(t, 1, outcome.Files)

	a, err := os.ReadFile(filepath.Join(repo, "a.ts"))
	require.NoError(t, err)
	assert.Equal(t, "call(2, 1);\n", string(a))

	b, err := os.ReadFile(filepath.Join(repo, "b.ts"))
	require.NoError(t, err)
	assert.Equal(t, "call(3, 4);\n", string(b), "never even read")
}

// ---- the rest -------------------------------------------------------------------------------------

// recordingCheckpointer stands in for the git snapshot.
//
// It reads the tree at the moment it is called, so a test can assert the snapshot saw the state
// *before* the writes — which is the property the whole plan-then-write ordering exists for, and
// the one a fake that only counted calls would not catch.
type recordingCheckpointer struct {
	id   string
	err  error
	kind string
	repo string
	// sawOnDisk is every file's content at checkpoint time, keyed by repo-relative path.
	sawOnDisk map[string]string
}

func (c *recordingCheckpointer) CreateCheckpoint(_ context.Context, repo, kind string) (string, error) {
	c.repo, c.kind = repo, kind
	c.sawOnDisk = map[string]string{}

	_ = filepath.WalkDir(repo, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil //nolint:nilerr // a tree it cannot read is not this fake's problem
		}
		rel, relErr := filepath.Rel(repo, path)
		if relErr != nil {
			return nil
		}
		if content, readErr := os.ReadFile(path); readErr == nil { //nolint:gosec // a test tree
			c.sawOnDisk[filepath.ToSlash(rel)] = string(content)
		}
		return nil
	})
	return c.id, c.err
}

// A replace with no undo is still a replace: the snapshot failing must not refuse what the user
// asked for. They find out through a null checkpoint id.
func TestAFailedCheckpointDoesNotStopTheReplace(t *testing.T) {
	repo := searchRepo(t, map[string]string{"a.ts": "old\n"})
	checkpointer := &recordingCheckpointer{err: errors.New("no repository here")}

	outcome, err := files.ReplaceInRepo(t.Context(), checkpointer, repo, "old", "new", defaults(), nil)

	require.NoError(t, err)
	assert.Nil(t, outcome.CheckpointID, "the one way the caller learns there is nothing to undo")
	content, err := os.ReadFile(filepath.Join(repo, "a.ts"))
	require.NoError(t, err)
	assert.Equal(t, "new\n", string(content))
}

func TestAnEmptyQueryIsANoOpNotAnError(t *testing.T) {
	repo := searchRepo(t, map[string]string{"a.ts": "content\n"})
	checkpointer := &recordingCheckpointer{id: "unused"}

	outcome, err := files.ReplaceInRepo(t.Context(), checkpointer, repo, "   ", "x", defaults(), nil)

	require.NoError(t, err)
	assert.Zero(t, outcome.Replacements)
	assert.Nil(t, outcome.CheckpointID)
	assert.Empty(t, checkpointer.kind, "nothing to protect, so nothing was snapshotted")
}

// Nothing matched is the same shape as nothing asked: no checkpoint for a replace that changes
// nothing, or the restore-points list fills with entries that restore nothing.
func TestAReplaceThatMatchesNothingTakesNoCheckpoint(t *testing.T) {
	repo := searchRepo(t, map[string]string{"a.ts": "content\n"})
	checkpointer := &recordingCheckpointer{id: "unused"}

	outcome, err := files.ReplaceInRepo(t.Context(), checkpointer, repo, "absent", "x", defaults(), nil)

	require.NoError(t, err)
	assert.Zero(t, outcome.Files)
	assert.Empty(t, checkpointer.kind)
}

// A binary file is skipped by grep's own heuristic: a NUL byte in the first 8 KiB.
func TestBinaryFilesAreSkipped(t *testing.T) {
	repo := searchRepo(t, nil)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "blob.bin"),
		[]byte("needle\x00needle"), 0o600))
	write(t, repo, "a.ts", "needle\n")

	outcome := search(t, repo, "needle", defaults(), 50)

	assert.Equal(t, []string{"a.ts:1"}, hitRefs(outcome))
}

func TestAFileOverTheSizeCapIsSkipped(t *testing.T) {
	repo := searchRepo(t, nil)
	write(t, repo, "big.txt", strings.Repeat("a", 1<<20)+"\nneedle\n")
	write(t, repo, "small.txt", "needle\n")

	outcome := search(t, repo, "needle", defaults(), 50)

	assert.Equal(t, []string{"small.txt:1"}, hitRefs(outcome))
}

// The line is still a hit — only its displayed text is cut, and cut by characters so a multi-byte
// one is never split in half.
func TestALongLineIsTruncatedForDisplayOnly(t *testing.T) {
	repo := searchRepo(t, nil)
	write(t, repo, "wide.txt", strings.Repeat("á", 500)+"needle\n")

	outcome := search(t, repo, "needle", defaults(), 50)

	require.Len(t, outcome.Hits, 1)
	line := outcome.Hits[0].Line
	assert.Equal(t, 401, len([]rune(line)), "400 characters plus the ellipsis")
	assert.True(t, strings.HasSuffix(line, "…"))
	assert.True(t, utf8.ValidString(line), "cut by characters, never by bytes")
}

// One generated file must not fill the whole result list.
func TestAtMostTwentyHitsPerFile(t *testing.T) {
	repo := searchRepo(t, nil)
	write(t, repo, "many.txt", strings.Repeat("needle\n", 50))
	write(t, repo, "other.txt", "needle\n")

	outcome := search(t, repo, "needle", defaults(), 500)

	perFile := map[string]int{}
	for _, hit := range outcome.Hits {
		perFile[hit.Path]++
	}
	assert.Equal(t, 20, perFile["many.txt"])
	assert.Equal(t, 1, perFile["other.txt"], "the other file still gets its turn")
}

func TestAnEmptySearchResultIsAnArray(t *testing.T) {
	repo := searchRepo(t, map[string]string{"a.ts": "content\n"})

	outcome := search(t, repo, "absent", defaults(), 50)

	assert.Empty(t, outcome.Hits)
	assert.NotNil(t, outcome.Hits)
	require.NoError(t, jsonwire.AssertNoNilSlices(outcome))
}
