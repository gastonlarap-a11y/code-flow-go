package apiclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The HTTP transport (API-001…024). GraphQL is not a second transport: it is this one, with a JSON
// body POSTed to a single endpoint, and there is no GraphQL-specific code anywhere in this file.

// fileChunkBytes is how a file-backed body is read: 64 KiB at a time, never buffered whole, so a
// multi-gigabyte upload never enters this process's memory (API-018).
const fileChunkBytes = 64 * 1024

// ErrCancelled is what a send the user stopped answers. `VERBATIM`.
var ErrCancelled = errors.New("Request cancelled") //nolint:staticcheck // ST1005: VERBATIM

// Send performs one fully-resolved request (API-001).
//
// **A fresh client per send**, and that is deliberate rather than wasteful: TLS verification, the
// client identity, the CA bundle, the proxy and the redirect policy are all properties of the
// transport, so two requests with different settings cannot share one. Pooling them by settings
// would be a cache keyed on a struct nobody wanted to compare.
func Send(ctx context.Context, request HTTPSendRequest) (HTTPResponse, error) {
	request.Options = request.Options.withDefaults()
	started := time.Now()

	hops := &hopRecorder{max: request.Options.MaxRedirects}

	client, err := buildClient(request.Options, hops)
	if err != nil {
		return HTTPResponse{}, err
	}
	defer client.CloseIdleConnections()

	body, err := prepareBody(request)
	if err != nil {
		return HTTPResponse{}, err
	}

	response, sent, err := runExchange(ctx, client, request, body, hops)
	if err != nil {
		return HTTPResponse{}, err
	}
	defer func() { _ = response.Body.Close() }()

	return readResponse(response, request, sent, hops, started)
}

// ---- the client -----------------------------------------------------------------------------------

func buildClient(options NetworkOptions, hops *hopRecorder) (*http.Client, error) {
	tlsConfig, err := buildTLS(options)
	if err != nil {
		return nil, err
	}

	transport := &http.Transport{
		TLSClientConfig:       tlsConfig,
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConnsPerHost:   4,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		// The encodings are advertised by hand so the console can report them; letting the
		// transport add its own `Accept-Encoding` would mean a header the request summary cannot
		// see. DisableCompression only stops the *automatic* header, not the decoding of a response
		// that arrives compressed.
		DisableCompression: false,
	}

	if proxy := strings.TrimSpace(options.ProxyURL); proxy != "" {
		parsed, err := url.Parse(proxy)
		if err != nil {
			return nil, fmt.Errorf("the proxy URL %q is not a URL: %w", proxy, err)
		}
		transport.Proxy = http.ProxyURL(parsed)
	}

	return &http.Client{
		Transport: transport,
		// `withDefaults` has already bounded this, so the conversion cannot wrap.
		Timeout:       time.Duration(options.TimeoutMs) * time.Millisecond, //nolint:gosec // G115: bounded by maxTimeoutMs
		CheckRedirect: redirectPolicy(options, hops),
	}, nil
}

func buildTLS(options NetworkOptions) (*tls.Config, error) {
	config := &tls.Config{
		//nolint:gosec // G402: the user asked for it, per request, and the console says so
		InsecureSkipVerify: !options.VerifySSL,
		MinVersion:         tls.VersionTLS12,
	}

	if path := strings.TrimSpace(options.CACertPath); path != "" {
		pem, err := os.ReadFile(path) //nolint:gosec // G304: a path the user picked in a dialog
		if err != nil {
			return nil, fmt.Errorf("read the CA bundle %s: %w", path, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("%s holds no PEM certificate this client could read", path) //nolint:err113 // names the file the user picked
		}
		config.RootCAs = pool
	}

	identity, err := clientIdentity(options)
	if err != nil {
		return nil, err
	}
	if identity != nil {
		config.Certificates = []tls.Certificate{*identity}
	}
	return config, nil
}

// clientIdentity loads the client certificate, refusing the two forms this TLS stack cannot read
// (API-002).
//
// Both refusals name the exact `openssl` command that fixes them. A silent ignore would produce a
// TLS handshake failure from the far end, which reads as a server problem.
func clientIdentity(options NetworkOptions) (*tls.Certificate, error) {
	path := strings.TrimSpace(options.ClientCertPath)
	if path == "" {
		return nil, nil //nolint:nilnil // no client certificate is the ordinary case
	}

	switch strings.ToLower(filepath.Ext(path)) {
	case ".p12", ".pfx":
		return nil, fmt.Errorf( //nolint:err113 // the message is the remediation
			"%s is a PKCS#12 bundle, which this client cannot read; convert it first: "+
				"openssl pkcs12 -in %s -out client.pem -nodes", path, path)
	}
	if strings.TrimSpace(options.ClientCertPassword) != "" {
		return nil, fmt.Errorf( //nolint:err113 // the message is the remediation
			"this client cannot decrypt an encrypted private key; decrypt it first: "+
				"openssl pkcs8 -topk8 -nocrypt -in %s -out client.pem", path)
	}

	certificate, err := tls.LoadX509KeyPair(path, path)
	if err != nil {
		return nil, fmt.Errorf("read the client certificate %s: %w", path, err)
	}
	return &certificate, nil
}

// ---- redirects -------------------------------------------------------------------------------------

// hopRecorder is the one hop counter both the automatic and the manual path read (API-006).
//
// One budget, not two: whichever path handled a hop, it grew by exactly one, so `max_redirects`
// means the same number however the chain was followed.
type hopRecorder struct {
	urls []string
	max  int
}

func (h *hopRecorder) record(target string) { h.urls = append(h.urls, target) }
func (h *hopRecorder) taken() int           { return len(h.urls) }

// redirectPolicy is custom rather than a hop count, because `keep_auth_on_redirect` has to
// intercept a cross-host hop **before** the client strips credentials from it (API-003).
//
// The two ways out of this function say different things to the client, and the difference is the
// whole mechanism: `http.ErrUseLastResponse` hands the current 3xx back **as if it were final**,
// which is what lets the manual path re-drive it with the headers intact; any other error fails the
// request outright, which is what the hop cap wants.
func redirectPolicy(options NetworkOptions, hops *hopRecorder) func(*http.Request, []*http.Request) error {
	if !options.FollowRedirects {
		// Never followed: the caller gets the raw 3xx, which is what they asked to see.
		return func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}

	return func(request *http.Request, via []*http.Request) error {
		if len(via) > options.MaxRedirects {
			return fmt.Errorf("more than %d redirects", options.MaxRedirects) //nolint:err113 // VERBATIM
		}

		// A cross-host hop with credentials to keep is stopped here and resumed by hand, precisely
		// so the client does not get the chance to strip `Authorization`, `Cookie` and
		// `Proxy-Authorization` the way it does on every cross-host hop by default.
		if options.KeepAuthOnRedirect && len(via) > 0 && !sameOrigin(via[len(via)-1].URL, request.URL) {
			return http.ErrUseLastResponse
		}

		hops.record(request.URL.String())
		return nil
	}
}

// sameOrigin compares host and port, with the scheme's default port filled in — so
// `https://x.test` and `https://x.test:443` are one origin, as they are to a browser.
func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Hostname(), b.Hostname()) && portOf(a) == portOf(b)
}

func portOf(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if strings.EqualFold(u.Scheme, "https") {
		return "443"
	}
	return "80"
}

// manualRedirectTarget answers the next hop for the one case the policy above stopped at
// (API-004).
//
// Three conditions, all required: following is on, credentials are being kept, and the response is
// a redirection. Every other configuration has already had its whole chain followed by the time the
// client returned.
func manualRedirectTarget(response *http.Response, current *url.URL, options NetworkOptions) (*url.URL, error) {
	if !options.FollowRedirects || !options.KeepAuthOnRedirect || !isRedirect(response.StatusCode) {
		return nil, nil //nolint:nilnil // no further hop is the ordinary answer
	}

	location := response.Header.Get("Location")
	if location == "" {
		return nil, nil //nolint:nilnil // a 3xx with no Location is the final response
	}

	target, err := current.Parse(location)
	if err != nil {
		// Explicit rather than mangled: a Location this cannot read is a server's mistake, and
		// guessing at it would send the request somewhere nobody named.
		return nil, fmt.Errorf("the server's Location header %q is not a URL: %w", location, err)
	}
	return target, nil
}

func isRedirect(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

// redirectedMethod is the manual path's method and body transform (API-005).
//
// **`BUG-API-a` is preserved here.** The rule reads "301/302 downgrade any method other than
// GET/HEAD to a bodiless GET", which is broader than the browser behaviour the original's comment
// claimed to replicate — a redirected PUT or DELETE is downgraded too, not only a POST.
// Suspected-correct is POST only. Ported as-is; not fixed.
//
// Reachable only when `keep_auth_on_redirect` is on **and** the hop crosses hosts: the default
// configuration never runs this at all.
func redirectedMethod(status int, method string) (nextMethod string, withBody bool) {
	switch status {
	case http.StatusSeeOther:
		return http.MethodGet, false
	case http.StatusMovedPermanently, http.StatusFound:
		if method != http.MethodGet && method != http.MethodHead {
			return http.MethodGet, false
		}
		return method, true
	default:
		return method, true
	}
}

// ---- the exchange ----------------------------------------------------------------------------------

// runExchange sends, and re-drives one hop by hand when — and only when — the policy stopped at a
// cross-host redirect it was told to keep credentials across (API-004).
func runExchange(
	ctx context.Context,
	client *http.Client,
	request HTTPSendRequest,
	body preparedBody,
	hops *hopRecorder,
) (*http.Response, SentRequestSummary, error) {
	target, err := parseRequestURL(request.URL)
	if err != nil {
		return nil, SentRequestSummary{}, err
	}

	method := request.Method
	withBody := true

	for {
		response, sent, err := sendOnce(ctx, client, request, body, method, target, withBody)
		if err != nil {
			return nil, SentRequestSummary{}, err
		}

		next, err := manualRedirectTarget(response, target, request.Options)
		if err != nil {
			_ = response.Body.Close()
			return nil, SentRequestSummary{}, err
		}
		if next == nil {
			return response, sent, nil
		}

		if hops.taken() >= request.Options.MaxRedirects {
			_ = response.Body.Close()
			return nil, SentRequestSummary{}, fmt.Errorf( //nolint:err113 // VERBATIM
				"%s %s went through more than %d redirects",
				method, request.URL, request.Options.MaxRedirects)
		}
		hops.record(next.String())

		_ = response.Body.Close()
		method, withBody = redirectedMethod(response.StatusCode, method)
		target = next
	}
}

// sendOnce builds and performs one request, handling the Digest round trip (API-007).
//
// The re-send targets wherever the **first** attempt actually landed, not the originally-typed URL:
// the nonce and the signed request-target belong to whoever issued the challenge.
func sendOnce(
	ctx context.Context,
	client *http.Client,
	request HTTPSendRequest,
	body preparedBody,
	method string,
	target *url.URL,
	withBody bool,
) (*http.Response, SentRequestSummary, error) {
	built, err := buildRequest(ctx, request, body, method, target, withBody)
	if err != nil {
		return nil, SentRequestSummary{}, err
	}
	sent := summarise(built, body)

	response, err := client.Do(built)
	if err != nil {
		return nil, SentRequestSummary{}, transportError(err)
	}

	if request.Auth == nil || request.Auth.Kind != AuthDigest ||
		response.StatusCode != http.StatusUnauthorized {
		return response, sent, nil
	}

	challenge, found := digestChallenge(response.Header)
	if !found {
		_ = response.Body.Close()
		return nil, SentRequestSummary{}, fmt.Errorf( //nolint:err113 // VERBATIM
			"%s %s returned 401 but no 'WWW-Authenticate: Digest' challenge, so the digest handshake cannot continue",
			method, target)
	}

	landed := response.Request.URL
	authorization, err := digestAuthorization(*request.Auth, challenge, method, digestURI(landed.String()))
	_ = response.Body.Close()
	if err != nil {
		return nil, SentRequestSummary{}, err
	}

	authenticated, err := buildRequest(ctx, request, body, method, landed, withBody)
	if err != nil {
		return nil, SentRequestSummary{}, err
	}
	authenticated.Header.Set("Authorization", authorization)
	sent = summarise(authenticated, body)

	response, err = client.Do(authenticated)
	if err != nil {
		return nil, SentRequestSummary{}, transportError(err)
	}
	return response, sent, nil
}

// transportError maps the two failures the caller can act on differently, and leaves the rest as
// the transport's own words — which are what `StatusText.Reason` used to find for the message a
// user reads.
func transportError(err error) error {
	if errors.Is(err, context.Canceled) {
		return ErrCancelled
	}
	return fmt.Errorf("the request failed before a response: %w", err)
}

// buildRequest assembles one attempt.
func buildRequest(
	ctx context.Context,
	request HTTPSendRequest,
	body preparedBody,
	method string,
	target *url.URL,
	withBody bool,
) (*http.Request, error) {
	var reader io.ReadCloser
	var length int64

	if withBody {
		opened, size, err := body.open()
		if err != nil {
			return nil, err
		}
		reader, length = opened, size
	}

	built, err := http.NewRequestWithContext(ctx, method, target.String(), reader)
	if err != nil {
		return nil, fmt.Errorf("build the request: %w", err)
	}
	if withBody && length >= 0 {
		// Set explicitly from the file's own length so the transport does not fall back to chunked
		// transfer encoding, which some servers refuse for an upload.
		built.ContentLength = length
	}

	for _, header := range request.Headers {
		// Multipart discards any caller content-type: the boundary is generated inside the writer
		// and only its own value can delimit the body (API-017).
		if body.multipart && strings.EqualFold(header[0], "content-type") {
			continue
		}
		built.Header.Add(header[0], header[1])
	}
	if withBody && body.contentType != "" && built.Header.Get("Content-Type") == "" {
		built.Header.Set("Content-Type", body.contentType)
	}
	if body.multipart {
		built.Header.Set("Content-Type", body.contentType)
	}

	// The caller's pre-matched jar, appended only when they have not set the header themselves.
	if len(request.Options.Cookies) > 0 && built.Header.Get("Cookie") == "" {
		pairs := make([]string, 0, len(request.Options.Cookies))
		for _, cookie := range request.Options.Cookies {
			pairs = append(pairs, cookie[0]+"="+cookie[1])
		}
		built.Header.Set("Cookie", strings.Join(pairs, "; "))
	}
	if built.Header.Get("Accept-Encoding") == "" {
		built.Header.Set("Accept-Encoding", advertisedEncodings)
	}

	if request.Auth != nil && request.Auth.Kind == AuthAWSV4 {
		signed, err := sigv4Headers(method, target.String(), headerPairs(built.Header),
			body.payloadHash, *request.Auth, time.Now().UTC().Format("20060102T150405Z"))
		if err != nil {
			return nil, err
		}
		for _, header := range signed {
			built.Header.Set(header[0], header[1])
		}
	}
	return built, nil
}

// parseRequestURL rejects what is not an absolute HTTP URL before anything is dialled.
func parseRequestURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("%q is not a URL: %w", raw, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("%q is not an absolute http(s) URL", raw) //nolint:err113 // names what the user typed
	}
	return parsed, nil
}

// headerPairs flattens a header map in a stable order, so a signature is reproducible.
func headerPairs(header http.Header) [][2]string {
	pairs := make([][2]string, 0, len(header))
	for name, values := range header {
		for _, value := range values {
			pairs = append(pairs, [2]string{name, value})
		}
	}
	return pairs
}

// summarise is the console's honest record of what went out (`wire_headers`).
//
// It reads the **built** request, so `Host`, `Accept-Encoding` and a Digest `Authorization` — none
// of which were in the caller's list — appear where a reader looking for them expects to find them.
func summarise(built *http.Request, body preparedBody) SentRequestSummary {
	// `Host` is never in the header map — it travels in the request's own field — so it is
	// synthesised here rather than read back, exactly as `wire_headers` does.
	headers := append(headerPairs(built.Header), [2]string{"Host", built.URL.Host})

	return SentRequestSummary{
		Method:      built.Method,
		URL:         built.URL.String(),
		Headers:     headers,
		BodyPreview: body.preview,
	}
}

// ---- the body --------------------------------------------------------------------------------------

// preparedBody is one request's body, ready to be opened once per attempt.
//
// `open` rather than a byte slice, because the same body is sent twice on a Digest handshake and
// once more per manual redirect hop — and a file-backed one must stream each time rather than be
// buffered so it can be replayed.
type preparedBody struct {
	open        func() (io.ReadCloser, int64, error)
	contentType string
	preview     string
	payloadHash string
	multipart   bool
}

// prepareBody picks exactly one representation, by a fixed priority (API-016).
//
// Nothing enforces that only one arrives: the renderer's own type guarantees it, and a backend
// check would be a second place for the rule to live. The order is the contract.
func prepareBody(request HTTPSendRequest) (preparedBody, error) {
	needsHash := request.Auth != nil && request.Auth.Kind == AuthAWSV4

	switch {
	case request.BodyText != nil:
		return bytesBody([]byte(*request.BodyText), "", previewText(*request.BodyText), needsHash), nil

	case request.BodyBase64 != nil:
		raw, err := base64.StdEncoding.DecodeString(*request.BodyBase64)
		if err != nil {
			return preparedBody{}, fmt.Errorf("the base64 body could not be decoded: %w", err)
		}
		return bytesBody(raw, "", previewBytes(raw), needsHash), nil

	case request.BodyFile != nil:
		return fileBody(*request.BodyFile, needsHash)

	case len(request.Urlencoded) > 0:
		form := url.Values{}
		for _, pair := range request.Urlencoded {
			form.Add(pair[0], pair[1])
		}
		encoded := form.Encode()
		return bytesBody([]byte(encoded), "application/x-www-form-urlencoded",
			previewText(encoded), needsHash), nil

	case len(request.FormData) > 0:
		return multipartBody(request.FormData)

	default:
		return bytesBody(nil, "", "", needsHash), nil
	}
}

func bytesBody(content []byte, contentType, preview string, needsHash bool) preparedBody {
	body := preparedBody{
		open: func() (io.ReadCloser, int64, error) {
			return io.NopCloser(bytes.NewReader(content)), int64(len(content)), nil
		},
		contentType: contentType,
		preview:     preview,
	}
	if needsHash {
		body.payloadHash = hex.EncodeToString(sha256Of(content))
	}
	return body
}

// fileBody streams from disk in 64 KiB reads (API-018), and hashes it in a **second pass** only
// when a SigV4 signature needs one (API-015) — so a plain upload never pays for it.
func fileBody(path string, needsHash bool) (preparedBody, error) {
	info, err := os.Stat(path)
	if err != nil {
		return preparedBody{}, fmt.Errorf("read the body file %s: %w", path, err)
	}
	if info.IsDir() {
		return preparedBody{}, fmt.Errorf("'%s' is a directory, not a file", path) //nolint:err113 // VERBATIM
	}

	body := preparedBody{
		open: func() (io.ReadCloser, int64, error) {
			file, err := os.Open(path) //nolint:gosec // G304: a path the user picked in a dialog
			if err != nil {
				return nil, 0, fmt.Errorf("open the body file %s: %w", path, err)
			}
			return file, info.Size(), nil
		},
		// Previewed without ever reading the content: the console says what is being streamed and
		// from where, which is the useful fact about a two-gigabyte upload.
		preview: fmt.Sprintf("<%d bytes streamed from %s>", info.Size(), path),
	}

	if needsHash {
		hash, err := hashFile(path)
		if err != nil {
			return preparedBody{}, err
		}
		body.payloadHash = hash
	}
	return body, nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path) //nolint:gosec // G304: a path the user picked in a dialog
	if err != nil {
		return "", fmt.Errorf("open the body file %s to hash it: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	digest := sha256.New()
	if _, err := io.CopyBuffer(digest, file, make([]byte, fileChunkBytes)); err != nil {
		return "", fmt.Errorf("hash the body file %s: %w", path, err)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// multipartBody assembles the parts (API-017).
//
// Buffered rather than streamed, unlike a single-file body: the parts have to be reassembled
// identically on a Digest re-send and on every manual redirect hop, and a writer cannot be rewound.
// The payload hash is `UNSIGNED-PAYLOAD` because AWS accepts it for exactly this case.
func multipartBody(parts []FormPart) (preparedBody, error) {
	buffer := &bytes.Buffer{}
	writer := multipart.NewWriter(buffer)
	preview := &strings.Builder{}

	for _, part := range parts {
		switch {
		case part.FilePath != nil && *part.FilePath != "":
			if err := writeFilePart(writer, part); err != nil {
				return preparedBody{}, err
			}
			fmt.Fprintf(preview, "%s=<file %s>; ", part.Name, *part.FilePath)

		default:
			value := ""
			if part.Value != nil {
				value = *part.Value
			}
			if err := writer.WriteField(part.Name, value); err != nil {
				return preparedBody{}, fmt.Errorf("write the form field %s: %w", part.Name, err)
			}
			fmt.Fprintf(preview, "%s=%s; ", part.Name, value)
		}
	}

	if err := writer.Close(); err != nil {
		return preparedBody{}, fmt.Errorf("close the multipart body: %w", err)
	}

	content := buffer.Bytes()
	return preparedBody{
		open: func() (io.ReadCloser, int64, error) {
			return io.NopCloser(bytes.NewReader(content)), int64(len(content)), nil
		},
		contentType: writer.FormDataContentType(),
		preview:     previewText(strings.TrimSuffix(preview.String(), "; ")),
		payloadHash: unsignedPayload,
		multipart:   true,
	}, nil
}

func writeFilePart(writer *multipart.Writer, part FormPart) error {
	file, err := os.Open(*part.FilePath) //nolint:gosec // G304: a path the user picked in a dialog
	if err != nil {
		return fmt.Errorf("open the form file %s: %w", *part.FilePath, err)
	}
	defer func() { _ = file.Close() }()

	header := make(map[string][]string, 2)
	header["Content-Disposition"] = []string{fmt.Sprintf(
		`form-data; name=%q; filename=%q`, part.Name, filepath.Base(*part.FilePath))}
	// A part with no declared type gets no override, which lets the server infer one — or none.
	if part.ContentType != nil && *part.ContentType != "" {
		header["Content-Type"] = []string{*part.ContentType}
	}

	target, err := writer.CreatePart(header)
	if err != nil {
		return fmt.Errorf("start the form part %s: %w", part.Name, err)
	}
	if _, err := io.CopyBuffer(target, file, make([]byte, fileChunkBytes)); err != nil {
		return fmt.Errorf("stream the form file %s: %w", *part.FilePath, err)
	}
	return nil
}

// ---- the response ----------------------------------------------------------------------------------

// readResponse reads the body under its cap and decodes it (API-020, API-021).
func readResponse(
	response *http.Response,
	request HTTPSendRequest,
	sent SentRequestSummary,
	hops *hopRecorder,
	started time.Time,
) (HTTPResponse, error) {
	body, err := readCapped(response.Body, request.Options.MaxResponseBytes)
	if err != nil {
		return HTTPResponse{}, err
	}
	elapsed := time.Since(started).Milliseconds()

	contentType := response.Header.Get("Content-Type")
	text, encoded := decodeBody(body, contentType)

	// The final URL closes the hop list, unless it is already the last one recorded.
	redirects := append([]string{}, hops.urls...)
	if final := response.Request.URL.String(); len(redirects) == 0 || redirects[len(redirects)-1] != final {
		redirects = append(redirects, final)
	}

	return HTTPResponse{
		Status:      uint16(response.StatusCode), //nolint:gosec // G115: an HTTP status is three digits
		StatusText:  statusTextOf(response),
		HTTPVersion: response.Proto,
		Headers:     headerPairs(response.Header),
		BodyText:    text,
		BodyBase64:  encoded,
		SizeBytes:   uint64(len(body)),
		DurationMs:  elapsed,
		// `-1` throughout rather than zero: this transport exposes no connection trace, and a `0 ms`
		// DNS lookup is a claim, where a dash is the truth.
		Timings: ResponseTimings{
			DNSMs: -1, ConnectMs: -1, TLSMs: -1, FirstByteMs: -1, DownloadMs: -1,
			TotalMs: elapsed,
		},
		Redirects:  redirects,
		SetCookies: parseSetCookies(response.Header.Values("Set-Cookie"), response.Request.URL.String(), time.Now()),
		Sent:       sent,
	}, nil
}

// readCapped reads up to the cap and **stops**, which is a truncation and not an error (API-020).
//
// `size_bytes` then reports what was kept rather than what the server declared, because what the
// reader has in front of them is the truncated body.
func readCapped(body io.Reader, cap uint64) ([]byte, error) {
	if cap == 0 {
		// Zero is unlimited in this contract, and the renderer sends the field on every request —
		// so a zero here is a user who asked for no cap, not a missing value.
		content, err := io.ReadAll(body)
		if err != nil {
			return nil, fmt.Errorf("read the response body: %w", err)
		}
		return content, nil
	}

	content, err := io.ReadAll(io.LimitReader(body, int64(cap))) //nolint:gosec // G115: a byte cap
	if err != nil {
		return nil, fmt.Errorf("read the response body: %w", err)
	}
	return content, nil
}

// statusTextOf prefers the server's own reason phrase over the canonical one: a server that answers
// `418 Soy una tetera` said something, and replacing it with the registry's text loses it.
func statusTextOf(response *http.Response) string {
	_, reason, found := strings.Cut(response.Status, " ")
	if found && strings.TrimSpace(reason) != "" {
		return reason
	}
	return http.StatusText(response.StatusCode)
}
