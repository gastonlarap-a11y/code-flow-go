package ai

// Transport is how an engine reaches its model.
//
// It is the first thing every operation branches on, before binary resolution, PATH building or
// stdio pipes: two of the six engines are HTTP endpoints with no process at all, and asking "where
// is your binary" of an endpoint is how a working Ollama install reports itself as not found.
type Transport string

const (
	// TransportSubprocess is a headless CLI child process: Claude, Codex, Gemini/agy, opencode.
	TransportSubprocess Transport = "subprocess"
	// TransportOllama is a local HTTP server, with no credential.
	TransportOllama Transport = "ollama"
	// TransportOpenAICompatible is any /v1/chat/completions-shaped endpoint, reached with a key
	// from the OS credential store — never from a process environment (SEC-007).
	TransportOpenAICompatible Transport = "openai-compatible"
)

// Engine is what a provider id resolves to.
//
// Deliberately small, and only the parts routing and discovery need. The argv each engine builds
// and how it parses its own output belong with the runner, which is where the differences between
// these six get interesting; here they are just four questions with six answers.
type Engine interface {
	// ID is the provider id as it appears in settings.
	ID() string
	// Transport says how this engine is reached.
	Transport() Transport
	// DefaultBinary is the command name, or the endpoint for an HTTP transport.
	DefaultBinary() string
	// CommitMessageModel is a faster model for drafting commit messages, or empty when the engine
	// has none. Only the `commit` task consults it (XLANG-005).
	CommitMessageModel() string
	// ListModelsArgs is the CLI's own model-listing subcommand, or nil when it has none. An engine
	// with no listing answers an empty list and the renderer falls back to a curated one.
	ListModelsArgs() []string
}

// EngineFor resolves a provider id (AI-001).
//
// **Every unrecognised value becomes Claude**, including the empty string. That is not laziness:
// a corrupt settings row, or a provider id written by a version that supported an engine this one
// dropped, has to resolve to something that works rather than failing the user's request.
func EngineFor(provider string) Engine {
	switch provider {
	case "gemini":
		return geminiEngine{}
	case "opencode":
		return openCodeEngine{}
	case "codex":
		return codexEngine{}
	case "ollama", "local":
		return ollamaEngine{}
	case "openai":
		return openAIEngine{}
	default:
		return claudeEngine{}
	}
}

// KnownProviders are the ids the settings screen offers. Anything else still runs — as Claude.
var KnownProviders = []string{"claude", "codex", "gemini", "opencode", "ollama", "local", "openai"}

// ---- the six ------------------------------------------------------------------------------------

type claudeEngine struct{}

func (claudeEngine) ID() string               { return "claude" }
func (claudeEngine) Transport() Transport     { return TransportSubprocess }
func (claudeEngine) DefaultBinary() string    { return "claude" }
func (claudeEngine) ListModelsArgs() []string { return nil }

// CommitMessageModel is the one engine that names a dedicated fast model, VERBATIM. Drafting a
// commit message on the base model costs time and money for a job that does not need either.
func (claudeEngine) CommitMessageModel() string { return "claude-haiku-4-5-20251001" }

type codexEngine struct{}

func (codexEngine) ID() string                 { return "codex" }
func (codexEngine) Transport() Transport       { return TransportSubprocess }
func (codexEngine) DefaultBinary() string      { return "codex" }
func (codexEngine) CommitMessageModel() string { return "" }

// ListModelsArgs is nil: Codex has no listing subcommand. Its models come from a cache file its
// own desktop app maintains, which the discovery code reads rather than spawning anything.
func (codexEngine) ListModelsArgs() []string { return nil }

// geminiEngine drives `agy`, the Antigravity CLI — the binary name and the provider id differ, and
// that is not a mistake to tidy: the provider id is what users' settings rows say.
type geminiEngine struct{}

func (geminiEngine) ID() string                 { return "gemini" }
func (geminiEngine) Transport() Transport       { return TransportSubprocess }
func (geminiEngine) DefaultBinary() string      { return "agy" }
func (geminiEngine) CommitMessageModel() string { return "" }
func (geminiEngine) ListModelsArgs() []string   { return []string{"models"} }

type openCodeEngine struct{}

func (openCodeEngine) ID() string                 { return "opencode" }
func (openCodeEngine) Transport() Transport       { return TransportSubprocess }
func (openCodeEngine) DefaultBinary() string      { return "opencode" }
func (openCodeEngine) CommitMessageModel() string { return "" }
func (openCodeEngine) ListModelsArgs() []string   { return []string{"models"} }

// ollamaEngine is a local server. Its "binary" is an endpoint, which is why every operation
// branches on the transport before going near binary resolution.
type ollamaEngine struct{}

func (ollamaEngine) ID() string                 { return "ollama" }
func (ollamaEngine) Transport() Transport       { return TransportOllama }
func (ollamaEngine) DefaultBinary() string      { return "http://localhost:11434" }
func (ollamaEngine) CommitMessageModel() string { return "" }
func (ollamaEngine) ListModelsArgs() []string   { return nil }

type openAIEngine struct{}

func (openAIEngine) ID() string                 { return "openai" }
func (openAIEngine) Transport() Transport       { return TransportOpenAICompatible }
func (openAIEngine) DefaultBinary() string      { return "https://api.openai.com/v1" }
func (openAIEngine) CommitMessageModel() string { return "" }
func (openAIEngine) ListModelsArgs() []string   { return nil }
