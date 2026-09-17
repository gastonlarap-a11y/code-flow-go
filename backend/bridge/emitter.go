package bridge

import "sync"

// Emitter is how a feature pushes an event to the renderer.
//
// It is an interface with exactly one method so that no feature package has to import Wails. The
// real implementation lives in backend/desktop and forwards to app.Event.Emit; a test passes a
// RecordingEmitter and asserts on what was emitted, with no window and no runtime.
//
// The ten event names and their payload shapes are in 01-ipc-surface.md. Two properties carried
// over from 2.x: events are broadcast (there is one window, and every payload already carries its
// own id — run_id, id, repo_path), and an event emitted before the renderer subscribes is lost.
type Emitter interface {
	Emit(name string, payload any)
}

// EmitterFunc adapts a function to the interface.
type EmitterFunc func(name string, payload any)

// Emit implements Emitter.
func (f EmitterFunc) Emit(name string, payload any) { f(name, payload) }

// Event is one recorded emission.
type Event struct {
	Name    string
	Payload any
}

// RecordingEmitter captures events for assertions. It is safe for concurrent use because the
// producers under test — run registries, watchers, stream connectors — emit from their own
// goroutines, which is exactly the shape a race detector run needs to exercise.
type RecordingEmitter struct {
	mu     sync.Mutex
	events []Event
}

// Emit implements Emitter.
func (r *RecordingEmitter) Emit(name string, payload any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, Event{Name: name, Payload: payload})
}

// Events returns a copy of what has been emitted so far.
func (r *RecordingEmitter) Events() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append(make([]Event, 0, len(r.events)), r.events...)
}

// Named returns the payloads emitted under one event name, in order.
func (r *RecordingEmitter) Named(name string) []any {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]any, 0, len(r.events))
	for _, e := range r.events {
		if e.Name == name {
			out = append(out, e.Payload)
		}
	}
	return out
}

// NopEmitter discards everything. For the many tests that exercise a feature which happens to emit.
type NopEmitter struct{}

// Emit implements Emitter.
func (NopEmitter) Emit(string, any) {}
