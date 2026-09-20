// Package apiclient is the built-in API workbench's backend: the collections, environments,
// history and cookie jar it stores, and the transports a webview cannot open for itself.
//
// **This package is a transport, not a model.** It never resolves a `{{variable}}`, never runs a
// pre-request script and never decides what a request should contain — the renderer interpolates,
// scripts and builds the final headers, then hands down a fully-resolved request. What lives here
// is only what a webview genuinely cannot do: raw sockets, auth that needs the wire (Digest's
// challenge, SigV4's canonical form), and the transport knobs `fetch` does not expose.
//
// The stores are the other half, and they are thin on purpose: a request's editable content is one
// opaque `spec` JSON blob, so adding a protocol, an auth scheme or a body mode never needs a
// migration and never needs this package to understand it.
package apiclient

// The stored shapes, each mirrored field-for-field in `frontend/src/types/api.ts`. The json tags
// are the wire names on both sides, so renaming one here is a breaking change on both.

// Collection is one top-level group of requests.
type Collection struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// Auth is a JSON `AuthConfig`; empty means nothing configured, and children then fall through
	// to "none". Opaque here — this package never reads inside it.
	Auth       string `json:"auth"`
	PreScript  string `json:"pre_script"`
	PostScript string `json:"post_script"`
	// Variables is a JSON `ApiVariable[]`, collection-scoped.
	Variables string `json:"variables"`
	SortOrder int64  `json:"sort_order"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// Folder nests arbitrarily inside a collection.
//
// A separate table from requests rather than one node table with a kind column, because a folder
// carries its own auth and scripts that the requests under it inherit — and because the tree always
// renders folders above requests, so the two have independent `sort_order` sequences.
type Folder struct {
	ID           string `json:"id"`
	CollectionID string `json:"collection_id"`
	// ParentID is null for a folder directly under the collection.
	ParentID    *string `json:"parent_id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Auth        string  `json:"auth"`
	PreScript   string  `json:"pre_script"`
	PostScript  string  `json:"post_script"`
	SortOrder   int64   `json:"sort_order"`
	CreatedAt   string  `json:"created_at"`
}

// Request is one saved request.
type Request struct {
	ID           string `json:"id"`
	CollectionID string `json:"collection_id"`
	// FolderID is null for a request directly under the collection.
	FolderID *string `json:"folder_id"`
	Name     string  `json:"name"`
	Protocol string  `json:"protocol"`
	// Method and URL are denormalised out of Spec purely so the tree can render a row without
	// parsing every blob.
	Method string `json:"method"`
	URL    string `json:"url"`
	// Spec is the renderer's serialised `ApiRequestSpec`, opaque here.
	Spec      string `json:"spec"`
	SortOrder int64  `json:"sort_order"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// Tree is everything the sidebar needs, in one round trip. The renderer nests it itself.
type Tree struct {
	Collections []Collection `json:"collections"`
	Folders     []Folder     `json:"folders"`
	Requests    []Request    `json:"requests"`
}

// Environment is one set of variables.
type Environment struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	// Variables is a JSON `ApiVariable[]`.
	Variables string `json:"variables"`
	// IsGlobal marks the one Globals pseudo-environment per workspace: always in scope, and
	// neither deletable nor switchable away from.
	IsGlobal  bool   `json:"is_global"`
	SortOrder int64  `json:"sort_order"`
	CreatedAt string `json:"created_at"`
}

// HistoryEntry is one send, whether or not it came from a saved request.
type HistoryEntry struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	// RequestID is null for an ad-hoc send.
	RequestID  *string `json:"request_id"`
	Name       string  `json:"name"`
	Protocol   string  `json:"protocol"`
	Method     string  `json:"method"`
	URL        string  `json:"url"`
	Status     *int64  `json:"status"`
	DurationMs *int64  `json:"duration_ms"`
	SizeBytes  *int64  `json:"size_bytes"`
	// Snapshot holds the full request spec and response, so an old entry can be replayed or
	// restored into the builder exactly as it ran.
	Snapshot  string `json:"snapshot"`
	CreatedAt string `json:"created_at"`
}

// Cookie is one row of the jar.
//
// Persisted rather than held in an HTTP client, because that client is rebuilt per request —
// per-request TLS, proxy and redirect overrides make a shared one impossible — so nothing in the
// transport layer can hold jar state across sends.
type Cookie struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	Domain      string `json:"domain"`
	Path        string `json:"path"`
	Name        string `json:"name"`
	Value       string `json:"value"`
	Secure      bool   `json:"secure"`
	HTTPOnly    bool   `json:"http_only"`
	// Expires is RFC 3339, or null for a session cookie.
	Expires   *string `json:"expires"`
	UpdatedAt string  `json:"updated_at"`
}

// FileBase64 is a file read for a binary body or a file form-part.
type FileBase64 struct {
	Base64 string `json:"base64"`
	Mime   string `json:"mime"`
	Size   int64  `json:"size"`
}
