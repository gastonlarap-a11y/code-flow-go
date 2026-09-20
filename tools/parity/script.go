package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// The scripted request list, and the fixture it reads.
//
// Read-mostly, as §9.7 prescribes, with the few writes that exist only so the reads have something
// to return. Nothing here reaches the network or the credential store: a parity run must be
// repeatable on a machine with no tokens, no GitHub and no Azure DevOps, and a command whose answer
// depends on an account is not a command two implementations can be compared on.

// step is one request, run against both cores.
type step struct {
	// name is what the report calls it. Distinct from method because the same command appears more
	// than once with different arguments.
	name   string
	method string
	params map[string]any

	// capture binds a field of this step's result to a placeholder later steps can use. Ids are
	// minted per side, so `{{workspace}}` resolves to a different uuid on each — which is exactly
	// why the comparison normalises them away afterwards.
	capture map[string]string

	// differs marks a step whose answer *must* differ, with the reason. The report prints both
	// values and does not count it as a failure; an entry here is a claim that has to be argued,
	// not a way to silence a difference.
	differs string
}

// placeholder is the syntax a step uses to reach a captured value: "{{workspace}}".
func placeholder(name string) string { return "{{" + name + "}}" }

// The differences this oracle found, argued rather than silenced.
//
// Each names the marker in `docs/` that records it. An entry here is a claim that the difference
// was investigated and is intended; removing one and re-running is how that claim is re-checked.
const (
	divergenceAddedOldPath = "DIVERGENCE-GIT-e — an addition's old_path is null here and the file's " +
		"own path in 2.7.1; every renderer consumer reads `new_path ?? old_path`, so neither is visible"

	divergenceSubdirectoryRoot = "DIVERGENCE-GIT-f — a path inside a repository resolves to that " +
		"repository here and is refused by 2.7.1, which its own is_git_repo already contradicted"

	missingDirectoryWording = "both refuse and name the path; the sentence is not a contract " +
		"(13-cross-language-contracts.md lists the parsed strings and this is not one) and 3.0.0's " +
		"says what to do next — see backend/git/runner.go, startFailure"
)

// scriptFor builds the request list against a fixture repository both cores read.
//
// The same repository for both sides, deliberately: every commit hash, file mode and diff hunk is
// then identical by construction, so a difference in `get_status` is a difference in how the two
// implementations read a repository rather than in which repository they read.
func scriptFor(repo string) []step {
	searchOptions := map[string]any{
		"caseSensitive": false, "wholeWord": false, "regex": false,
		"include": "", "exclude": "",
	}

	return []step{
		// ---- the dispatcher itself ----------------------------------------------------------
		{
			name:   "an unknown command",
			method: "definitely_not_a_command",
		},
		{
			// An empty object, not an absent one. `lib/bridge/host.ts` sends `params ?? {}`, so
			// every one of the 246 wrappers puts an object on the wire even when it has nothing to
			// put in it — a request with no `params` member at all is a shape the renderer cannot
			// produce, and comparing the two cores on it measures neither.
			name:   "a missing required parameter",
			method: "get_status",
			params: map[string]any{},
		},
		{
			name:    "the running version",
			method:  "update_current_version",
			differs: "2.7.1 against 3.0.0 is the upgrade being measured, not a defect",
		},

		// ---- the built-in templates, which depend on nothing -------------------------------
		{name: "the commit template", method: "default_commit_template"},
		{name: "the review template", method: "default_review_template"},
		{name: "the analyze template", method: "default_analyze_template"},
		{name: "the PR description template", method: "default_pr_description_template"},
		{name: "the conflict template", method: "default_resolve_conflict_template"},

		// ---- an empty install ----------------------------------------------------------------
		{name: "workspaces, before any exist", method: "list_workspaces"},
		{name: "a setting that was never written", method: "get_setting", params: map[string]any{"key": "ai_provider"}},

		// ---- creating the little state the reads need ---------------------------------------
		{
			name:    "creating a workspace",
			method:  "create_workspace",
			params:  map[string]any{"name": "Parity", "icon": "folder", "color": "#4f46e5"},
			capture: map[string]string{"workspace": "id"},
		},
		{name: "workspaces, with one", method: "list_workspaces"},
		{
			name:   "projects, before any exist",
			method: "list_projects",
			params: map[string]any{"workspaceId": placeholder("workspace")},
		},
		{
			name:   "creating a project on the fixture repository",
			method: "create_project",
			params: map[string]any{"input": map[string]any{
				"workspace_id": placeholder("workspace"),
				"name":         "fixture",
				"local_path":   repo,
				"remote_url":   nil,
				"color":        "#0ea5e9",
				"icon":         "git-branch",
				"ado_org":      nil, "ado_project": nil, "ado_repo_id": nil,
				"github_owner": nil, "github_repo": nil, "github_host": nil,
			}},
			capture: map[string]string{"project": "id"},
		},
		{
			name:   "projects, with one",
			method: "list_projects",
			params: map[string]any{"workspaceId": placeholder("workspace")},
		},

		// ---- the workspace-scoped reads -------------------------------------------------------
		{
			name:   "the workspace's agents",
			method: "list_workspace_agents",
			params: map[string]any{"workspaceId": placeholder("workspace")},
		},
		{
			name:   "the workspace's MCP servers",
			method: "list_workspace_mcps",
			params: map[string]any{"workspaceId": placeholder("workspace")},
		},
		{
			name:   "the workspace's skills",
			method: "list_workspace_skills",
			params: map[string]any{"workspaceId": placeholder("workspace")},
		},
		{
			name:   "the workspace's review contexts",
			method: "list_review_contexts",
			params: map[string]any{"workspaceId": placeholder("workspace")},
		},
		{
			name:   "the workspace's review prompt",
			method: "get_workspace_prompt",
			params: map[string]any{"workspaceId": placeholder("workspace"), "kind": "review"},
		},
		{
			name:   "the workspace's review runs",
			method: "list_review_runs",
			params: map[string]any{"workspaceId": placeholder("workspace")},
		},
		{
			name:   "the project's chat conversations",
			method: "list_chat_conversations",
			params: map[string]any{"projectId": placeholder("project"), "search": nil},
		},
		{
			name:   "the project's job history",
			method: "list_job_history",
			params: map[string]any{"projectId": placeholder("project")},
		},

		// ---- git, against the fixture ---------------------------------------------------------
		{name: "the repository's status", method: "get_status", params: map[string]any{"repoPath": repo}},
		{name: "the repository's branches", method: "list_branches", params: map[string]any{"repoPath": repo}},
		{
			name:    "the working diff",
			method:  "get_working_diff",
			params:  map[string]any{"repoPath": repo},
			differs: divergenceAddedOldPath,
		},
		{
			name:    "the staged diff",
			method:  "get_staged_diff",
			params:  map[string]any{"repoPath": repo},
			differs: divergenceAddedOldPath,
		},
		{
			name:    "the commit list",
			method:  "list_commits",
			params:  map[string]any{"repoPath": repo, "allRefs": true, "limit": 20},
			capture: map[string]string{"commit": "id"},
		},
		{
			name:    "the first commit's files",
			method:  "list_commit_files",
			params:  map[string]any{"repoPath": repo, "oid": placeholder("commit")},
			differs: divergenceAddedOldPath,
		},
		{
			name:    "the first commit's diff",
			method:  "get_commit_diff",
			params:  map[string]any{"repoPath": repo, "oid": placeholder("commit")},
			differs: divergenceAddedOldPath,
		},
		{
			name:    "a status read of a path that is not there",
			method:  "get_status",
			params:  map[string]any{"repoPath": filepath.Join(repo, "moved-away")},
			differs: missingDirectoryWording,
		},
		{
			// A directory that exists and holds no repository — the other half of the same
			// question, and the one a user reaches by picking the wrong folder rather than by
			// moving the right one.
			name:    "a status read of a directory that is not a repository",
			method:  "get_status",
			params:  map[string]any{"repoPath": filepath.Join(repo, "ignored")},
			differs: divergenceSubdirectoryRoot,
		},
		{
			name:   "is this a repository",
			method: "is_git_repo",
			params: map[string]any{"path": repo},
		},
		{
			name:   "is a plain directory a repository",
			method: "is_git_repo",
			params: map[string]any{"path": filepath.Join(repo, "ignored")},
		},

		// ---- search and the secret gate ---------------------------------------------------------
		{
			name:   "searching the repository",
			method: "search_repo",
			params: map[string]any{
				"repoPath": repo, "query": "parity", "options": searchOptions, "maxResults": 500,
			},
		},
		{
			name:   "a search that matches nothing",
			method: "search_repo",
			params: map[string]any{
				"repoPath": repo, "query": "nothing-here-matches-this", "options": searchOptions, "maxResults": 500,
			},
		},
		{
			name:   "scanning what is staged",
			method: "scan_staged_secrets",
			params: map[string]any{"repoPath": repo},
		},

		// ---- the API workbench's tree ------------------------------------------------------------
		{
			name:   "the API tree of an empty workspace",
			method: "api_load_tree",
			params: map[string]any{"workspaceId": placeholder("workspace")},
		},
	}
}

// buildFixtureRepo creates the repository both cores read.
//
// A committed history, an unstaged edit, an untracked file and a second branch: between them they
// exercise every branch of a status read, which is the command most likely to differ between a
// libgit2 implementation and one that shells out to git.
func buildFixtureRepo(ctx context.Context, dir string) error {
	// Owner-only throughout. The fixture lives in a temp directory for the length of one run and
	// nothing else needs to read it — including `staged.env`, which holds a key-shaped literal.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create the fixture directory: %w", err)
	}

	write := func(name, content string) error {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(content), 0o600)
	}

	git := func(args ...string) error {
		// gosec G204: `git` with a literal argument list, in a directory this function just made.
		cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec
		cmd.Dir = dir
		// A fixed identity and no global config, so the repository is byte-identical on any
		// machine and nothing is read from the developer's own git configuration.
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=Parity", "GIT_AUTHOR_EMAIL=parity@local",
			"GIT_COMMITTER_NAME=Parity", "GIT_COMMITTER_EMAIL=parity@local",
			"GIT_AUTHOR_DATE=2026-01-01T00:00:00+00:00",
			"GIT_COMMITTER_DATE=2026-01-01T00:00:00+00:00",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %w: %s", args, err, out)
		}
		return nil
	}

	steps := []func() error{
		func() error { return git("init", "--initial-branch=main") },
		func() error { return write("README.md", "# parity fixture\n\nA repository two cores read.\n") },
		func() error {
			return write("src/app.go", "package app\n\n// parity: the word the search step looks for.\nfunc Run() {}\n")
		},
		func() error { return write(".gitignore", "ignored/\n") },
		func() error { return git("add", ".") },
		func() error { return git("commit", "-m", "the first commit") },
		func() error { return git("branch", "feature/second") },

		// An unstaged edit and an untracked file: the two states a status read separates.
		func() error { return write("README.md", "# parity fixture\n\nEdited, not staged.\n") },
		func() error { return write("untracked.txt", "not added\n") },

		// An ignored directory, which is also the script's "a directory that is not a repository":
		// it exists, it holds no `.git`, and it is inside a repository — the shape a user reaches
		// by picking one folder too deep.
		func() error { return write("ignored/note.txt", "not reported by status\n") },

		// Something staged, so the secret gate has an index to scan. The literal is AWS's own
		// documented example key — it is not a credential, and it is the shape a scanner looks for.
		func() error { return write("staged.env", "AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE\n") },
		func() error { return git("add", "staged.env") },
	}
	for _, run := range steps {
		if err := run(); err != nil {
			return err
		}
	}
	return nil
}

// resolve substitutes captured values into a step's parameters.
func resolve(params map[string]any, captured map[string]string) map[string]any {
	if params == nil {
		return nil
	}

	out := make(map[string]any, len(params))
	for key, value := range params {
		out[key] = resolveValue(value, captured)
	}
	return out
}

func resolveValue(value any, captured map[string]string) any {
	switch typed := value.(type) {
	case string:
		for name, actual := range captured {
			if typed == placeholder(name) {
				return actual
			}
		}
		return typed
	case map[string]any:
		return resolve(typed, captured)
	case []any:
		out := make([]any, 0, len(typed))
		for _, inner := range typed {
			out = append(out, resolveValue(inner, captured))
		}
		return out
	default:
		return value
	}
}
