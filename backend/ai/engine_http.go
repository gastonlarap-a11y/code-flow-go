package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// The two engines with no process at all (AI-003).
//
// Both short-circuit before binary resolution, PATH building and stdio pipes — asking "where is
// your binary" of an endpoint is how a working Ollama install reports itself as not found. What
// they share is the message composition; what separates them is billing, and therefore what an
// error means.

// chatMessage is the shape both APIs take.
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// composeMessages is system prompt, then one user message of ask plus data.
//
// The same composition order as every subprocess engine, so a prompt template written for one
// reads the same to another.
func composeMessages(inv Invocation) []chatMessage {
	messages := make([]chatMessage, 0, 2)
	if inv.SystemPrompt != "" {
		messages = append(messages, chatMessage{Role: "system", Content: inv.SystemPrompt})
	}

	user := inv.Prompt
	if inv.StdinContent != "" {
		user += briefSeparator + inv.StdinContent
	}
	return append(messages, chatMessage{Role: "user", Content: user})
}

// HTTPEngine completes a chat turn over an endpoint.
type HTTPEngine struct {
	client   *http.Client
	secrets  SecretReader
	provider string
}

// NewHTTPEngine binds the engine to a client and the credential store.
func NewHTTPEngine(client *http.Client, secrets SecretReader, provider string) HTTPEngine {
	if client == nil {
		client = http.DefaultClient
	}
	return HTTPEngine{client: client, secrets: secrets, provider: provider}
}

// Complete runs one turn against whichever endpoint this provider is.
func (h HTTPEngine) Complete(ctx context.Context, endpoint string, inv Invocation) (Result, error) {
	if EngineFor(h.provider).Transport() == TransportOllama {
		return h.ollama(ctx, endpoint, inv)
	}
	return h.openAI(ctx, endpoint, inv)
}

// post sends a JSON body and returns the response body and status.
func (h HTTPEngine) post(ctx context.Context, url, bearer string, body any) ([]byte, int, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, 0, err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		// One header on one request. The key is never put in a process environment, a log or a
		// response (SEC-007).
		request.Header.Set("Authorization", "Bearer "+bearer)
	}

	response, err := h.client.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = response.Body.Close() }()

	content, err := io.ReadAll(response.Body)
	return content, response.StatusCode, err
}

// ---- OpenAI-compatible ------------------------------------------------------------------------

// openAI completes against /v1/chat/completions.
//
// That endpoint rather than OpenAI's newer Responses API specifically *because* it is the one
// every other provider implements: Azure, OpenRouter, Groq, DeepSeek, Together, Fireworks and a
// local vLLM all speak it, so pointing the base URL at one of them is the entire configuration.
func (h HTTPEngine) openAI(ctx context.Context, endpoint string, inv Invocation) (Result, error) {
	key := ""
	if h.secrets != nil {
		stored, err := h.secrets.Get("ai-api-key:" + h.provider)
		if err == nil {
			key = strings.TrimSpace(stored)
		}
	}

	// Two preconditions, failed before any request is sent, with messages that say what to do.
	// VERBATIM, Spanish.
	if key == "" {
		return Result{}, errors.New("Falta la API key. Añádela en Ajustes › Asistente de IA › Proveedores.") //nolint:staticcheck // ST1005: VERBATIM
	}
	if strings.TrimSpace(inv.Model) == "" {
		return Result{}, errors.New("Selecciona un modelo en Ajustes (por ejemplo gpt-5).") //nolint:staticcheck // ST1005: VERBATIM
	}

	body, status, err := h.post(ctx, strings.TrimSuffix(endpoint, "/")+"/chat/completions", key,
		map[string]any{"model": inv.Model, "messages": composeMessages(inv), "stream": false})
	if err != nil {
		return Result{}, err
	}

	if status < 200 || status >= 300 {
		return Result{}, errors.New(Classify(openAIError(status, inv.Model, body), false))
	}

	var completion struct {
		Choices []struct {
			Message chatMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &completion); err != nil || len(completion.Choices) == 0 {
		return Result{}, fmt.Errorf("the endpoint returned no completion: %s", strings.TrimSpace(string(body)))
	}
	// No server-side conversation state: every request stands alone, which is why no session id
	// comes back and every chat turn re-sends its context.
	return Result{Text: strings.TrimSpace(completion.Choices[0].Message.Content)}, nil
}

// openAIError turns a status into the message the user reads. Every string VERBATIM.
func openAIError(status int, model string, body []byte) string {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return "La API key fue rechazada"
	case http.StatusTooManyRequests:
		// This exact wording is what the quota dictionary matches on — the engine never tags
		// itself, and the marker is applied uniformly to every HTTP result.
		return fmt.Sprintf("Rate limit / quota exceeded: %s", errorDetail(body))
	case http.StatusNotFound:
		return fmt.Sprintf("El modelo '%s' no existe en este endpoint", model)
	default:
		return fmt.Sprintf("HTTP %d: %s", status, errorDetail(body))
	}
}

// errorDetail pulls `error.message` out of an OpenAI-shaped body, falling back to the raw text for
// an endpoint that does not follow the convention.
func errorDetail(body []byte) string {
	var shaped struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &shaped); err == nil && shaped.Error.Message != "" {
		return shaped.Error.Message
	}
	return strings.TrimSpace(string(body))
}

// ---- Ollama -----------------------------------------------------------------------------------

// ollama completes against a local server, with no credential.
//
// It has no quota concept at all — nothing is billed — which is why its errors never reach the
// quota dictionary and a 404 means a model that was never pulled rather than one that does not
// exist.
func (h HTTPEngine) ollama(ctx context.Context, endpoint string, inv Invocation) (Result, error) {
	model := strings.TrimSpace(inv.Model)
	if model == "" {
		// Ollama has no "let the server pick" concept, so a blank model is refused here rather
		// than reaching the server and coming back as something less actionable. VERBATIM.
		return Result{}, errors.New("Selecciona un modelo de Ollama en Ajustes.") //nolint:staticcheck // ST1005: VERBATIM
	}

	body, status, err := h.post(ctx, strings.TrimSuffix(endpoint, "/")+"/api/chat", "",
		map[string]any{"model": model, "messages": composeMessages(inv), "stream": false})
	if err != nil {
		return Result{}, err
	}

	if status == http.StatusNotFound {
		return Result{}, fmt.Errorf("El modelo '%s' no está disponible en Ollama. Descárgalo con: ollama pull %s", model, model) //nolint:staticcheck // ST1005: VERBATIM
	}
	if status < 200 || status >= 300 {
		return Result{}, fmt.Errorf("Ollama devolvió %d: %s", status, strings.TrimSpace(string(body))) //nolint:staticcheck // ST1005: VERBATIM
	}

	var completion struct {
		Message chatMessage `json:"message"`
	}
	if err := json.Unmarshal(body, &completion); err != nil {
		return Result{}, fmt.Errorf("Ollama devolvió una respuesta ilegible: %s", strings.TrimSpace(string(body))) //nolint:staticcheck // ST1005: VERBATIM
	}

	// A synthetic id, minted or reused: the server keeps no session, and this exists purely so a
	// conversation's turns group together in the activity log.
	session := inv.SessionID
	if session == "" {
		session = "ollama-" + uuid.NewString()
	}
	return Result{Text: strings.TrimSpace(completion.Message.Content), SessionID: session}, nil
}
