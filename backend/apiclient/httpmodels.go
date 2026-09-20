package apiclient

// The HTTP transport's wire contract, mirrored field-for-field in `frontend/src/types/api.ts`.
//
// The json tags are the wire names on both sides. Every one of these crossed the IPC boundary in
// 2.x under exactly these spellings, so renaming one here is a breaking change on both sides at
// once — and a silent one, since a renamed field arrives as `undefined` rather than as an error.

// The defaults a request carries when the renderer sends none. Each is a judgement rather than a
// technical limit, and each is the 2.x value.
const (
	// defaultTimeoutMs bounds the whole exchange, redirects and the Digest round trip included.
	defaultTimeoutMs = 30_000
	// defaultMaxRedirects is the hop budget. Shared across the automatic and manual paths.
	defaultMaxRedirects = 10

	// maxTimeoutMs bounds what a timeout can be set to, at a little over a day. Not a rule from the
	// specification: it exists because the value crosses from JSON as a `uint64` and is multiplied
	// by a million to become a duration, which a large enough number turns negative — a request
	// that then times out immediately, reading as a broken transport.
	maxTimeoutMs = 24 * 60 * 60 * 1000
)

// The response cap's default — 50 MiB — is **not** here, deliberately. It belongs to the settings
// screen, which sends `max_response_bytes` on every request; this side reads `0` as *unlimited*, as
// the wire contract says, so a default applied here would override a user who asked for no cap.

// NetworkOptions is everything about the transport that a request can override.
//
// `keep_auth_on_redirect` defaults to **false**, and that default is the interesting one: forwarding
// a bearer token to whatever host a 302 names is a credential leak, and the only person who can say
// it is safe is the one who typed the token.
type NetworkOptions struct {
	TimeoutMs          uint64 `json:"timeout_ms"`
	FollowRedirects    bool   `json:"follow_redirects"`
	MaxRedirects       int    `json:"max_redirects"`
	VerifySSL          bool   `json:"verify_ssl"`
	KeepAuthOnRedirect bool   `json:"keep_auth_on_redirect"`
	// ProxyURL empty means direct.
	ProxyURL           string `json:"proxy_url"`
	ClientCertPath     string `json:"client_cert_path"`
	ClientCertPassword string `json:"client_cert_password"`
	CACertPath         string `json:"ca_cert_path"`
	// Cookies are **pre-matched by the caller** for the URL being sent. No jar lives in this layer:
	// matching by domain, path and expiry is the renderer's, and the table is the store's.
	Cookies          [][2]string `json:"cookies"`
	MaxResponseBytes uint64      `json:"max_response_bytes"`
}

// FormPart is one multipart part. It is a file when FilePath is set and text otherwise.
type FormPart struct {
	Name        string  `json:"name"`
	Value       *string `json:"value"`
	FilePath    *string `json:"file_path"`
	ContentType *string `json:"content_type"`
}

// BackendAuth is the auth this side has to compute, as a union tagged on `kind`.
//
// Only two schemes reach here. Every other one the renderer models — Basic, Bearer, API key, JWT,
// OAuth2 — resolves to a plain header on that side. These two cannot: Digest needs a live challenge,
// and SigV4 needs a canonical form built from the request as it will actually be sent.
type BackendAuth struct {
	Kind string `json:"kind"`

	// Digest.
	Username string `json:"username"`
	Password string `json:"password"`

	// AWS SigV4.
	AccessKey    string `json:"access_key"`
	SecretKey    string `json:"secret_key"`
	SessionToken string `json:"session_token"`
	Region       string `json:"region"`
	Service      string `json:"service"`
}

// The two kinds, `VERBATIM` — the renderer discriminates on them.
const (
	AuthDigest = "digest"
	AuthAWSV4  = "awsv4"
)

// HTTPSendRequest is one fully-resolved request.
//
// "Fully resolved" is the contract: the variables are interpolated, the pre-request script has run
// and the headers are final. Exactly one body field is set, which the renderer guarantees and this
// side does not enforce — it simply picks by a fixed priority if more than one arrives.
type HTTPSendRequest struct {
	Method  string      `json:"method"`
	URL     string      `json:"url"`
	Headers [][2]string `json:"headers"`

	BodyText   *string     `json:"body_text"`
	BodyBase64 *string     `json:"body_base64"`
	BodyFile   *string     `json:"body_file"`
	FormData   []FormPart  `json:"form_data"`
	Urlencoded [][2]string `json:"urlencoded"`

	Auth    *BackendAuth   `json:"auth"`
	Options NetworkOptions `json:"options"`
}

// ResponseTimings is where the time went. `-1` means "unavailable" rather than "zero", which is a
// distinction the console draws: a dash and a `0 ms` say different things about a request.
type ResponseTimings struct {
	DNSMs       int64 `json:"dns_ms"`
	ConnectMs   int64 `json:"connect_ms"`
	TLSMs       int64 `json:"tls_ms"`
	FirstByteMs int64 `json:"first_byte_ms"`
	DownloadMs  int64 `json:"download_ms"`
	TotalMs     int64 `json:"total_ms"`
}

// ParsedCookie is one `Set-Cookie` the response carried, parsed but **not** stored: persisting it
// is the renderer's decision, through the jar commands.
type ParsedCookie struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Domain string `json:"domain"`
	Path   string `json:"path"`
	// Expires is RFC 3339, or null for a session cookie — which is also what an unparseable date
	// reads as, because guessing at one would invent an expiry the server never sent.
	Expires  *string `json:"expires"`
	Secure   bool    `json:"secure"`
	HTTPOnly bool    `json:"http_only"`
}

// SentRequestSummary is what actually went on the wire, headers the transport added included.
//
// The console shows this rather than the request that was handed in, because the two differ in ways
// that matter when something is wrong: `Host`, `Accept-Encoding` and the Digest `Authorization`
// were none of them in the caller's list.
type SentRequestSummary struct {
	Method      string      `json:"method"`
	URL         string      `json:"url"`
	Headers     [][2]string `json:"headers"`
	BodyPreview string      `json:"body_preview"`
}

// HTTPResponse is one exchange's whole result.
type HTTPResponse struct {
	Status      uint16      `json:"status"`
	StatusText  string      `json:"status_text"`
	HTTPVersion string      `json:"http_version"`
	Headers     [][2]string `json:"headers"`
	// BodyText is empty when the body is binary, and BodyBase64 is null when it is text. The two
	// are never both set.
	BodyText   string  `json:"body_text"`
	BodyBase64 *string `json:"body_base64"`
	// SizeBytes is what was **kept**, which past the cap is less than the server declared.
	SizeBytes  uint64          `json:"size_bytes"`
	DurationMs int64           `json:"duration_ms"`
	Timings    ResponseTimings `json:"timings"`
	// Redirects is every hop, the final URL last.
	Redirects  []string           `json:"redirects"`
	SetCookies []ParsedCookie     `json:"set_cookies"`
	Sent       SentRequestSummary `json:"sent"`
}

// withDefaults fills the two options whose zero value is not a choice anybody can make.
//
// **`max_response_bytes` is deliberately not among them**: zero means *unlimited* in this contract,
// and the renderer sends the field on every request (`buildNetworkOptions` has no optional keys), so
// defaulting a zero here would silently cap a user who had explicitly asked for no cap. Nor is
// `verify_ssl` or `follow_redirects`, for the same reason in the other direction — Go cannot tell
// "sent false" from "sent nothing", and the renderer always sends, so `false` is always a choice.
//
// What is left is a 0 ms timeout and a 0-hop redirect budget, neither of which the settings UI can
// produce and both of which would make a request behave as though the transport were broken. They
// only arrive from a hand-built request, which is to say a test.
func (o NetworkOptions) withDefaults() NetworkOptions {
	if o.TimeoutMs == 0 || o.TimeoutMs > maxTimeoutMs {
		o.TimeoutMs = defaultTimeoutMs
	}
	if o.MaxRedirects == 0 {
		o.MaxRedirects = defaultMaxRedirects
	}
	return o
}
