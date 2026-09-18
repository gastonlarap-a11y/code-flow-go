package ai_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/gastonlarap-a11y/code-flow/backend/ai"
	"github.com/gastonlarap-a11y/code-flow/backend/bridge/jsonwire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSecrets stands in for the credential store.
type fakeSecrets map[string]string

func (f fakeSecrets) Get(key string) (string, error) { return f[key], nil }

// ---- discovery --------------------------------------------------------------------------------

// The manual `{provider}_binary_path` escape hatch: a path the user typed is used exactly as
// given, with no directory search and no extension resolution.
func TestAnExplicitPathIsTrustedAsIs(t *testing.T) {
	for _, explicit := range []string{"/opt/custom/claude", "./relative/claude", "claude.exe"} {
		t.Run(explicit, func(t *testing.T) {
			assert.Equal(t, explicit, ai.ResolveBinary(explicit, []string{t.TempDir()}))
		})
	}
}

func TestABareNameResolvesAgainstTheSearchDirectories(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("extension resolution has its own test; on Unix a bare name is left to PATH")
	}
	assert.Equal(t, "claude", ai.ResolveBinary("claude", []string{t.TempDir()}),
		"Unix has no executable-extension quirk, so the child's augmented PATH finds it")
}

// The install directories are searched before the inherited PATH, which is the whole point: a
// macOS app launched from Finder inherits launchd's minimal PATH and would otherwise report a
// working CLI as missing.
func TestTheInstallDirectoriesComeBeforeThePath(t *testing.T) {
	dirs := ai.SearchDirs()
	require.NotEmpty(t, dirs)

	pathEntries := filepath.SplitList(os.Getenv("PATH"))
	if len(pathEntries) == 0 {
		t.Skip("no PATH to compare against")
	}

	firstPathEntry := -1
	for i, dir := range dirs {
		if dir == pathEntries[0] {
			firstPathEntry = i
			break
		}
	}
	require.GreaterOrEqual(t, firstPathEntry, 0, "the inherited PATH is in the list")
	assert.Positive(t, firstPathEntry, "and something comes before it")
}

func TestFindOnPathLocatesARealExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a Unix executable bit")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "codeflow-fake-cli")
	require.NoError(t, os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700))

	t.Setenv("PATH", dir)

	resolved, found := ai.FindOnPath("codeflow-fake-cli")

	assert.True(t, found)
	assert.Equal(t, binary, resolved, "an absolute path, which is what the badge shows")
}

// A file with no executable bit is not a binary, however promising its name.
func TestFindOnPathIgnoresANonExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no executable bit")
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "claude"), []byte("not a program"), 0o600))
	t.Setenv("PATH", dir)

	_, found := ai.FindOnPath("claude")

	assert.False(t, found)
}

func TestFindOnPathReportsAMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	resolved, found := ai.FindOnPath("a-cli-nobody-installed")

	assert.False(t, found)
	assert.Equal(t, "a-cli-nobody-installed", resolved, "the badge shows what it looked for")
}

// ---- the Codex catalogue ----------------------------------------------------------------------

func TestCodexModelsComeFromItsCacheWithNoProcessSpawned(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	cache := map[string]any{"models": []map[string]any{
		{"id": "o-third", "visibility": "list", "priority": 30},
		{"id": "o-first", "visibility": "list", "priority": 10},
		{"id": "hidden", "visibility": "hidden", "priority": 1},
		{"id": "o-second", "visibility": "list", "priority": 20},
	}}
	content, err := json.Marshal(cache)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "models_cache.json"), content, 0o600))

	models, err := ai.NewCatalogue(nil, nil).ListModels(t.Context(), "codex", "codex")

	require.NoError(t, err)
	assert.Equal(t, []string{"o-first", "o-second", "o-third"}, models,
		"ascending by priority, and only what the cache marks as listed")
}

// An unreadable cache is "nobody asked Codex", not "Codex has no models" — and only the first
// falls through to the renderer's curated list.
func TestCodexWithNoCacheAnswersAnEmptyList(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())

	models, err := ai.NewCatalogue(nil, nil).ListModels(t.Context(), "codex", "codex")

	require.NoError(t, err)
	assert.Empty(t, models)
	assert.NotNil(t, models)
}

// ---- HTTP engines -----------------------------------------------------------------------------

func TestOllamaModelsComeFromItsTags(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/tags", r.URL.Path)
		_, _ = w.Write([]byte(`{"models":[{"name":"llama3.2:3b"},{"name":"qwen2.5-coder:7b"}]}`))
	}))
	defer server.Close()

	models, err := ai.NewCatalogue(server.Client(), nil).ListModels(t.Context(), "ollama", server.URL)

	require.NoError(t, err)
	assert.Equal(t, []string{"llama3.2:3b", "qwen2.5-coder:7b"}, models)
}

// The filter is an exclude list rather than an allow list, so a brand-new chat model works the day
// it ships. The cost is that an unforeseen non-chat family shows up once, which is the cheaper
// mistake.
func TestOpenAIModelsAreFilteredAndAlphabetised(t *testing.T) {
	var sawBearer string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawBearer = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"gpt-5"},{"id":"text-embedding-3-small"},{"id":"gpt-4o"},
			{"id":"whisper-1"},{"id":"dall-e-3"},{"id":"a-brand-new-model"}]}`))
	}))
	defer server.Close()

	catalogue := ai.NewCatalogue(server.Client(), fakeSecrets{"ai-api-key:openai": "sk-test"})
	models, err := catalogue.ListModels(t.Context(), "openai", server.URL)

	require.NoError(t, err)
	assert.Equal(t, []string{"a-brand-new-model", "gpt-4o", "gpt-5"}, models)
	assert.Equal(t, "Bearer sk-test", sawBearer, "the key travels in a header and nowhere else")
}

// The picker showing nothing is honest; the badge is what reports the missing key.
func TestOpenAIWithNoKeyListsNothingRatherThanFailing(t *testing.T) {
	models, err := ai.NewCatalogue(nil, fakeSecrets{}).ListModels(t.Context(), "openai", "https://example.invalid")

	require.NoError(t, err)
	assert.Empty(t, models)
	assert.NotNil(t, models)
}

// ---- the availability badge ---------------------------------------------------------------------

func TestProbeForASubprocessEngine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a Unix executable bit")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "claude")
	require.NoError(t, os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700))
	t.Setenv("PATH", dir)

	status := ai.NewCatalogue(nil, nil).Probe(t.Context(), "claude", "")

	assert.True(t, status.Available)
	assert.Equal(t, binary, status.Detail, "the resolved path, so the badge can show it")
	assert.Equal(t, "claude", status.Binary, "and what was asked for")
	require.NoError(t, jsonwire.AssertNoNilSlices(status))
}

func TestProbeForAMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	status := ai.NewCatalogue(nil, nil).Probe(t.Context(), "claude", "")

	assert.False(t, status.Available)
	assert.Equal(t, "claude", status.Detail)
}

func TestProbeForOpenAIWithNoKey(t *testing.T) {
	status := ai.NewCatalogue(nil, fakeSecrets{}).Probe(t.Context(), "openai", "")

	assert.False(t, status.Available)
	// VERBATIM: a short raw reason the renderer wraps in its own translated label.
	assert.Equal(t, "missing-api-key", status.Detail)
}

func TestProbeForAReachableOllama(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer server.Close()

	status := ai.NewCatalogue(server.Client(), nil).Probe(t.Context(), "ollama", server.URL)

	assert.True(t, status.Available)
	assert.Equal(t, server.URL, status.Detail, "the endpoint, not a path")
}

func TestProbeForAnUnreachableOllama(t *testing.T) {
	status := ai.NewCatalogue(http.DefaultClient, nil).
		Probe(t.Context(), "ollama", "http://127.0.0.1:1")

	assert.False(t, status.Available)
	assert.NotEmpty(t, status.Detail, "the raw reason, never pre-translated")
}

// An engine with no listing subcommand answers an empty list with no process started at all.
func TestClaudeListsNothingWithoutSpawningAnything(t *testing.T) {
	models, err := ai.NewCatalogue(nil, nil).ListModels(t.Context(), "claude", "claude")

	require.NoError(t, err)
	assert.Empty(t, models)
	assert.NotNil(t, models)
}
