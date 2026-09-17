// Package bridge is the single door between the renderer and the backend.
//
// # Why a command registry rather than idiomatic Wails bindings
//
// Wails' own idiom is one service per domain and one bound method per operation, with positional
// arguments and generated TypeScript. This port does not use it, on purpose.
//
// The renderer already owns 246 typed wrappers over invoke(name, params), imported from 125 files.
// The specification, the 24 test-vector files and the golden fixtures are all keyed by command
// name. The error strings carry sentinel prefixes the renderer matches at position 0. Per-method
// bindings would change the shape of every call site (positional arguments instead of a camelCase
// object), regenerate TypeScript that competes with the hand-written types/domain.ts, and leave no
// single place to prove the wire contract is unchanged.
//
// One Invoke(method, params) keeps the renderer's 246 wrappers untouched and reduces "did we port
// every command?" to one table-driven test (§9.4 #1).
//
// # Dependency direction
//
// bridge knows no feature. Features depend on bridge — each exposes Register(r *Registry, deps) —
// and receive an Emitter interface rather than the Wails application, so every feature is testable
// without a window. Nothing under backend/<feature> imports Wails; that dependency lives in
// backend/desktop and main.go alone, which is what keeps the beta's API churn contained.
package bridge

import (
	"context"
	"sort"
)

// Handler runs one command.
//
// The returned value is marshalled with its own struct tags; a nil value reaches the renderer as
// null, exactly like the C# void commands did. The returned error's message crosses to the
// renderer unchanged, which is why sentinel prefixes live at its start and why a handler must
// never wrap one.
type Handler func(ctx context.Context, p Params) (any, error)

// Registry maps a command name to its handler.
//
// It is built once during composition and sealed. Both failure modes are programming errors that
// can only be caught here — a duplicate registration silently shadows a command, and a late one
// creates a command that exists only after some code path has run — so both panic rather than
// returning an error nobody would check at start-up.
type Registry struct {
	handlers map[string]Handler
	sealed   bool
}

// NewRegistry returns an empty, unsealed registry.
func NewRegistry() *Registry {
	return &Registry{handlers: make(map[string]Handler, 256)}
}

// Add registers one command. It panics on a duplicate name or after Seal.
func (r *Registry) Add(name string, h Handler) {
	if r.sealed {
		panic("bridge: the registry is sealed; cannot add " + name)
	}
	if name == "" {
		panic("bridge: a command needs a name")
	}
	if h == nil {
		panic("bridge: command " + name + " has no handler")
	}
	if _, duplicate := r.handlers[name]; duplicate {
		panic("bridge: duplicate command " + name)
	}
	r.handlers[name] = h
}

// Seal closes the registry. main calls it after the last feature has registered.
func (r *Registry) Seal() { r.sealed = true }

// Sealed reports whether Seal has been called.
func (r *Registry) Sealed() bool { return r.sealed }

// Lookup returns the handler for a command.
func (r *Registry) Lookup(name string) (Handler, bool) {
	h, ok := r.handlers[name]
	return h, ok
}

// Names lists every registered command, sorted. The contract test (§9.4 #1) compares this against
// the command names the renderer actually calls, which is what turns "we forgot one" from a bug
// report into a failing build.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.handlers))
	for name := range r.handlers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Len is the number of registered commands.
func (r *Registry) Len() int { return len(r.handlers) }
