package ai

import (
	"context"
	"strings"
)

// Which engine runs which task, and with which model (XLANG-005, AI-001, AI-007).
//
// Every one of these strings is a settings key an existing install already has rows for, and the
// renderer's own settings screen re-reads the same keys to mirror this cascade. The two have to
// agree on every name or the UI shows a provider the backend does not use.

// Task is one of the ten things an engine can be asked to do.
//
// Each selects a provider **and** a model independently, which is the point: one repository can
// draft commits on a local model, review pull requests on the largest one available, and fix
// findings through a third engine entirely.
type Task string

// The ten task keys, VERBATIM — they are the `{task}` in `ai_provider_{task}` and
// `{provider}_{task}_model`, so a typo here silently orphans a user's saved setting.
const (
	TaskCommit        Task = "commit"
	TaskAnalyze       Task = "analyze"
	TaskReview        Task = "review"
	TaskPRDescription Task = "pr_description"
	TaskChat          Task = "chat"
	TaskFix           Task = "fix"
	TaskConflict      Task = "conflict"
	TaskInline        Task = "inline"
	TaskTicketReview  Task = "ticket_review"
	TaskDBML          Task = "dbml"
)

// AllTasks is every key, for the test that pins them and for anything enumerating the settings
// screen's rows.
var AllTasks = []Task{
	TaskCommit, TaskAnalyze, TaskReview, TaskPRDescription, TaskChat,
	TaskFix, TaskConflict, TaskInline, TaskTicketReview, TaskDBML,
}

// DefaultProvider is where every unset or unrecognised provider lands (AI-001).
const DefaultProvider = "claude"

// judgingTasks are handed the change they are meant to judge, so an unset tool list falls back to
// a read-only set rather than to no restriction at all.
//
// The reasoning is measured rather than tidy: with no bound, a review of this repository called
// `Bash` seventeen times against two or three `Read`s and read over two million cached tokens to
// judge a diff it had already been given. A chat turn is a conversation with the repository and a
// fix is an edit to it — bounding either would take away what the user asked for.
var judgingTasks = map[Task]bool{
	TaskAnalyze: true, TaskReview: true, TaskTicketReview: true,
}

// defaultJudgingTools is that fallback. VERBATIM, and only for an **unset** setting: a blank stored
// value means "no tools", which is a choice the user made by clearing every checkbox.
var defaultJudgingTools = []string{"Read", "Grep", "Glob"}

// SettingsReader is the one thing routing needs from storage.
//
// Declared at the consumer and deliberately narrow: routing reads settings and nothing else, and a
// nil reader resolves everything to its defaults — which is what keeps the AI commands answering
// on an install whose database did not open.
type SettingsReader interface {
	GetSetting(ctx context.Context, key string) (*string, error)
}

// Config is everything one operation needs to invoke an engine.
type Config struct {
	Provider     string
	Model        string
	Binary       string
	AllowedTools []string
}

// Router resolves the cascade against stored settings.
type Router struct{ settings SettingsReader }

// NewRouter binds a router to the settings store. A nil store is allowed and means "no settings",
// which resolves to the built-in defaults throughout.
func NewRouter(settings SettingsReader) Router { return Router{settings: settings} }

// setting reads one key, treating blank as unset.
//
// **Blank counts as unset everywhere in this cascade**, and that is the rule rather than a
// convenience: clearing a field in the settings UI writes an empty string, and the user means
// "inherit" by it. The one exception is the tool list, where blank means "no tools" — see Tools.
func (r Router) setting(ctx context.Context, key string) string {
	if r.settings == nil {
		return ""
	}
	value, err := r.settings.GetSetting(ctx, key)
	if err != nil || value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

// ActiveProvider is the global provider, defaulting to Claude.
func (r Router) ActiveProvider(ctx context.Context) string {
	if provider := r.setting(ctx, "ai_provider"); provider != "" {
		return provider
	}
	return DefaultProvider
}

// ProviderFor resolves which provider runs a task: the per-task override, else the global one.
func (r Router) ProviderFor(ctx context.Context, task Task) string {
	if provider := r.setting(ctx, "ai_provider_"+string(task)); provider != "" {
		return provider
	}
	return r.ActiveProvider(ctx)
}

// Resolve is the full cascade for a task (XLANG-005).
func (r Router) Resolve(ctx context.Context, task Task) Config {
	provider := r.ProviderFor(ctx, task)
	return r.resolveFor(ctx, provider, task, "")
}

// ResolveWith is the cascade for an explicitly chosen provider and model.
//
// It skips provider and model resolution but still reads that provider's binary and tools, which
// is what `analyze_working_changes` and `send_chat_message` do when the renderer sends an agent
// override: the user picked an engine for this one run, not a new default.
func (r Router) ResolveWith(ctx context.Context, provider, model string, task Task) Config {
	provider, model = strings.TrimSpace(provider), strings.TrimSpace(model)
	if provider == "" {
		return r.Resolve(ctx, task)
	}
	return r.resolveFor(ctx, provider, task, model)
}

func (r Router) resolveFor(ctx context.Context, provider string, task Task, model string) Config {
	engine := EngineFor(provider)

	if model == "" {
		model = r.modelFor(ctx, provider, task, engine)
	}

	binary := r.setting(ctx, provider+"_binary_path")
	if binary == "" {
		binary = engine.DefaultBinary()
	}

	return Config{
		Provider:     provider,
		Model:        model,
		Binary:       binary,
		AllowedTools: r.toolsFor(ctx, provider, task),
	}
}

// modelFor is the model half of the cascade.
//
// The middle step applies to **`commit` only**: an engine may name a faster, cheaper model for
// drafting a commit message, and using the base model there costs time and money for a job that
// does not need it. The renderer's mirror of this cascade omits the step (BUG-XLANG-a), so the
// settings screen shows the base model while the backend runs the fast one — preserved, because
// the behaviour is right and only the display is wrong.
func (r Router) modelFor(ctx context.Context, provider string, task Task, engine Engine) string {
	if model := r.setting(ctx, provider+"_"+string(task)+"_model"); model != "" {
		return model
	}
	if task == TaskCommit {
		if model := engine.CommitMessageModel(); model != "" {
			return model
		}
	}
	return r.setting(ctx, provider+"_model")
}

// toolsFor is the allowed-tool list for a task.
//
// Unset and blank are different here, uniquely in this cascade. Unset means nobody has opened that
// settings row, and for the judging tasks that falls back to a read-only set. Blank means every
// checkbox was cleared, which is an answer — overruling it with a default would take a decision
// away from the person who made it.
func (r Router) toolsFor(ctx context.Context, provider string, task Task) []string {
	key := provider + "_allowed_tools"

	var stored *string
	if r.settings != nil {
		if value, err := r.settings.GetSetting(ctx, key); err == nil {
			stored = value
		}
	}

	if stored == nil && judgingTasks[task] {
		return append([]string(nil), defaultJudgingTools...)
	}
	if stored == nil {
		return []string{}
	}

	tools := make([]string, 0, 4)
	for _, tool := range strings.Split(*stored, ",") {
		if trimmed := strings.TrimSpace(tool); trimmed != "" {
			tools = append(tools, trimmed)
		}
	}
	return tools
}

// SharedTemplate reads a prompt template, falling back to the legacy per-provider key (AI-053).
//
// Templates are provider-independent by design: a customised commit template applies whichever
// engine is active. The legacy key is read second and without a migration step, so an install that
// customised its template under the old `claude_`-prefixed name keeps it — silently, which is the
// point. Both unset means the built-in default.
func (r Router) SharedTemplate(ctx context.Context, key, legacyKey, fallback string) string {
	if value := r.setting(ctx, key); value != "" {
		return value
	}
	if value := r.setting(ctx, legacyKey); value != "" {
		return value
	}
	return fallback
}
