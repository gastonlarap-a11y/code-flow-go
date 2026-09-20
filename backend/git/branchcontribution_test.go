package git_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/git"
)

// `GIT-039`: a branch's whole contribution is one comparison, not two diffs added together.

func contributionRepo(t *testing.T) string {
	t.Helper()
	path := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		command := exec.CommandContext(t.Context(), "git", append([]string{"-C", path}, args...)...)
		command.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := command.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	write := func(name, content string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(path, name), []byte(content), 0o600))
	}

	run("init", "-b", "main")
	write("shared.go", "package main\n\nvar Shared = 1\n")
	run("add", ".")
	run("commit", "-m", "base")

	run("checkout", "-b", "feature")
	write("shared.go", "package main\n\nvar Shared = 2\n")
	write("added.go", "package main\n\nvar Added = true\n")
	run("add", ".")
	run("commit", "-m", "on the branch")

	// The base moves on after the branch left it. What that commit changed is not this branch's
	// contribution and must not appear.
	run("checkout", "main")
	write("elsewhere.go", "package main\n\nvar Elsewhere = 1\n")
	run("add", ".")
	run("commit", "-m", "meanwhile on main")

	run("checkout", "feature")
	// And the same file again, uncommitted.
	write("shared.go", "package main\n\nvar Shared = 3\n")
	return path
}

// The obvious implementation — the branch diff concatenated with the working diff — is wrong: a file
// touched in a commit of the branch *and* again uncommitted appears twice, and a model handed the
// same file twice reports the same finding twice.
func TestBranchContributionCountsATwiceTouchedFileOnce(t *testing.T) {
	repo := contributionRepo(t)

	files, err := git.BranchContribution(t.Context(), repo, "main")
	require.NoError(t, err)

	paths := make([]string, 0, len(files))
	for _, file := range files {
		require.NotNil(t, file.NewPath)
		paths = append(paths, *file.NewPath)
	}
	assert.ElementsMatch(t, []string{"shared.go", "added.go"}, paths)
}

func TestBranchContributionIsAgainstTheMergeBaseNotTheOtherTip(t *testing.T) {
	repo := contributionRepo(t)

	files, err := git.BranchContribution(t.Context(), repo, "main")
	require.NoError(t, err)

	for _, file := range files {
		require.NotNil(t, file.NewPath)
		assert.NotEqual(t, "elsewhere.go", *file.NewPath,
			"what the base branch did meanwhile is not this branch's contribution")
	}
}

// The uncommitted state is what is compared, so the latest edit is the one the review judges.
func TestBranchContributionShowsTheWorkingTreeNotTheLastCommit(t *testing.T) {
	repo := contributionRepo(t)

	files, err := git.BranchContribution(t.Context(), repo, "main")
	require.NoError(t, err)

	var shared *git.FileDiff
	for i := range files {
		if files[i].NewPath != nil && *files[i].NewPath == "shared.go" {
			shared = &files[i]
		}
	}
	require.NotNil(t, shared)

	rendered := git.RenderForPrompt([]git.FileDiff{*shared}, git.PromptBudgetChars)
	assert.Contains(t, rendered, "Shared = 3", "the uncommitted value")
	assert.NotContains(t, rendered, "Shared = 2", "not the one the branch committed")
}

// `DiffTargets.WorkingDirectory` implied `IncludeUntracked`, so a file the branch adds and has not
// staged was always part of this diff. git's own `diff` leaves it out.
func TestBranchContributionIncludesAnUntrackedFile(t *testing.T) {
	repo := contributionRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "nuevo.go"),
		[]byte("package main\n\nvar Nuevo = 1\n"), 0o600))

	files, err := git.BranchContribution(t.Context(), repo, "main")
	require.NoError(t, err)

	found := false
	for _, file := range files {
		if file.NewPath != nil && *file.NewPath == "nuevo.go" {
			found = true
			assert.Equal(t, "added", file.Status)
		}
	}
	assert.True(t, found, "an unstaged new file is part of what the branch contributes")
}

// Both failures are reported rather than degraded: each would otherwise produce a diff that reads as
// "this branch changed everything", which is the most misleading answer a review could be given.
func TestBranchContributionReportsWhatItCannotCompare(t *testing.T) {
	t.Run("a base branch that does not resolve", func(t *testing.T) {
		repo := contributionRepo(t)

		_, err := git.BranchContribution(t.Context(), repo, "no-such-branch")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no-such-branch")
	})

	t.Run("a repository with no commits", func(t *testing.T) {
		empty := t.TempDir()
		command := exec.CommandContext(t.Context(), "git", "-C", empty, "init", "-b", "main")
		command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		require.NoError(t, command.Run())

		_, err := git.BranchContribution(t.Context(), empty, "main")
		require.Error(t, err)
		assert.True(t,
			strings.Contains(err.Error(), "no commits") || strings.Contains(err.Error(), "main"),
			"the message names what is missing: %s", err)
	})
}
