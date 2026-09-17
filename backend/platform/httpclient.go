package platform

import (
	"net"
	"net/http"
	"time"
)

// NewSharedHTTPClient builds the one *http.Client the providers, work items and the updater share,
// mirroring the single HttpClient the 2.x composition root created (MIGRATION-GO.md §15.2.7).
//
// One client, not one per call: each client owns a connection pool, and a new one per request
// means a fresh TLS handshake to github.com or dev.azure.com every time — which is what a review
// posting forty threads would pay forty times over.
//
// The API client deliberately does *not* use this (API-001). It builds a client per send because
// the user controls its proxy, its TLS verification and its client certificate, and those cannot
// be shared between two requests that disagree about them.
func NewSharedHTTPClient() *http.Client {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2: true,

		// .NET's PooledConnectionLifetime was 15 minutes, set so a long-lived process would
		// eventually notice a DNS change. Go's equivalent lever is idle expiry: a connection that
		// has been unused for 90 seconds is closed and the next request re-resolves. Capping the
		// lifetime of a *busy* connection buys nothing here, since no CodeFlow host moves mid-review.
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	return &http.Client{
		Transport: &TransientRetryTransport{Next: transport},

		// Five minutes, as in 2.x. It is generous because the longest call behind it is a review
		// posting against a slow enterprise Azure DevOps, and because per-call deadlines are set
		// by the callers that know better through their context.
		Timeout: 5 * time.Minute,
	}
}
