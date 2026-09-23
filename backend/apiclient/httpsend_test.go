package apiclient_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/apiclient"
)

// The transport against a real server. Every assertion is about what actually went over the wire or
// came back off it — a transport tested through a fake of itself tests the fake.

// recorder captures what each request carried.
type recorder struct {
	mutex    sync.Mutex
	requests []capturedRequest
}

type capturedRequest struct {
	Method string
	Path   string
	Header http.Header
	Body   string
}

func (r *recorder) capture(request *http.Request) {
	body, _ := io.ReadAll(request.Body)

	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.requests = append(r.requests, capturedRequest{
		Method: request.Method,
		Path:   request.URL.RequestURI(),
		Header: request.Header.Clone(),
		Body:   string(body),
	})
}

func (r *recorder) seen() []capturedRequest {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return append([]capturedRequest{}, r.requests...)
}

// request builds the minimal shape, with the defaults the renderer would send.
func request(method, url string) apiclient.HTTPSendRequest {
	return apiclient.HTTPSendRequest{
		Method: method, URL: url,
		Options: apiclient.NetworkOptions{
			TimeoutMs: 5_000, FollowRedirects: true, MaxRedirects: 10, VerifySSL: true,
			MaxResponseBytes: 50 * 1024 * 1024,
		},
	}
}

// ---- the basics --------------------------------------------------------------------------------

func TestASendCarriesTheHeadersAndReadsTheBody(t *testing.T) {
	log := &recorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.capture(r)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Custom", "sí")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()

	send := request(http.MethodPost, server.URL+"/pagos")
	send.Headers = [][2]string{{"X-Trace", "abc123"}}
	send.BodyText = new(`{"importe":100}`)

	response, err := apiclient.Send(t.Context(), send)
	require.NoError(t, err)

	assert.Equal(t, uint16(201), response.Status)
	assert.Equal(t, "Created", response.StatusText)
	assert.Equal(t, `{"ok":true}`, response.BodyText)
	assert.Nil(t, response.BodyBase64, "a JSON body is text, never both")
	assert.Equal(t, uint64(11), response.SizeBytes)

	got := log.seen()
	require.Len(t, got, 1)
	assert.Equal(t, "abc123", got[0].Header.Get("X-Trace"))
	assert.Equal(t, `{"importe":100}`, got[0].Body)
	// Advertised by hand so the console can report them; the transport would otherwise add this
	// below the layer the request summary can see.
	assert.Equal(t, "gzip, br, deflate", got[0].Header.Get("Accept-Encoding"))
}

// The console's record is of the **built** request, so the headers the transport injected are in it.
func TestTheSentSummaryIsHonestAboutWhatTheTransportAdded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	send := request(http.MethodGet, server.URL+"/x")
	response, err := apiclient.Send(t.Context(), send)
	require.NoError(t, err)

	headers := map[string]string{}
	for _, header := range response.Sent.Headers {
		headers[strings.ToLower(header[0])] = header[1]
	}

	assert.Equal(t, "gzip, br, deflate", headers["accept-encoding"])
	// `Host` never lives in the header map — it travels in the request's own field — so it is
	// synthesised for the console rather than read back.
	assert.NotEmpty(t, headers["host"])
	assert.Equal(t, http.MethodGet, response.Sent.Method)
}

func TestTheCallersJarIsAppendedOnlyWhenTheyDidNotSetTheHeader(t *testing.T) {
	log := &recorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.capture(r)
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	t.Run("pre-matched by the caller", func(t *testing.T) {
		send := request(http.MethodGet, server.URL+"/x")
		send.Options.Cookies = [][2]string{{"sid", "abc"}, {"csrf", "def"}}

		_, err := apiclient.Send(t.Context(), send)
		require.NoError(t, err)

		got := log.seen()
		assert.Equal(t, "sid=abc; csrf=def", got[len(got)-1].Header.Get("Cookie"))
	})

	t.Run("an explicit header wins over the jar", func(t *testing.T) {
		send := request(http.MethodGet, server.URL+"/x")
		send.Headers = [][2]string{{"Cookie", "mio=1"}}
		send.Options.Cookies = [][2]string{{"sid", "abc"}}

		_, err := apiclient.Send(t.Context(), send)
		require.NoError(t, err)

		got := log.seen()
		assert.Equal(t, "mio=1", got[len(got)-1].Header.Get("Cookie"),
			"no jar matching lives in this layer; the caller decided")
	})
}

// ---- bodies ------------------------------------------------------------------------------------

func TestBodyPriority(t *testing.T) {
	log := &recorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.capture(r)
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	// Nothing enforces that only one arrives — the renderer's own type guarantees it — so what is
	// asserted is the fixed order: text, then base64, then file, then urlencoded, then multipart.
	send := request(http.MethodPost, server.URL+"/x")
	send.BodyText = new("el texto gana")
	send.BodyBase64 = new(base64.StdEncoding.EncodeToString([]byte("el base64 no")))
	send.Urlencoded = [][2]string{{"tampoco", "1"}}

	_, err := apiclient.Send(t.Context(), send)
	require.NoError(t, err)

	got := log.seen()
	assert.Equal(t, "el texto gana", got[len(got)-1].Body)
}

func TestABase64BodyIsDecodedBeforeItGoesOut(t *testing.T) {
	log := &recorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.capture(r)
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	send := request(http.MethodPost, server.URL+"/x")
	send.BodyBase64 = new(base64.StdEncoding.EncodeToString([]byte{0x00, 0xff, 0x10}))

	_, err := apiclient.Send(t.Context(), send)
	require.NoError(t, err)

	got := log.seen()
	assert.Equal(t, string([]byte{0x00, 0xff, 0x10}), got[len(got)-1].Body)
}

func TestAnUndecodableBase64BodyFailsBeforeAnythingIsDialled(t *testing.T) {
	send := request(http.MethodPost, "https://unreachable.invalid/x")
	send.BodyBase64 = new("no es base64 !!!")

	_, err := apiclient.Send(t.Context(), send)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "base64")
}

func TestAUrlencodedBodySetsItsContentTypeOnlyIfTheCallerDidNot(t *testing.T) {
	log := &recorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.capture(r)
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	send := request(http.MethodPost, server.URL+"/x")
	send.Urlencoded = [][2]string{{"a", "1"}, {"b", "dos palabras"}}

	_, err := apiclient.Send(t.Context(), send)
	require.NoError(t, err)

	got := log.seen()
	assert.Equal(t, "application/x-www-form-urlencoded", got[len(got)-1].Header.Get("Content-Type"))
	assert.Equal(t, "a=1&b=dos+palabras", got[len(got)-1].Body)
}

func TestAFileBodyIsStreamedWithAnExplicitLength(t *testing.T) {
	log := &recorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.capture(r)
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "cuerpo.json")
	content := strings.Repeat("x", 200_000)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	send := request(http.MethodPut, server.URL+"/x")
	send.BodyFile = &path

	response, err := apiclient.Send(t.Context(), send)
	require.NoError(t, err)

	got := log.seen()
	assert.Equal(t, content, got[len(got)-1].Body)
	// Set explicitly from the file's own length so the transport does not fall back to chunked
	// encoding, which some servers refuse for an upload.
	assert.Equal(t, fmt.Sprint(len(content)), got[len(got)-1].Header.Get("Content-Length"))

	// Previewed without ever reading the content: what is useful about a large upload is what it
	// is and where it came from.
	assert.Contains(t, response.Sent.BodyPreview, "200000 bytes streamed from")
	assert.NotContains(t, response.Sent.BodyPreview, "xxxx")
}

func TestADirectoryAsABodyIsRefusedByName(t *testing.T) {
	directory := t.TempDir()
	send := request(http.MethodPut, "https://unreachable.invalid/x")
	send.BodyFile = &directory

	_, err := apiclient.Send(t.Context(), send)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is a directory, not a file")
}

// `API-017`: the boundary is generated inside the writer, so only its own content type can delimit
// the body — any the caller set is discarded.
func TestMultipartDiscardsTheCallersContentType(t *testing.T) {
	log := &recorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.capture(r)
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "adjunto.txt")
	require.NoError(t, os.WriteFile(path, []byte("contenido"), 0o600))

	send := request(http.MethodPost, server.URL+"/x")
	send.Headers = [][2]string{{"Content-Type", "application/json"}}
	send.FormData = []apiclient.FormPart{
		{Name: "campo", Value: new("valor")},
		{Name: "archivo", FilePath: &path, ContentType: new("text/plain")},
	}

	_, err := apiclient.Send(t.Context(), send)
	require.NoError(t, err)

	got := log.seen()[0]
	contentType := got.Header.Get("Content-Type")
	assert.True(t, strings.HasPrefix(contentType, "multipart/form-data; boundary="), contentType)
	assert.NotContains(t, contentType, "application/json")

	assert.Contains(t, got.Body, `name="campo"`)
	assert.Contains(t, got.Body, "valor")
	assert.Contains(t, got.Body, `filename="adjunto.txt"`)
	assert.Contains(t, got.Body, "contenido")
	assert.Contains(t, got.Body, "Content-Type: text/plain", "the part's own type is kept")
}

// ---- redirects ---------------------------------------------------------------------------------

func TestRedirectsAreRecordedWithTheFinalURLLast(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/uno":
			http.Redirect(w, r, server.URL+"/dos", http.StatusFound)
		case "/dos":
			http.Redirect(w, r, server.URL+"/tres", http.StatusFound)
		default:
			_, _ = io.WriteString(w, "llegué")
		}
	}))
	defer server.Close()

	response, err := apiclient.Send(t.Context(), request(http.MethodGet, server.URL+"/uno"))
	require.NoError(t, err)

	assert.Equal(t, "llegué", response.BodyText)
	assert.Equal(t, []string{server.URL + "/dos", server.URL + "/tres"}, response.Redirects)
}

func TestFollowRedirectsOffHandsBackTheRaw3xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/otro", http.StatusFound)
	}))
	defer server.Close()

	send := request(http.MethodGet, server.URL+"/uno")
	send.Options.FollowRedirects = false

	response, err := apiclient.Send(t.Context(), send)
	require.NoError(t, err)

	assert.Equal(t, uint16(302), response.Status, "the caller asked to see the redirect itself")
	assert.Equal(t, "/otro", headerOf(response.Headers, "Location"))
}

func TestTheHopCapIsEnforced(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/siguiente", http.StatusFound)
	}))
	defer server.Close()

	send := request(http.MethodGet, server.URL+"/uno")
	send.Options.MaxRedirects = 3

	_, err := apiclient.Send(t.Context(), send)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "more than 3 redirects")
}

// `API-003`/`API-004`: a cross-host hop with credentials to keep is stopped and resumed by hand,
// precisely so the transport does not strip `Authorization` on the way.
func TestKeepAuthOnRedirectCarriesTheHeaderAcrossHosts(t *testing.T) {
	log := &recorder{}
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.capture(r)
		_, _ = io.WriteString(w, "llegué")
	}))
	defer destination.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/destino", http.StatusFound)
	}))
	defer origin.Close()

	send := request(http.MethodGet, origin.URL+"/origen")
	send.Headers = [][2]string{{"Authorization", "Bearer el-token"}}
	send.Options.KeepAuthOnRedirect = true

	response, err := apiclient.Send(t.Context(), send)
	require.NoError(t, err)
	assert.Equal(t, "llegué", response.BodyText)

	got := log.seen()
	require.Len(t, got, 1)
	assert.Equal(t, "Bearer el-token", got[0].Header.Get("Authorization"),
		"the whole point of the manual resume")
}

func TestWithoutKeepAuthTheHeaderIsNotCarriedAcrossHosts(t *testing.T) {
	log := &recorder{}
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.capture(r)
		_, _ = io.WriteString(w, "llegué")
	}))
	defer destination.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/destino", http.StatusFound)
	}))
	defer origin.Close()

	// Addressed by a different **hostname**, not merely a different port: the stripping rule in the
	// transport — and in 2.x's, which this mirrors — compares hostnames, so two ports on
	// `127.0.0.1` are one host to it and the header would travel.
	viaLocalhost := strings.Replace(origin.URL, "127.0.0.1", "localhost", 1)

	send := request(http.MethodGet, viaLocalhost+"/origen")
	send.Headers = [][2]string{{"Authorization", "Bearer el-token"}}

	_, err := apiclient.Send(t.Context(), send)
	require.NoError(t, err)

	got := log.seen()
	require.Len(t, got, 1)
	// The default, and it is the safe one: forwarding a bearer token to whatever host a 302 names
	// is a credential leak, and only the person who typed it can say otherwise.
	assert.Empty(t, got[0].Header.Get("Authorization"))
}

// `BUG-API-a`, preserved and pinned: the manual path downgrades **any** non-GET/HEAD method on a
// 301/302, not only POST — broader than the browser behaviour the original claimed to replicate.
func TestTheManualPathDowngradesEveryNonGetMethod(t *testing.T) {
	log := &recorder{}
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.capture(r)
		_, _ = io.WriteString(w, "ok")
	}))
	defer destination.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/destino", http.StatusMovedPermanently)
	}))
	defer origin.Close()

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			send := request(method, origin.URL+"/origen")
			send.Headers = [][2]string{{"Authorization", "Bearer t"}}
			send.Options.KeepAuthOnRedirect = true
			send.BodyText = new("el cuerpo")

			_, err := apiclient.Send(t.Context(), send)
			require.NoError(t, err)

			got := log.seen()
			landed := got[len(got)-1]
			assert.Equal(t, http.MethodGet, landed.Method,
				"a PUT or DELETE is downgraded too — BUG-API-a, ported as-is")
			assert.Empty(t, landed.Body)
		})
	}
}

func TestA307PreservesTheMethodAndBody(t *testing.T) {
	log := &recorder{}
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.capture(r)
		_, _ = io.WriteString(w, "ok")
	}))
	defer destination.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/destino", http.StatusTemporaryRedirect)
	}))
	defer origin.Close()

	send := request(http.MethodPost, origin.URL+"/origen")
	send.Headers = [][2]string{{"Authorization", "Bearer t"}}
	send.Options.KeepAuthOnRedirect = true
	send.BodyText = new("el cuerpo")

	_, err := apiclient.Send(t.Context(), send)
	require.NoError(t, err)

	landed := log.seen()[0]
	assert.Equal(t, http.MethodPost, landed.Method)
	assert.Equal(t, "el cuerpo", landed.Body, "307 is what preserving the body is for")
}

// ---- Digest against a real challenge ------------------------------------------------------------

func TestTheDigestHandshakeIsOneRoundTrip(t *testing.T) {
	log := &recorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.capture(r)

		if r.Header.Get("Authorization") == "" {
			w.Header().Set("WWW-Authenticate",
				`Digest realm="testrealm@host.com", qop="auth", nonce="dcd98b7102dd2f0e8b11d0f600bfb0c093", opaque="5ccc069c"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, "autenticado")
	}))
	defer server.Close()

	send := request(http.MethodGet, server.URL+"/dir/index.html")
	send.Auth = &apiclient.BackendAuth{
		Kind: apiclient.AuthDigest, Username: "Mufasa", Password: "Circle Of Life",
	}

	response, err := apiclient.Send(t.Context(), send)
	require.NoError(t, err)
	assert.Equal(t, "autenticado", response.BodyText)

	got := log.seen()
	require.Len(t, got, 2, "unauthenticated first, then the computed answer")
	assert.Empty(t, got[0].Header.Get("Authorization"))

	authorization := got[1].Header.Get("Authorization")
	assert.True(t, strings.HasPrefix(authorization, "Digest "), authorization)
	assert.Contains(t, authorization, `username="Mufasa"`)
	assert.Contains(t, authorization, `uri="/dir/index.html"`)
	assert.Contains(t, authorization, `opaque="5ccc069c"`)
	assert.Contains(t, authorization, "nc=00000001")

	// The console shows the attempt that carried the credential, not the one that did not.
	assert.Contains(t, headerOf(response.Sent.Headers, "Authorization"), "Digest ")
}

func TestA401WithNoDigestChallengeSaysSo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="x"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	send := request(http.MethodGet, server.URL+"/x")
	send.Auth = &apiclient.BackendAuth{Kind: apiclient.AuthDigest, Username: "u", Password: "p"}

	_, err := apiclient.Send(t.Context(), send)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no 'WWW-Authenticate: Digest' challenge")
}

// ---- SigV4 on the wire ---------------------------------------------------------------------------

func TestAnAWSSignedRequestCarriesItsThreeHeaders(t *testing.T) {
	log := &recorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.capture(r)
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	send := request(http.MethodPost, server.URL+"/objeto")
	send.BodyText = new("contenido")
	send.Auth = &apiclient.BackendAuth{
		Kind: apiclient.AuthAWSV4, AccessKey: "AKIDEXAMPLE",
		SecretKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
		Region:    "us-east-1", Service: "s3",
	}

	_, err := apiclient.Send(t.Context(), send)
	require.NoError(t, err)

	got := log.seen()[0]
	assert.True(t, strings.HasPrefix(got.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/"))
	assert.NotEmpty(t, got.Header.Get("X-Amz-Date"))
	// The payload hash is of the exact bytes, so the server can verify what it received.
	assert.NotEmpty(t, got.Header.Get("X-Amz-Content-Sha256"))
	assert.Empty(t, got.Header.Get("X-Amz-Security-Token"), "no session token was supplied")
}

// ---- the response ----------------------------------------------------------------------------------

// `API-020`: past the cap the body is **truncated**, and `size_bytes` reports what was kept.
func TestTheResponseCapTruncatesRatherThanFailing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, strings.Repeat("a", 10_000))
	}))
	defer server.Close()

	send := request(http.MethodGet, server.URL+"/x")
	send.Options.MaxResponseBytes = 100

	response, err := apiclient.Send(t.Context(), send)
	require.NoError(t, err)

	assert.Len(t, response.BodyText, 100)
	assert.Equal(t, uint64(100), response.SizeBytes,
		"what was kept, not what the server declared")
}

// Zero is unlimited in this contract, and the renderer sends the field on every request — so a zero
// is a user who asked for no cap, not a missing value.
func TestACapOfZeroIsUnlimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, strings.Repeat("a", 200_000))
	}))
	defer server.Close()

	send := request(http.MethodGet, server.URL+"/x")
	send.Options.MaxResponseBytes = 0

	response, err := apiclient.Send(t.Context(), send)
	require.NoError(t, err)
	assert.Len(t, response.BodyText, 200_000)
}

func TestSetCookiesComeBackParsedButNotStored(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Set-Cookie", "sid=abc; Path=/app; HttpOnly")
		w.Header().Add("Set-Cookie", "csrf=def; Secure")
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	response, err := apiclient.Send(t.Context(), request(http.MethodGet, server.URL+"/v1/login"))
	require.NoError(t, err)

	require.Len(t, response.SetCookies, 2)
	assert.Equal(t, "sid", response.SetCookies[0].Name)
	assert.Equal(t, "/app", response.SetCookies[0].Path)
	assert.True(t, response.SetCookies[0].HTTPOnly)
	assert.True(t, response.SetCookies[1].Secure)
	// Persisting them is the renderer's decision, through the jar commands — this layer only reads.
}

func TestABinaryResponseComesBackAsBase64(t *testing.T) {
	raw := []byte{0x89, 'P', 'N', 'G', 0x00, 0xff}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(raw)
	}))
	defer server.Close()

	response, err := apiclient.Send(t.Context(), request(http.MethodGet, server.URL+"/x"))
	require.NoError(t, err)

	assert.Empty(t, response.BodyText)
	require.NotNil(t, response.BodyBase64)
	assert.Equal(t, base64.StdEncoding.EncodeToString(raw), *response.BodyBase64)
}

func TestTimingsReportUnavailableRatherThanZero(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	response, err := apiclient.Send(t.Context(), request(http.MethodGet, server.URL+"/x"))
	require.NoError(t, err)

	// A `0 ms` DNS lookup is a claim; a dash is the truth. The console draws the distinction.
	assert.Equal(t, int64(-1), response.Timings.DNSMs)
	assert.Equal(t, int64(-1), response.Timings.ConnectMs)
	assert.Equal(t, response.DurationMs, response.Timings.TotalMs)
}

func TestAServersOwnReasonPhraseSurvives(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer server.Close()

	response, err := apiclient.Send(t.Context(), request(http.MethodGet, server.URL+"/x"))
	require.NoError(t, err)
	assert.Equal(t, uint16(418), response.Status)
	assert.NotEmpty(t, response.StatusText)
}

// ---- refusals and cancellation ---------------------------------------------------------------------

func TestAURLThatIsNotAbsoluteIsRefusedByName(t *testing.T) {
	for _, raw := range []string{"", "   ", "/relativa", "example.test/x"} {
		_, err := apiclient.Send(t.Context(), request(http.MethodGet, raw))
		require.Error(t, err, "%q", raw)
		assert.Contains(t, err.Error(), "absolute http(s) URL")
	}
}

func TestAPKCS12ClientCertificateIsRefusedWithTheConversionCommand(t *testing.T) {
	send := request(http.MethodGet, "https://unreachable.invalid/x")
	send.Options.ClientCertPath = "/certs/identidad.p12"

	_, err := apiclient.Send(t.Context(), send)
	require.Error(t, err)
	// The message is the remediation: a silent ignore produces a TLS failure from the far end,
	// which reads as a server problem.
	assert.Contains(t, err.Error(), "openssl pkcs12")
}

func TestAnEncryptedClientKeyIsRefusedWithTheDecryptCommand(t *testing.T) {
	send := request(http.MethodGet, "https://unreachable.invalid/x")
	send.Options.ClientCertPath = "/certs/cliente.pem"
	send.Options.ClientCertPassword = "la-clave"

	_, err := apiclient.Send(t.Context(), send)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "openssl pkcs8")
}

func TestAnUnreadableCABundleNamesTheFile(t *testing.T) {
	send := request(http.MethodGet, "https://unreachable.invalid/x")
	send.Options.CACertPath = filepath.Join(t.TempDir(), "no-existe.pem")

	_, err := apiclient.Send(t.Context(), send)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no-existe.pem")
}

func TestACancelledSendAnswersItsOwnError(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		_, _ = io.WriteString(w, "tarde")
	}))
	defer server.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := apiclient.Send(ctx, request(http.MethodGet, server.URL+"/x"))
	require.Error(t, err)
	assert.ErrorIs(t, err, apiclient.ErrCancelled)
}

func TestATimeoutIsReportedRatherThanHanging(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		_, _ = io.WriteString(w, "tarde")
	}))
	defer server.Close()
	defer close(release)

	send := request(http.MethodGet, server.URL+"/x")
	send.Options.TimeoutMs = 100

	_, err := apiclient.Send(t.Context(), send)
	require.Error(t, err)
	assert.NotErrorIs(t, err, apiclient.ErrCancelled,
		"a timeout and a user pressing stop say opposite things to the reader")
}

func headerOf(headers [][2]string, name string) string {
	for _, header := range headers {
		if strings.EqualFold(header[0], name) {
			return header[1]
		}
	}
	return ""
}
