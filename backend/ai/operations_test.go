package ai_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gastonlarap-a11y/code-flow/backend/ai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ollamaOperations points every task at a local Ollama stand-in, which is the cheapest way to
// exercise an operation end to end: no process, no binary, one round trip.
func ollamaOperations(t *testing.T, reply string) (ai.Operations, *string) {
	t.Helper()

	var received string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// io.ReadAll, not one Read: a single Read is not obliged to fill the buffer, and a test
		// that asserts on a partial body passes by luck.
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		received = string(body)
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":` + jsonString(reply) + `}}`))
	}))
	t.Cleanup(server.Close)

	rows := settings{
		"ai_provider":        "ollama",
		"ollama_model":       "llama3.2",
		"ollama_binary_path": server.URL,
	}
	router := ai.NewRouter(rows)
	return ai.NewOperations(router, nil, server.Client(), nil), &received
}

func jsonString(text string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(text)
	return `"` + escaped + `"`
}

func TestGenerateCommitMessage(t *testing.T) {
	operations, received := ollamaOperations(t, "feat(git): add the thing")

	message, err := operations.GenerateCommitMessage(t.Context(), "", "diff --git a/x b/x\n+added\n")

	require.NoError(t, err)
	assert.Equal(t, "feat(git): add the thing", message)
	assert.Contains(t, *received, "added", "the diff reached the model")
}

func TestGenerateCommitMessageRefusesAnEmptyDiff(t *testing.T) {
	operations, _ := ollamaOperations(t, "unused")

	_, err := operations.GenerateCommitMessage(t.Context(), "", "   \n  ")

	// VERBATIM.
	assert.EqualError(t, err, "No staged changes to summarize")
}

// Some models wrap their answer in a fence despite being told not to, and those backticks would go
// into the repository's history.
func TestACommitMessageLosesItsCodeFence(t *testing.T) {
	operations, _ := ollamaOperations(t, "```\nfeat: the change\n\n- a bullet\n```")

	message, err := operations.GenerateCommitMessage(t.Context(), "", "a diff")

	require.NoError(t, err)
	assert.Equal(t, "feat: the change\n\n- a bullet", message)
}

func TestAFenceWithALanguageTagIsStrippedToo(t *testing.T) {
	operations, _ := ollamaOperations(t, "```go\nfunc main() {}\n```")

	edited, err := operations.InlineEdit(t.Context(), "", "main.go", "file", "selection", "make it empty")

	require.NoError(t, err)
	assert.Equal(t, "func main() {}", edited)
}

// Only one outer fence is stripped: a genuinely fenced block inside the content does not survive a
// round trip, which is what the original does.
func TestOnlyTheOuterFenceIsStripped(t *testing.T) {
	operations, _ := ollamaOperations(t, "```\nBefore\n```inner```\nAfter\n```")

	message, err := operations.GenerateCommitMessage(t.Context(), "", "a diff")

	require.NoError(t, err)
	assert.Contains(t, message, "Before")
	assert.Contains(t, message, "After")
}

func TestTextWithNoFenceIsUntouched(t *testing.T) {
	operations, _ := ollamaOperations(t, "  plain answer  ")

	message, err := operations.GenerateCommitMessage(t.Context(), "", "a diff")

	require.NoError(t, err)
	assert.Equal(t, "plain answer", message)
}

func TestInlineEditRefusesAnEmptySelection(t *testing.T) {
	operations, _ := ollamaOperations(t, "unused")

	_, err := operations.InlineEdit(t.Context(), "", "main.go", "content", "   ", "do something")

	// VERBATIM, Spanish.
	assert.EqualError(t, err, "No hay código seleccionado para editar")
}

func TestInlineEditLabelsItsPayload(t *testing.T) {
	operations, received := ollamaOperations(t, "rewritten")

	_, err := operations.InlineEdit(t.Context(), "", "src/main.go", "whole file", "the selection", "rename it")

	require.NoError(t, err)
	for _, label := range []string{
		"ARCHIVO", "CONTENIDO DEL ARCHIVO (contexto)", "FRAGMENTO SELECCIONADO", "INSTRUCCIÓN",
	} {
		assert.Contains(t, *received, label)
	}
}

// No empty-side guard, unlike the diff-based operations: a side that is empty because one branch
// deleted the file is information the model needs, not a missing input.
func TestResolveConflictSendsAllThreeSidesEvenWhenOneIsEmpty(t *testing.T) {
	operations, received := ollamaOperations(t, "merged content")

	merged, err := operations.ResolveConflict(t.Context(), "", "shared.txt", "base text", "our text", "")

	require.NoError(t, err)
	assert.Equal(t, "merged content", merged)
	for _, label := range []string{"VERSIÓN BASE", "VERSIÓN NUESTRA", "VERSIÓN DE ELLOS"} {
		assert.Contains(t, *received, label)
	}
}

// A side bigger than the cap is better merged by hand than fed whole to the model.
func TestEachConflictSideIsCappedIndependently(t *testing.T) {
	operations, received := ollamaOperations(t, "merged")

	huge := strings.Repeat("a", 50_000)
	_, err := operations.ResolveConflict(t.Context(), "", "big.txt", huge, huge, huge)

	require.NoError(t, err)
	assert.Less(t, len(*received), 3*50_000, "each side was cut before being sent")
}

// A backstop: the UI already hides "fix with AI" for a plain completion endpoint, which has no
// tool loop and no repository to edit.
func TestApplyFindingFixRefusesANonAgenticProvider(t *testing.T) {
	operations, _ := ollamaOperations(t, "unused")

	_, err := operations.ApplyFindingFix(t.Context(), "", "fix this finding", t.TempDir())

	assert.EqualError(t, err,
		"Este proveedor no puede editar archivos. Usa Claude, Gemini u opencode.")
}

// ---- the HTTP engines' own refusals --------------------------------------------------------------

func TestOllamaRefusesABlankModel(t *testing.T) {
	rows := settings{"ai_provider": "ollama", "ollama_binary_path": "http://127.0.0.1:1"}
	operations := ai.NewOperations(ai.NewRouter(rows), nil, http.DefaultClient, nil)

	_, err := operations.GenerateCommitMessage(t.Context(), "", "a diff")

	// Ollama has no "pick for me" concept, so this is refused before the request is sent.
	assert.EqualError(t, err, "Selecciona un modelo de Ollama en Ajustes.")
}

func TestOpenAIRefusesAMissingKeyBeforeSendingAnything(t *testing.T) {
	rows := settings{"ai_provider": "openai", "openai_model": "gpt-5"}
	operations := ai.NewOperations(ai.NewRouter(rows), nil, http.DefaultClient, fakeSecrets{})

	_, err := operations.GenerateCommitMessage(t.Context(), "", "a diff")

	assert.EqualError(t, err,
		"Falta la API key. Añádela en Ajustes › Asistente de IA › Proveedores.")
}

func TestOpenAIRefusesAMissingModel(t *testing.T) {
	rows := settings{"ai_provider": "openai"}
	operations := ai.NewOperations(ai.NewRouter(rows), nil, http.DefaultClient,
		fakeSecrets{"ai-api-key:openai": "sk-test"})

	_, err := operations.GenerateCommitMessage(t.Context(), "", "a diff")

	assert.EqualError(t, err, "Selecciona un modelo en Ajustes (por ejemplo gpt-5).")
}

// This exact wording is what the quota dictionary matches on: the engine never tags itself.
func TestOpenAIRateLimitBecomesAQuotaMarker(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"Too many requests"}}`))
	}))
	defer server.Close()

	rows := settings{
		"ai_provider":        "openai",
		"openai_model":       "gpt-5",
		"openai_binary_path": server.URL,
	}
	operations := ai.NewOperations(ai.NewRouter(rows), nil, server.Client(),
		fakeSecrets{"ai-api-key:openai": "sk-test"})

	_, err := operations.GenerateCommitMessage(t.Context(), "", "a diff")

	require.Error(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), "QUOTA_EXCEEDED::"), err.Error())
	assert.Contains(t, err.Error(), "Too many requests")
}

func TestOpenAIRejectedKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	rows := settings{
		"ai_provider": "openai", "openai_model": "gpt-5", "openai_binary_path": server.URL,
	}
	operations := ai.NewOperations(ai.NewRouter(rows), nil, server.Client(),
		fakeSecrets{"ai-api-key:openai": "sk-test"})

	_, err := operations.GenerateCommitMessage(t.Context(), "", "a diff")

	assert.EqualError(t, err, "La API key fue rechazada")
}

func TestOllamaSuggestsPullingAnAbsentModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	rows := settings{
		"ai_provider": "ollama", "ollama_model": "llama9", "ollama_binary_path": server.URL,
	}
	operations := ai.NewOperations(ai.NewRouter(rows), nil, server.Client(), nil)

	_, err := operations.GenerateCommitMessage(t.Context(), "", "a diff")

	assert.EqualError(t, err,
		"El modelo 'llama9' no está disponible en Ollama. Descárgalo con: ollama pull llama9")
}

// Every request stands alone: no server-side conversation state, so nothing comes back to resume.
func TestOpenAIReportsNoSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`))
	}))
	defer server.Close()

	rows := settings{
		"ai_provider": "openai", "openai_model": "gpt-5", "openai_binary_path": server.URL,
	}
	operations := ai.NewOperations(ai.NewRouter(rows), nil, server.Client(),
		fakeSecrets{"ai-api-key:openai": "sk-test"})

	message, err := operations.GenerateCommitMessage(t.Context(), "", "a diff")

	require.NoError(t, err)
	assert.Equal(t, "hello", message)
}
