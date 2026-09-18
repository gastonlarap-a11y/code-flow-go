package ai_test

import (
	"context"
	"testing"

	"github.com/gastonlarap-a11y/code-flow/backend/ai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// settings is a stand-in for the store, holding exactly what a real settings table would.
//
// A missing key and a key holding an empty string are **different** here, and keeping them
// distinct is the whole point of the fake: the tool-list rule is the one place the difference is
// observable, and a map that conflated them would let that rule break silently.
type settings map[string]string

func (s settings) GetSetting(_ context.Context, key string) (*string, error) {
	value, found := s[key]
	if !found {
		return nil, nil
	}
	return &value, nil
}

func resolve(t *testing.T, rows settings, task ai.Task) ai.Config {
	t.Helper()
	return ai.NewRouter(rows).Resolve(t.Context(), task)
}

// The ten keys are the `{task}` in `ai_provider_{task}` and `{provider}_{task}_model`. A typo here
// orphans a user's saved setting silently — their override simply stops applying.
func TestTheTenTaskKeysAreVerbatim(t *testing.T) {
	keys := make([]string, 0, len(ai.AllTasks))
	for _, task := range ai.AllTasks {
		keys = append(keys, string(task))
	}

	assert.Equal(t, []string{
		"commit", "analyze", "review", "pr_description", "chat",
		"fix", "conflict", "inline", "ticket_review", "dbml",
	}, keys)
}

func TestTheProviderCascade(t *testing.T) {
	t.Run("nothing configured is Claude", func(t *testing.T) {
		assert.Equal(t, "claude", resolve(t, settings{}, ai.TaskChat).Provider)
	})

	t.Run("the global provider applies to every task", func(t *testing.T) {
		rows := settings{"ai_provider": "codex"}
		assert.Equal(t, "codex", resolve(t, rows, ai.TaskChat).Provider)
		assert.Equal(t, "codex", resolve(t, rows, ai.TaskCommit).Provider)
	})

	t.Run("a per-task override wins", func(t *testing.T) {
		rows := settings{"ai_provider": "codex", "ai_provider_commit": "ollama"}
		assert.Equal(t, "ollama", resolve(t, rows, ai.TaskCommit).Provider)
		assert.Equal(t, "codex", resolve(t, rows, ai.TaskChat).Provider, "and only that task")
	})

	// Clearing the field in the UI writes an empty string, and the user means "inherit" by it.
	t.Run("a blank override counts as unset", func(t *testing.T) {
		rows := settings{"ai_provider": "codex", "ai_provider_commit": "   "}
		assert.Equal(t, "codex", resolve(t, rows, ai.TaskCommit).Provider)
	})

	t.Run("an unrecognised provider still runs, as Claude", func(t *testing.T) {
		config := resolve(t, settings{"ai_provider": "an-engine-we-dropped"}, ai.TaskChat)
		assert.Equal(t, "an-engine-we-dropped", config.Provider, "the id is kept for its settings")
		assert.Equal(t, "claude", config.Binary, "but it resolves to Claude's engine")
	})
}

func TestTheModelCascade(t *testing.T) {
	t.Run("a per-task model wins", func(t *testing.T) {
		rows := settings{"claude_model": "base", "claude_chat_model": "for-chat"}
		assert.Equal(t, "for-chat", resolve(t, rows, ai.TaskChat).Model)
	})

	t.Run("the base model is the final fallback", func(t *testing.T) {
		assert.Equal(t, "base", resolve(t, settings{"claude_model": "base"}, ai.TaskChat).Model)
	})

	// The middle step, and it applies to `commit` only: drafting a commit message on the base
	// model costs time and money for a job that does not need either.
	t.Run("commit falls back to the engine's fast model, not the base one", func(t *testing.T) {
		rows := settings{"claude_model": "base"}
		assert.Equal(t, "claude-haiku-4-5-20251001", resolve(t, rows, ai.TaskCommit).Model)
		assert.Equal(t, "base", resolve(t, rows, ai.TaskChat).Model, "and only for commit")
	})

	t.Run("an explicit commit model still wins over the fast one", func(t *testing.T) {
		rows := settings{"claude_model": "base", "claude_commit_model": "chosen"}
		assert.Equal(t, "chosen", resolve(t, rows, ai.TaskCommit).Model)
	})

	// Only Claude names one, so for every other engine commit falls straight through.
	t.Run("an engine with no fast model falls through to the base one", func(t *testing.T) {
		rows := settings{"ai_provider": "codex", "codex_model": "base"}
		assert.Equal(t, "base", resolve(t, rows, ai.TaskCommit).Model)
	})
}

func TestTheBinaryCascade(t *testing.T) {
	t.Run("the engine's default", func(t *testing.T) {
		assert.Equal(t, "claude", resolve(t, settings{}, ai.TaskChat).Binary)
		assert.Equal(t, "agy", resolve(t, settings{"ai_provider": "gemini"}, ai.TaskChat).Binary,
			"the provider is called gemini and the binary is agy — deliberate, not a typo")
	})

	t.Run("a manual path bypasses everything", func(t *testing.T) {
		rows := settings{"claude_binary_path": "/opt/custom/claude"}
		assert.Equal(t, "/opt/custom/claude", resolve(t, rows, ai.TaskChat).Binary)
	})

	t.Run("a blank path counts as unset", func(t *testing.T) {
		rows := settings{"claude_binary_path": "  "}
		assert.Equal(t, "claude", resolve(t, rows, ai.TaskChat).Binary)
	})

	// An HTTP engine's "binary" is its endpoint, which is why every operation branches on the
	// transport before going near binary resolution.
	t.Run("an HTTP engine's default is an endpoint", func(t *testing.T) {
		assert.Equal(t, "http://localhost:11434",
			resolve(t, settings{"ai_provider": "ollama"}, ai.TaskChat).Binary)
		assert.Equal(t, "https://api.openai.com/v1",
			resolve(t, settings{"ai_provider": "openai"}, ai.TaskChat).Binary)
	})
}

// The one place where unset and blank differ, and the difference is a decision the user made.
func TestTheToolListDistinguishesUnsetFromBlank(t *testing.T) {
	t.Run("unset on a judging task falls back to read-only tools", func(t *testing.T) {
		for _, task := range []ai.Task{ai.TaskAnalyze, ai.TaskReview, ai.TaskTicketReview} {
			t.Run(string(task), func(t *testing.T) {
				assert.Equal(t, []string{"Read", "Grep", "Glob"}, resolve(t, settings{}, task).AllowedTools)
			})
		}
	})

	t.Run("unset on any other task means no restriction", func(t *testing.T) {
		for _, task := range []ai.Task{ai.TaskChat, ai.TaskFix, ai.TaskCommit, ai.TaskDBML} {
			t.Run(string(task), func(t *testing.T) {
				assert.Empty(t, resolve(t, settings{}, task).AllowedTools)
			})
		}
	})

	// Clearing every checkbox is an answer. Overruling it with a default would take a decision
	// away from the person who made it.
	t.Run("blank means no tools, even on a judging task", func(t *testing.T) {
		rows := settings{"claude_allowed_tools": ""}
		assert.Empty(t, resolve(t, rows, ai.TaskReview).AllowedTools)
	})

	t.Run("a stored list is split and trimmed, empties dropped", func(t *testing.T) {
		rows := settings{"claude_allowed_tools": " Read , ,Bash,  Glob "}
		assert.Equal(t, []string{"Read", "Bash", "Glob"}, resolve(t, rows, ai.TaskReview).AllowedTools)
	})
}

// The agent override: the user picked an engine for this one run, not a new default — so the
// provider and model are taken as given while the binary and tools still come from that provider's
// saved settings.
func TestAnAgentOverrideSkipsResolutionButNotConfiguration(t *testing.T) {
	rows := settings{
		"ai_provider":         "claude",
		"codex_binary_path":   "/opt/codex",
		"codex_allowed_tools": "Read",
		"codex_model":         "ignored-because-explicit",
	}

	config := ai.NewRouter(rows).ResolveWith(t.Context(), "codex", "o-something", ai.TaskChat)

	assert.Equal(t, "codex", config.Provider)
	assert.Equal(t, "o-something", config.Model)
	assert.Equal(t, "/opt/codex", config.Binary, "still that provider's saved binary")
	assert.Equal(t, []string{"Read"}, config.AllowedTools)
}

func TestAnEmptyAgentOverrideFallsBackToTheCascade(t *testing.T) {
	rows := settings{"ai_provider": "codex", "codex_model": "base"}

	config := ai.NewRouter(rows).ResolveWith(t.Context(), "", "", ai.TaskChat)

	assert.Equal(t, "codex", config.Provider)
	assert.Equal(t, "base", config.Model)
}

// An install whose database did not open still has to be configurable.
func TestRoutingWithNoSettingsAtAll(t *testing.T) {
	config := ai.NewRouter(nil).Resolve(t.Context(), ai.TaskChat)

	assert.Equal(t, "claude", config.Provider)
	assert.Equal(t, "claude", config.Binary)
	assert.Empty(t, config.Model)
}

// A customised template applies whichever engine is active, and an install that customised it
// under the old per-provider key keeps it with no migration step.
func TestSharedTemplateFallsBackThroughTheLegacyKey(t *testing.T) {
	router := ai.NewRouter(settings{"claude_commit_template": "the old customisation"})

	got := router.SharedTemplate(t.Context(), "commit_template", "claude_commit_template", "built-in")

	assert.Equal(t, "the old customisation", got)
}

func TestSharedTemplatePrefersTheNewKey(t *testing.T) {
	router := ai.NewRouter(settings{
		"commit_template":        "the new one",
		"claude_commit_template": "the old one",
	})

	assert.Equal(t, "the new one",
		router.SharedTemplate(t.Context(), "commit_template", "claude_commit_template", "built-in"))
}

func TestSharedTemplateFallsBackToTheBuiltIn(t *testing.T) {
	router := ai.NewRouter(settings{"commit_template": "   "})

	assert.Equal(t, "built-in",
		router.SharedTemplate(t.Context(), "commit_template", "claude_commit_template", "built-in"))
}

func TestEngineForEveryKnownProvider(t *testing.T) {
	for _, known := range ai.KnownProviders {
		t.Run(known, func(t *testing.T) {
			engine := ai.EngineFor(known)
			require.NotNil(t, engine)
			assert.NotEmpty(t, engine.DefaultBinary())
		})
	}

	// ollama and local are the same engine under two ids, because a user's stored setting may say
	// either.
	assert.Equal(t, ai.EngineFor("ollama").ID(), ai.EngineFor("local").ID())
}
