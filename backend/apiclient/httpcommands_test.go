package apiclient_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/apiclient"
	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/safego"
)

func httpRegistry(t *testing.T) (*bridge.Registry, *apiclient.Cancels) {
	t.Helper()

	cancels := apiclient.NewCancels()
	registry := bridge.NewRegistry()
	apiclient.RegisterHTTP(registry, apiclient.HTTPDeps{Cancels: cancels})
	registry.Seal()
	return registry, cancels
}

func TestTheThreeHTTPCommandsAreRegistered(t *testing.T) {
	registry, _ := httpRegistry(t)

	for _, name := range []string{"api_send_http", "api_send_http_tracked", "api_cancel_http"} {
		_, registered := registry.Lookup(name)
		assert.True(t, registered, "%s", name)
	}
	assert.Equal(t, 3, registry.Len())
}

// They take no store on purpose: an install whose database did not open can still send one request
// by hand, which is when somebody is most likely to want to.
func TestTheTransportNeedsNoDatabase(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	registry, _ := httpRegistry(t)

	answer, err := invoke(t, registry, "api_send_http", map[string]any{
		"request": map[string]any{
			"method": "GET", "url": server.URL + "/x", "headers": [][2]string{},
			"options": map[string]any{
				"timeout_ms": 5000, "follow_redirects": true, "max_redirects": 10,
				"verify_ssl": true, "max_response_bytes": 1048576,
			},
		},
	})
	require.NoError(t, err)

	response, ok := answer.(apiclient.HTTPResponse)
	require.True(t, ok)
	assert.Equal(t, uint16(200), response.Status)
	assert.Equal(t, "ok", response.BodyText)
}

// `API-061`: firing the token aborts the in-flight send with its own error.
func TestATrackedSendIsCancellableByID(t *testing.T) {
	release := make(chan struct{})
	arrived := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(arrived)
		<-release
		_, _ = io.WriteString(w, "tarde")
	}))
	defer server.Close()
	defer close(release)

	registry, cancels := httpRegistry(t)

	failed := make(chan error, 1)
	safego.Go("tracked-send", func() {
		_, err := invoke(t, registry, "api_send_http_tracked", map[string]any{
			"id": "run-1",
			"request": map[string]any{
				"method": "GET", "url": server.URL + "/lento", "headers": [][2]string{},
				"options": map[string]any{
					"timeout_ms": 10_000, "follow_redirects": true, "max_redirects": 10,
					"verify_ssl": true, "max_response_bytes": 1048576,
				},
			},
		})
		failed <- err
	})

	<-arrived
	cancels.Cancel("run-1")

	select {
	case err := <-failed:
		require.Error(t, err)
		assert.ErrorIs(t, err, apiclient.ErrCancelled)
	case <-time.After(5 * time.Second):
		t.Fatal("the cancelled send never returned")
	}
}

// A cancel that arrives after the send finished is a no-op, and documented as one: it can
// legitimately race a send that already returned, and an error there would put a failure on screen
// for a button that did exactly what it should.
func TestCancellingAnUnknownOrFinishedSendIsANoOp(t *testing.T) {
	registry, cancels := httpRegistry(t)

	_, err := invoke(t, registry, "api_cancel_http", map[string]any{"id": "nunca-existió"})
	assert.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	_, err = invoke(t, registry, "api_send_http_tracked", map[string]any{
		"id": "run-1",
		"request": map[string]any{
			"method": "GET", "url": server.URL + "/x", "headers": [][2]string{},
			"options": map[string]any{
				"timeout_ms": 5000, "follow_redirects": true, "max_redirects": 10,
				"verify_ssl": true, "max_response_bytes": 1048576,
			},
		},
	})
	require.NoError(t, err)

	// The entry is gone the moment the send returned, so this reaches nothing.
	cancels.Cancel("run-1")
	_, err = invoke(t, registry, "api_cancel_http", map[string]any{"id": "run-1"})
	assert.NoError(t, err)
}

func TestAMissingRequestParameterIsNamed(t *testing.T) {
	registry, _ := httpRegistry(t)

	_, err := invoke(t, registry, "api_send_http_tracked", map[string]any{"id": "run-1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request")
}
