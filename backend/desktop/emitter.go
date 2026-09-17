package desktop

import (
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Emitter is the Wails side of bridge.Emitter.
//
// It exists as its own type, created before the application and bound afterwards, because of an
// ordering problem: features are registered into the command registry before application.New is
// called (the registry has to be inside a Service in Options.Services), but the thing that
// actually emits is the application. Handing features this and binding it later keeps them from
// ever seeing a Wails type.
type Emitter struct {
	app atomic.Pointer[application.App]
}

// NewEmitter returns an emitter that drops events until Bind is called.
func NewEmitter() *Emitter { return &Emitter{} }

// Bind attaches the created application.
func (e *Emitter) Bind(a *application.App) { e.app.Store(a) }

// Emit sends one event to the renderer.
//
// With exactly one data argument, Wails puts the payload in CustomEvent.Data, which is what the
// renderer's listen() reads as ev.data. Passing two would nest it in an array and every listener
// would read undefined.
//
// Events before Bind are dropped rather than queued, which matches 2.x: an event emitted before
// the renderer subscribed was lost there too, and every payload carries its own id so a listener
// that missed one can reconcile from a command.
func (e *Emitter) Emit(name string, payload any) {
	a := e.app.Load()
	if a == nil {
		return
	}
	a.Event.Emit(name, payload)
}
