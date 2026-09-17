//go:build !darwin && !windows

package security

// Linux and everything else: no backend.
//
// Every operation fails rather than falling back to a file (SEC-004). That is the deliberate
// choice 2.x made and it is the right one: a "fallback" here means writing a GitHub token to disk
// in plaintext, and the user would have no way of knowing it happened. An app that cannot store
// credentials on a platform it does not ship for is a smaller problem than one that stores them
// badly.
//
// CodeFlow ships for macOS and Windows. This file exists so the package builds on a Linux CI
// runner and so `go vet ./...` covers it.
func osBackend() backend { return unsupportedBackend{} }

type unsupportedBackend struct{}

func (unsupportedBackend) get(string, string) (string, error) { return "", ErrUnsupported }
func (unsupportedBackend) set(string, string, string) error   { return ErrUnsupported }
func (unsupportedBackend) delete(string, string) error        { return ErrUnsupported }
