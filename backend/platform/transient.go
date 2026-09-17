package platform

import (
	"errors"
	"net/http"
	"strings"
)

// transientSignals are the failures that mean "the connection was never made", transcribed from
// the 2.x Platform/TransientNetwork.cs list (MIGRATION-GO.md §15.2.1).
//
// Timeouts are deliberately absent, and that absence is the whole design. A DNS failure or a
// refused connection proves the far side never saw the request, so replaying it is free. A timeout
// proves nothing — the server may have created the pull request and simply answered slowly — and
// retrying it is how a review gets posted twice.
//
// Matching is on text because that is what the underlying libraries give us: the same condition
// surfaces as a *net.OpError here, a *url.Error there and a driver's own string elsewhere. Blunt,
// and correct for the one decision it drives.
var transientSignals = []string{
	"no such host",
	"nodename nor servname provided",
	"name or service not known",
	"temporary failure in name resolution",
	"no address associated with hostname",
	"connection refused",
	"network is unreachable",
	"network is down",
	"no route to host",
}

// IsTransientNetwork reports whether err is a connection that was never established.
//
// It walks the whole chain rather than testing err.Error() alone: a wrapper is free to write its
// own message and drop the cause's text, and errors.Join holds several independent branches.
func IsTransientNetwork(err error) bool {
	for err != nil {
		if matchesTransientSignal(err.Error()) {
			return true
		}

		// errors.Join and anything shaped like it holds several independent causes; any one of
		// them being transient is enough. errors.Unwrap does not follow this shape, so it is
		// checked first and explicitly.
		if joined, ok := err.(interface{ Unwrap() []error }); ok { //nolint:errorlint // walking the chain, not matching a type
			for _, branch := range joined.Unwrap() {
				if IsTransientNetwork(branch) {
					return true
				}
			}
			return false
		}

		err = errors.Unwrap(err)
	}
	return false
}

func matchesTransientSignal(message string) bool {
	lower := strings.ToLower(message)
	for _, signal := range transientSignals {
		if strings.Contains(lower, signal) {
			return true
		}
	}
	return false
}

// TransientRetryTransport replays a request once, immediately, when the connection was never made.
//
// Three constraints, each of which is the reason the middleware is this small:
//
//   - Only once, and with no backoff. It exists for the laptop that just woke up and whose DNS is
//     not answering yet, not as a resilience policy. A real outage should surface as one.
//   - Only requests with no body. A body is an io.Reader that has already been consumed by the
//     first attempt; replaying it would send an empty payload. net/http can rewind a body it
//     created itself (GetBody), but the requests this matters for — provider GETs, update checks —
//     have no body at all, so the simple test is also the honest one.
//   - Never for the API client. That one builds a fresh client per send (API-001) and its requests
//     are the user's own; silently repeating one would be a lie about what was sent.
type TransientRetryTransport struct{ Next http.RoundTripper }

// RoundTrip implements http.RoundTripper.
func (t *TransientRetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	next := t.Next
	if next == nil {
		next = http.DefaultTransport
	}

	resp, err := next.RoundTrip(req)
	if err == nil || req.Body != nil || !IsTransientNetwork(err) {
		return resp, err
	}
	return next.RoundTrip(req)
}
