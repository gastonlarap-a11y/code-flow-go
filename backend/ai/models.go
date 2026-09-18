package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/proc"
)

// Listing models and probing availability (AI-008, AI-015, AI-032).
//
// Both answer the Settings screen, and both degrade rather than fail: an empty model list is the
// renderer's signal to show its own curated list, and an unreachable endpoint is a badge, not an
// error dialog. Neither is a place to interrupt someone configuring their tools.

// ProviderStatus is the Settings availability badge.
type ProviderStatus struct {
	Available bool `json:"available"`
	// Detail is the resolved path or endpoint when available, and a short raw reason when not —
	// never pre-translated: the renderer wraps it in its own label.
	Detail string `json:"detail"`
	// Binary is what was actually checked, so the badge can show which path failed.
	Binary string `json:"binary"`
}

// SecretReader reads one credential. Declared at the consumer, and the key never leaves this
// package's request-building code — it is not returned, logged, or put in an environment.
type SecretReader interface {
	Get(key string) (string, error)
}

// Catalogue answers the two Settings questions for a provider.
type Catalogue struct {
	client  *http.Client
	secrets SecretReader
}

// NewCatalogue binds the catalogue to an HTTP client and the credential store.
func NewCatalogue(client *http.Client, secrets SecretReader) Catalogue {
	if client == nil {
		client = http.DefaultClient
	}
	return Catalogue{client: client, secrets: secrets}
}

// apiKeyFor reads a provider's stored key, or the empty string.
//
// A missing key is not an error here: "no key configured" is a state the badge reports and the
// model list degrades over, not a failure to raise.
func (c Catalogue) apiKeyFor(provider string) string {
	if c.secrets == nil {
		return ""
	}
	key, err := c.secrets.Get("ai-api-key:" + provider)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(key)
}

// ListModels answers the models a provider reports (AI-015).
//
// The precedence is: an HTTP engine asks its own API; a subprocess engine's on-disk catalogue is
// read before its CLI is spawned; an engine with neither answers an empty list **with no process
// started at all**. Empty is a valid answer throughout — the renderer shows a curated list for it.
func (c Catalogue) ListModels(ctx context.Context, provider, binary string) ([]string, error) {
	engine := EngineFor(provider)
	if binary == "" {
		binary = engine.DefaultBinary()
	}

	switch engine.Transport() {
	case TransportOllama:
		return c.ollamaTags(ctx, binary)

	case TransportOpenAICompatible:
		key := c.apiKeyFor(provider)
		if key == "" {
			// Degraded, not an error: the Settings badge is what reports a missing key, and the
			// picker showing nothing is the honest answer.
			return []string{}, nil
		}
		models, err := c.openAIModels(ctx, binary, key)
		if err != nil {
			return []string{}, nil //nolint:nilerr // see above: the badge reports it, not the picker
		}
		return models, nil
	}

	// A catalogue the CLI already wrote to disk beats spawning it.
	if provider == "codex" {
		if models, found := codexCachedModels(); found {
			return models, nil
		}
	}

	args := engine.ListModelsArgs()
	if len(args) == 0 {
		return []string{}, nil
	}
	return listModelsByCommand(ctx, binary, args)
}

// Probe answers whether a provider is usable (AI-008).
func (c Catalogue) Probe(ctx context.Context, provider, binary string) ProviderStatus {
	engine := EngineFor(provider)
	if binary == "" {
		binary = engine.DefaultBinary()
	}

	switch engine.Transport() {
	case TransportOllama:
		if _, err := c.ollamaTags(ctx, binary); err != nil {
			return ProviderStatus{Detail: err.Error(), Binary: binary}
		}
		return ProviderStatus{Available: true, Detail: binary, Binary: binary}

	case TransportOpenAICompatible:
		key := c.apiKeyFor(provider)
		if key == "" {
			// A short raw reason the renderer translates. VERBATIM.
			return ProviderStatus{Detail: "missing-api-key", Binary: binary}
		}
		if _, err := c.openAIModels(ctx, binary, key); err != nil {
			return ProviderStatus{Detail: err.Error(), Binary: binary}
		}
		return ProviderStatus{Available: true, Detail: binary, Binary: binary}
	}

	resolved, found := FindOnPath(binary)
	if !found {
		return ProviderStatus{Detail: binary, Binary: binary}
	}
	return ProviderStatus{Available: true, Detail: resolved, Binary: binary}
}

// ---- subprocess listing ---------------------------------------------------------------------------

// listModelsByCommand spawns the CLI's own listing subcommand.
//
// Through proc like every other child, so it runs in its own process group with no console window
// on Windows — a Settings screen that flashed a black window every time it refreshed a badge would
// be its own bug.
func listModelsByCommand(ctx context.Context, binary string, args []string) ([]string, error) {
	resolved := ResolveBinary(binary, SearchDirs())

	cmd := proc.Command(ctx, resolved, args...)
	cmd.Env = proc.Environment(SearchDirs())

	out, err := cmd.Output()
	if err != nil {
		detail := "no output"
		if text := strings.TrimSpace(string(out)); text != "" {
			detail = text
		}
		// VERBATIM shape.
		return nil, fmt.Errorf("'%s %s' failed: %s", binary, strings.Join(args, " "), detail)
	}

	models := make([]string, 0, 16)
	for line := range proc.Lines(strings.NewReader(string(out))) {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			models = append(models, trimmed)
		}
	}
	return models, nil
}

// codexCachedModels reads the catalogue Codex's own desktop app maintains (AI-032).
//
// No subcommand is spawned — Codex has none. An unreadable or empty cache answers "not found"
// rather than an empty list, which is the difference between "Codex says it has no models" and
// "nobody asked Codex", and only the second falls through to the renderer's curated list.
func codexCachedModels() ([]string, bool) {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return nil, false
		}
		home = filepath.Join(userHome, ".codex")
	}

	content, err := os.ReadFile(filepath.Join(home, "models_cache.json")) //nolint:gosec // a fixed name under a fixed directory
	if err != nil {
		return nil, false
	}

	var cache struct {
		Models []struct {
			ID         string `json:"id"`
			Visibility string `json:"visibility"`
			Priority   int    `json:"priority"`
		} `json:"models"`
	}
	if err := json.Unmarshal(content, &cache); err != nil {
		return nil, false
	}

	listed := make([]struct {
		id       string
		priority int
	}, 0, len(cache.Models))
	for _, model := range cache.Models {
		if model.Visibility == "list" && model.ID != "" {
			listed = append(listed, struct {
				id       string
				priority int
			}{model.ID, model.Priority})
		}
	}
	if len(listed) == 0 {
		return nil, false
	}

	sort.SliceStable(listed, func(i, j int) bool { return listed[i].priority < listed[j].priority })

	models := make([]string, 0, len(listed))
	for _, model := range listed {
		models = append(models, model.id)
	}
	return models, true
}

// ---- HTTP listing ----------------------------------------------------------------------------------

func (c Catalogue) getJSON(ctx context.Context, url, bearer string, into any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if bearer != "" {
		// The key travels in a header on one request and nowhere else — never into a process
		// environment, a log or a response (SEC-007).
		request.Header.Set("Authorization", "Bearer "+bearer)
	}

	response, err := c.client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return json.NewDecoder(response.Body).Decode(into)
}

// ollamaTags lists what a local Ollama has pulled.
func (c Catalogue) ollamaTags(ctx context.Context, endpoint string) ([]string, error) {
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := c.getJSON(ctx, strings.TrimSuffix(endpoint, "/")+"/api/tags", "", &tags); err != nil {
		return nil, err
	}

	models := make([]string, 0, len(tags.Models))
	for _, model := range tags.Models {
		if model.Name != "" {
			models = append(models, model.Name)
		}
	}
	return models, nil
}

// openAIModels lists an endpoint's chat models, alphabetically.
func (c Catalogue) openAIModels(ctx context.Context, endpoint, key string) ([]string, error) {
	var listing struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := c.getJSON(ctx, strings.TrimSuffix(endpoint, "/")+"/models", key, &listing); err != nil {
		return nil, err
	}

	models := make([]string, 0, len(listing.Data))
	for _, model := range listing.Data {
		if model.ID != "" && isChatModel(model.ID) {
			models = append(models, model.ID)
		}
	}
	sort.Strings(models)
	return models, nil
}

// nonChatFamilies are the substrings that mark a model as something other than a chat model.
//
// An exclude list rather than an allow list, deliberately: a brand-new chat model works the day it
// ships, where an allow list would hide it until someone updated this file. The cost is that an
// unforeseen non-chat family shows up in the picker once, which is the cheaper mistake.
var nonChatFamilies = []string{
	"embedding", "tts", "whisper", "transcribe", "dall-e", "moderation", "audio",
	"realtime", "image", "sora", "similarity", "-search-", "-edit-", "davinci",
	"babbage", "curie",
}

func isChatModel(id string) bool {
	lowered := strings.ToLower(id)
	for _, family := range nonChatFamilies {
		if strings.Contains(lowered, family) {
			return false
		}
	}
	return true
}
