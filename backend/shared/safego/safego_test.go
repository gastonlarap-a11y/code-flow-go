package safego_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/safego"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capture installs a handler that records what it is given and restores the default afterwards.
// The channel is buffered so a reporting goroutine never blocks on a test that stopped reading.
func capture(t *testing.T) <-chan safego.Recovered {
	t.Helper()
	got := make(chan safego.Recovered, 8)
	safego.SetHandler(func(r safego.Recovered) { got <- r })
	t.Cleanup(func() { safego.SetHandler(nil) })
	return got
}

func waitFor(t *testing.T, ch <-chan safego.Recovered) safego.Recovered {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("the handler was never called")
		return safego.Recovered{}
	}
}

// The whole point of the package: the process must still be here to run the assertion.
func TestGoRecoversAPanicAndReportsIt(t *testing.T) {
	got := capture(t)

	safego.Go("exploding-worker", func() { panic("boom") })

	r := waitFor(t, got)
	assert.Equal(t, "exploding-worker", r.Name)
	assert.Equal(t, "boom", r.Value)
	assert.NotEmpty(t, r.Stack, "a report without a stack cannot be debugged")
	assert.Contains(t, r.String(), `panic in goroutine "exploding-worker"`)
}

func TestGoRunsTheFunctionAndStaysQuietWhenItReturns(t *testing.T) {
	got := capture(t)
	done := make(chan struct{})

	safego.Go("quiet-worker", func() { close(done) })

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the function never ran")
	}

	select {
	case r := <-got:
		t.Fatalf("a clean goroutine must not be reported, got %v", r)
	case <-time.After(50 * time.Millisecond):
	}
}

// The stack has to point at the panicking function, not at safego's own recover frame, or every
// report in errors.log would name the same three lines of this file.
func TestStackNamesThePanickingFunction(t *testing.T) {
	got := capture(t)

	safego.Go("named-frame", func() { panicFromHere() })

	assert.Contains(t, string(waitFor(t, got).Stack), "panicFromHere")
}

func panicFromHere() { panic("from a named function") }

func TestDoReportsAndReturnsFalseOnPanic(t *testing.T) {
	got := capture(t)

	ok := safego.Do("tray-click", func() { panic("ui callback blew up") })

	assert.False(t, ok)
	assert.Equal(t, "tray-click", waitFor(t, got).Name)
}

func TestDoReturnsTrueWhenTheCallbackCompletes(t *testing.T) {
	capture(t)
	ran := false

	ok := safego.Do("menu-click", func() { ran = true })

	assert.True(t, ok)
	assert.True(t, ran)
}

// A handler that panics must not become the thing that kills the process it was installed to
// protect. It is reported to stderr and the goroutine ends quietly.
func TestAPanickingHandlerDoesNotEscape(t *testing.T) {
	safego.SetHandler(func(safego.Recovered) { panic("the handler is broken too") })
	t.Cleanup(func() { safego.SetHandler(nil) })

	done := make(chan struct{})
	safego.Go("double-fault", func() {
		defer close(done)
		panic("first")
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the goroutine never finished")
	}
}

// Every feature registry starts goroutines from several of its own, so the handler is written from
// many at once. Run with -race, this is the assertion that the atomic pointer is enough.
func TestConcurrentGoroutinesAreAllReported(t *testing.T) {
	const n = 50
	got := capture(t)

	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		safego.Go("worker", func() {
			defer wg.Done()
			if i%2 == 0 {
				panic(i)
			}
		})
	}
	wg.Wait()

	// Drain what arrived; the halves that panicked are the even ones.
	seen := 0
	for {
		select {
		case r := <-got:
			seen++
			require.True(t, strings.HasPrefix(r.String(), "panic in goroutine"))
		case <-time.After(200 * time.Millisecond):
			assert.Equal(t, n/2, seen)
			return
		}
	}
}
