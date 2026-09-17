package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge/jsonwire"
)

// Service is the only type bound to the frontend. Its single method is what the renderer's
// host.ts calls through Call.ByName:
//
//	github.com/gastonlarap-a11y/code-flow/backend/bridge.Service.Invoke
//
// That fully-qualified name is <package path>.<type>.<method>, built by Wails' bindings.go. It is
// a string in two places — here and in host.ts — so a Go test asserts the FQN resolves (§9.4).
type Service struct {
	registry *Registry
	record   func(method string, err error)
}

// NewService wires the registry to the error log.
//
// record is a plain func rather than a *diagnostics.ErrorLog so bridge keeps its "knows no
// feature, knows no infrastructure" property; main passes diagnostics.ErrorLog.Record, tests pass
// nil or a capture.
func NewService(registry *Registry, record func(method string, err error)) *Service {
	return &Service{registry: registry, record: record}
}

// Invoke runs one command.
//
// It is deliberately the whole error-handling boundary of the backend:
//
//   - An unknown command answers with the exact 2.x text. The renderer calls eleven names that are
//     not registered (nine debug_* and two api_grpc_*) and relies on this message.
//   - A handler's error crosses unchanged. Sentinel prefixes live at position 0 of the message and
//     the renderer matches them with startsWith, so nothing may be prepended here.
//   - A panic is recovered. In Go an unrecovered panic on any goroutine ends the process, and the
//     Wails runtime would otherwise surface it as "<pkg.Type.Method>: panic: ..." after the window
//     had already gone. One bad command must not close the user's terminals and chats.
//   - Every failure is recorded, exactly where IpcServer.DispatchAsync recorded it in 2.x, so
//     errors.log stays a complete record rather than a sample.
//
// Each call runs on its own goroutine inside Wails and the ctx it receives is cancelled when the
// call returns. Work that must outlive the call — AI runs, streams, terminals, watchers — belongs
// to a registry with an application-lifetime context, never to this one (§3.7).
func (s *Service) Invoke(ctx context.Context, method string, params json.RawMessage) (out json.RawMessage, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("internal error in %s: %v", method, p)
			s.recordFailure(method, err)
			s.recordFailure(method, fmt.Errorf("stack for %s: %s", method, debug.Stack()))
			out = nil
		}
	}()

	handler, ok := s.registry.Lookup(method)
	if !ok {
		// Not recorded: the eleven deferred commands are called on purpose by a renderer that
		// handles the refusal, and logging them would fill errors.log with expected lines.
		return nil, fmt.Errorf("unknown command '%s'", method)
	}

	result, err := handler(ctx, NewParams(params))
	if err != nil {
		s.recordFailure(method, err)
		return nil, err
	}

	encoded, err := jsonwire.Marshal(result)
	if err != nil {
		s.recordFailure(method, err)
		return nil, err
	}
	return encoded, nil
}

func (s *Service) recordFailure(method string, err error) {
	if s.record != nil {
		s.record(method, err)
	}
}
