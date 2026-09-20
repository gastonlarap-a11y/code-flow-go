package apiclient

import (
	"context"
	"sync"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
)

// The three HTTP commands, and the registry one of them cancels through (API-060, API-061).

// Cancels holds the in-flight sends that can still be stopped.
//
// Separate from the stream connections on purpose (API-060): the two have different lifecycles — a
// send lives for one exchange and a socket for as long as somebody watches it — and one map with
// both in it would need a discriminator nothing else would read.
type Cancels struct {
	mutex sync.Mutex
	// Values are pointers so a finishing send can tell its own entry from a replacement's: two
	// `context.CancelFunc` values cannot be compared, and clearing by id alone would let a send
	// that finished late delete the token of the one that replaced it.
	inFlight map[string]*inFlightSend
}

type inFlightSend struct{ cancel context.CancelFunc }

// NewCancels builds an empty registry.
func NewCancels() *Cancels {
	return &Cancels{inFlight: make(map[string]*inFlightSend, 4)}
}

// begin registers a send under its id and answers the context it runs on.
//
// A second send under a live id replaces the first **and cancels it**: the renderer mints one id
// per send, so a collision means the first is a send nobody is watching any more.
func (c *Cancels) begin(ctx context.Context, id string) (context.Context, func()) {
	running, cancel := context.WithCancel(ctx)
	entry := &inFlightSend{cancel: cancel}

	c.mutex.Lock()
	if previous, found := c.inFlight[id]; found {
		previous.cancel()
	}
	c.inFlight[id] = entry
	c.mutex.Unlock()

	return running, func() {
		c.mutex.Lock()
		if c.inFlight[id] == entry {
			delete(c.inFlight, id)
		}
		c.mutex.Unlock()
		// Always released, entry or not: an un-cancelled context leaks its parent's goroutine.
		cancel()
	}
}

// Cancel fires the token for an id.
//
// **A no-op on an unknown id**, and that is documented rather than accidental: a cancel can
// legitimately race a send that already finished, and an error there would put a failure on screen
// for a button that did exactly what it should.
func (c *Cancels) Cancel(id string) {
	c.mutex.Lock()
	entry, found := c.inFlight[id]
	if found {
		delete(c.inFlight, id)
	}
	c.mutex.Unlock()

	if found {
		entry.cancel()
	}
}

// HTTPDeps is what the transport commands need. No database: they answer on an install whose
// storage failed, which is when somebody is most likely to be testing one request by hand.
type HTTPDeps struct {
	Cancels *Cancels
}

// RegisterHTTP adds the send, its cancellable twin, and the cancel.
func RegisterHTTP(r *bridge.Registry, deps HTTPDeps) {
	// Not cancellable, and it does not pretend to be: there is no id to cancel by, so no stop
	// button is offered for it.
	r.Add("api_send_http", func(ctx context.Context, p bridge.Params) (any, error) {
		request, err := bridge.Arg[HTTPSendRequest](p, "request")
		if err != nil {
			return nil, err
		}
		return Send(ctx, request)
	})

	// The same send, registered so `api_cancel_http` can reach it.
	r.Add("api_send_http_tracked", func(ctx context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		request, err := bridge.Arg[HTTPSendRequest](p, "request")
		if err != nil {
			return nil, err
		}

		running, done := deps.Cancels.begin(ctx, id)
		defer done()

		return Send(running, request)
	})

	r.Add("api_cancel_http", func(_ context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		deps.Cancels.Cancel(id)
		return nil, nil
	})
}
