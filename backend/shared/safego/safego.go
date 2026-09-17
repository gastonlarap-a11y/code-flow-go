// Package safego starts every goroutine in the application.
//
// Why a package for two dozen lines: in .NET an unobserved exception on a Task was swallowed by
// the runtime, so a background failure in CodeFlow 2.x cost one feature. In Go an unrecovered
// panic in *any* goroutine terminates the whole process — the window disappears with no dialog and
// the user loses unsaved work in a terminal, a chat and an API request at once. That is a strictly
// worse failure mode than the one the port is replacing, and it is the reinterpretation of
// BOOT-035 ("no emitter may end the app") for Go.
//
// The rule is therefore mechanical: `go` statements do not appear outside this package. A lint
// gate in CI greps for them (docs: MIGRATION-GO.md §10.1), which only works because there is
// exactly one sanctioned spelling.
//
// Recovery is not swallowing. A recovered panic is a defect; it is reported through the handler
// installed at start-up (which writes it to errors.log and shell.log with its stack) and the
// goroutine ends. What it must not do is take the other fifteen features down with it.
package safego

import (
	"fmt"
	"os"
	"runtime/debug"
	"sync/atomic"
)

// Recovered is one goroutine that panicked. Name is the caller-supplied label, which is what makes
// a stack in a log readable months later — "hide-after-fullscreen" says more than goroutine 47.
type Recovered struct {
	Name  string
	Value any
	Stack []byte
}

// String renders the panic the way the logs want it: one line, the stack kept separate so a caller
// can decide whether to include it.
func (r Recovered) String() string {
	return fmt.Sprintf("panic in goroutine %q: %v", r.Name, r.Value)
}

// handler is swapped once, in main, for the one that writes to the real logs. It is a pointer to a
// func rather than an atomic.Value holding a func because a func is not comparable, which
// atomic.Value requires for its first store to be type-checked consistently.
var handler atomic.Pointer[func(Recovered)]

// SetHandler installs the reporter for recovered panics. main calls it once, before anything is
// started; tests call it to capture. Passing nil restores the default (stderr).
func SetHandler(fn func(Recovered)) {
	if fn == nil {
		handler.Store(nil)
		return
	}
	handler.Store(&fn)
}

// report is separate so both Go and Do share it, and so the default never panics itself: a handler
// that panics would defeat the whole purpose, so it runs behind its own recover.
func report(r Recovered) {
	defer func() {
		if p := recover(); p != nil {
			fmt.Fprintf(os.Stderr, "safego: the panic handler itself panicked: %v\n", p)
		}
	}()

	if fn := handler.Load(); fn != nil {
		(*fn)(r)
		return
	}
	fmt.Fprintf(os.Stderr, "%s\n%s\n", r.String(), r.Stack)
}

// Go runs fn in a new goroutine, recovering and reporting a panic instead of letting it end the
// process. name labels the goroutine in whatever the handler writes.
//
// It deliberately takes no context and returns nothing: a goroutine that needs to be waited on or
// cancelled owns that machinery itself (a registry with a lifetime context, an errgroup), and
// hiding it here would encourage fire-and-forget work that nobody can shut down. See §3.7 on the
// difference between a call's context and the application lifetime.
func Go(name string, fn func()) {
	go func() {
		defer func() {
			if p := recover(); p != nil {
				report(Recovered{Name: name, Value: p, Stack: debug.Stack()})
			}
		}()
		fn()
	}()
}

// Do runs fn on the current goroutine with the same protection, and reports whether it completed.
// It exists for callbacks the UI toolkit invokes on its own threads — a tray click, a menu item, a
// window hook — where there is no `go` statement to wrap but a panic is just as fatal.
func Do(name string, fn func()) (ok bool) {
	defer func() {
		if p := recover(); p != nil {
			report(Recovered{Name: name, Value: p, Stack: debug.Stack()})
			ok = false
		}
	}()
	fn()
	return true
}
