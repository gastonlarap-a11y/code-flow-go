// Package security owns the credential store and the staged-secret scanner.
//
// Every credential CodeFlow holds — Azure DevOps PATs, GitHub tokens, AI provider API keys,
// database passwords — lives in the OS credential store and nowhere else. There is no filesystem
// fallback and no encrypted-blob-in-SQLite path, deliberately: a fallback is a place where a
// secret ends up when the real store is unavailable, and nobody notices until it is read by
// something else (SEC-004).
//
// The consequence is that a store failure is **loud**. It reaches the renderer as an error rather
// than as "no credential", because those two mean different things to the user: one says
// reconnect, the other says your keychain is locked.
package security

import (
	"errors"
	"fmt"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/sentinel"
)

// Service is the keychain service name every credential is filed under (SEC-001).
//
// A compile-time constant, never parameterised, and byte-identical to what 2.x wrote. It is also
// the macOS bundle identifier and the single-instance UniqueID — the same string in three places,
// and changing it here makes every existing user's saved tokens unreadable while the app cheerfully
// reports "no credential saved".
const Service = "com.codeflow.app"

// The four key formats (SEC-002). VERBATIM: reproducing them byte-for-byte is what lets 3.0 read
// what 2.7.x stored.
//
// Each is scoped to what the secret actually belongs to. A PAT is organisation-scoped, so Azure
// DevOps is keyed per org. A GitHub token is account-wide across every repository a host serves,
// so GitHub is keyed per host — which also leaves room for a GitHub Enterprise host. The AI key is
// per provider, because several are configurable side by side. The database password is keyed by
// the connection's **id** rather than its host: a connection is what the user named and edits, so
// renaming its host must not strand the password, and two logins to one server are two secrets.
//
// No validation, sanitisation or escaping — whatever string is passed becomes part of the key
// verbatim, exactly as in 2.x. An org containing a colon produces a key with two colons, and
// nothing prevents it.
func ADOPATKey(org string) string              { return "ado-pat:" + org }
func GitHubTokenKey(host string) string        { return "github-token:" + host }
func AIAPIKey(provider string) string          { return "ai-api-key:" + provider }
func DBPasswordKey(connectionID string) string { return "db-password:" + connectionID }

// ErrNoEntry means the credential is simply not there. It is not a failure (SEC-003): a read maps
// it to "none" and a delete maps it to success, because deleting something already absent is the
// state the caller asked for.
var ErrNoEntry = errors.New("no credential stored")

// ErrRefused means the store was reached and said no — a locked keychain, a denied prompt, a
// cancelled dialog. Distinct from ErrNoEntry because the user can act on it, and it is what
// carries the CREDENTIAL_REFUSED sentinel to the renderer.
var ErrRefused = errors.New("the credential store refused access")

// ErrUnsupported is Linux: there is no backend at all. It throws rather than silently falling back
// to plaintext (SEC-004), which is the only honest behaviour when the alternative is writing a
// token to a file.
var ErrUnsupported = errors.New("no OS credential store is available on this platform")

// backend is the per-OS implementation. Declared here, at the consumer, and satisfied by exactly
// one file per platform through build tags.
type backend interface {
	get(service, key string) (string, error)
	set(service, key, secret string) error
	delete(service, key string) error
}

// Store reads and writes credentials. It holds no secret itself and caches nothing: a cached
// token outlives the moment the user revoked it.
type Store struct{ backend backend }

// NewStore returns the store for this platform.
func NewStore() *Store { return &Store{backend: osBackend()} }

// Get returns a stored secret, or ErrNoEntry when there is none.
func (s *Store) Get(key string) (string, error) {
	secret, err := s.backend.get(Service, key)
	if err != nil {
		return "", err
	}
	return secret, nil
}

// Has reports whether a non-empty secret is stored.
//
// Non-empty rather than merely present: an empty value is what a half-finished save leaves behind,
// and the Settings screen showing "configured" for one is worse than showing nothing.
func (s *Store) Has(key string) (bool, error) {
	secret, err := s.Get(key)
	if errors.Is(err, ErrNoEntry) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return secret != "", nil
}

// Set stores a secret, replacing any existing one.
func (s *Store) Set(key, secret string) error { return s.backend.set(Service, key, secret) }

// Delete removes a secret. Deleting one that was never stored succeeds.
func (s *Store) Delete(key string) error {
	err := s.backend.delete(Service, key)
	if errors.Is(err, ErrNoEntry) {
		return nil
	}
	return err
}

// AsCommandError translates a store failure into what the renderer expects (XLANG-012).
//
// Applied at the command boundary and nowhere else. Inside the process, callers branch on
// ErrRefused with errors.Is; the sentinel exists so the renderer can offer "reconnect" instead of
// rendering a red banner, and it only works if it sits at position 0 of the message.
func AsCommandError(err error) error {
	if err == nil || errors.Is(err, ErrNoEntry) {
		return nil
	}
	if errors.Is(err, ErrRefused) {
		return errors.New(sentinel.CredentialRefused + refusalDetail(err))
	}
	return err
}

// refusalDetail keeps the platform's own words after the sentinel — "User interaction is not
// allowed" says something a generic message does not — while making sure the sentinel is not
// repeated if it already made it into the text.
func refusalDetail(err error) string {
	message := err.Error()
	message = strings.TrimPrefix(message, sentinel.CredentialRefused)
	if message == "" {
		return "the credential store refused access"
	}
	return message
}

// refused wraps a platform error as a refusal, preserving its text.
func refused(reason string) error {
	return fmt.Errorf("%s: %w", reason, ErrRefused)
}
