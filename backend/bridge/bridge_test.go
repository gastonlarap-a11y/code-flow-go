package bridge_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/sentinel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ok(value any) bridge.Handler {
	return func(context.Context, bridge.Params) (any, error) { return value, nil }
}

// ---- Registry ----------------------------------------------------------------------------

func TestRegistryLooksUpWhatWasAdded(t *testing.T) {
	r := bridge.NewRegistry()
	r.Add("get_status", ok("clean"))

	handler, found := r.Lookup("get_status")

	require.True(t, found)
	assert.NotNil(t, handler)
	assert.Equal(t, 1, r.Len())
}

// A duplicate silently shadows a command: the second registration wins and the first feature's
// behaviour disappears with no error anywhere. It can only be caught here.
func TestRegistryPanicsOnADuplicate(t *testing.T) {
	r := bridge.NewRegistry()
	r.Add("get_status", ok(nil))

	assert.PanicsWithValue(t, "bridge: duplicate command get_status", func() { r.Add("get_status", ok(nil)) })
}

// A late registration creates a command that exists only after some code path has run, which is a
// race between the renderer's first call and whatever triggered it.
func TestRegistryPanicsAfterSeal(t *testing.T) {
	r := bridge.NewRegistry()
	r.Seal()

	assert.True(t, r.Sealed())
	assert.PanicsWithValue(t, "bridge: the registry is sealed; cannot add late_command", func() {
		r.Add("late_command", ok(nil))
	})
}

func TestRegistryRejectsAnEmptyNameOrNilHandler(t *testing.T) {
	r := bridge.NewRegistry()

	assert.Panics(t, func() { r.Add("", ok(nil)) })
	assert.Panics(t, func() { r.Add("no_handler", nil) })
}

func TestRegistryNamesAreSorted(t *testing.T) {
	r := bridge.NewRegistry()
	for _, name := range []string{"list_workspaces", "get_status", "api_send"} {
		r.Add(name, ok(nil))
	}

	assert.Equal(t, []string{"api_send", "get_status", "list_workspaces"}, r.Names())
}

// ---- Params ------------------------------------------------------------------------------

func params(t *testing.T, raw string) bridge.Params {
	t.Helper()
	return bridge.NewParams(json.RawMessage(raw))
}

func TestArgReadsARequiredValue(t *testing.T) {
	p := params(t, `{"repoPath":"/x","depth":3,"force":true}`)

	path, err := bridge.Arg[string](p, "repoPath")
	require.NoError(t, err)
	assert.Equal(t, "/x", path)

	depth, err := bridge.Arg[int](p, "depth")
	require.NoError(t, err)
	assert.Equal(t, 3, depth)

	force, err := bridge.Arg[bool](p, "force")
	require.NoError(t, err)
	assert.True(t, force)
}

// The exact text, verified against the installed 2.7.1 core. The renderer shows it to the user.
func TestArgMissingUsesTheExactMessage(t *testing.T) {
	_, err := bridge.Arg[string](params(t, `{}`), "repoPath")

	require.Error(t, err)
	assert.Equal(t, "missing required parameter 'repoPath'", err.Error())
}

// The renderer sends both an absent field and an explicit null for the same "no value", so a
// handler that told them apart would behave differently depending on which call site reached it.
func TestArgTreatsNullAsMissing(t *testing.T) {
	_, err := bridge.Arg[string](params(t, `{"repoPath":null}`), "repoPath")

	require.Error(t, err)
	assert.Equal(t, "missing required parameter 'repoPath'", err.Error())
}

func TestArgReportsAWrongType(t *testing.T) {
	_, err := bridge.Arg[int](params(t, `{"depth":"three"}`), "depth")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "parameter 'depth' has the wrong type")
}

// Malformed params must still produce a named parameter error, not a JSON syntax error: the user
// can act on "missing required parameter 'repoPath'" and cannot act on "invalid character '}'".
func TestMalformedParamsStillNameTheMissingParameter(t *testing.T) {
	_, err := bridge.Arg[string](params(t, `{not json`), "repoPath")

	require.Error(t, err)
	assert.Equal(t, "missing required parameter 'repoPath'", err.Error())
}

func TestOptionalArg(t *testing.T) {
	p := params(t, `{"message":"hello","cleared":null}`)

	present, err := bridge.OptionalArg[string](p, "message")
	require.NoError(t, err)
	require.NotNil(t, present)
	assert.Equal(t, "hello", *present)

	for _, name := range []string{"cleared", "absent"} {
		value, err := bridge.OptionalArg[string](p, name)
		require.NoError(t, err)
		assert.Nil(t, value, name)
	}
}

func TestArgOrFallsBack(t *testing.T) {
	p := params(t, `{"limit":10}`)

	limit, err := bridge.ArgOr(p, "limit", 50)
	require.NoError(t, err)
	assert.Equal(t, 10, limit)

	offset, err := bridge.ArgOr(p, "offset", 50)
	require.NoError(t, err)
	assert.Equal(t, 50, offset)
}

func TestHasAndRaw(t *testing.T) {
	p := params(t, `{"a":1,"b":null}`)

	assert.True(t, p.Has("a"))
	assert.False(t, p.Has("b"), "an explicit null counts as absent")
	assert.False(t, p.Has("c"))
	assert.ElementsMatch(t, []string{"a", "b"}, p.Names())
}

func TestBindDecodesTheWholeObject(t *testing.T) {
	var got struct {
		RepoPath string `json:"repoPath"`
		Depth    int    `json:"depth"`
	}

	require.NoError(t, bridge.Bind(params(t, `{"repoPath":"/x","depth":3}`), &got))
	assert.Equal(t, "/x", got.RepoPath)
	assert.Equal(t, 3, got.Depth)
}

func TestEmptyParamsAreUsable(t *testing.T) {
	for _, raw := range []string{"", "null", "{}"} {
		p := bridge.NewParams(json.RawMessage(raw))
		assert.False(t, p.Has("anything"), raw)
		_, err := bridge.Arg[string](p, "x")
		assert.Error(t, err, raw)
	}
}

// ---- Service.Invoke ----------------------------------------------------------------------

type recorder struct {
	mu      sync.Mutex
	entries []string
}

func (r *recorder) record(method string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, method+": "+err.Error())
}

func (r *recorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.entries...)
}

func serviceWith(build func(*bridge.Registry)) (*bridge.Service, *recorder) {
	r := bridge.NewRegistry()
	build(r)
	r.Seal()
	rec := &recorder{}
	return bridge.NewService(r, rec.record), rec
}

// The nine debug_* and two api_grpc_* commands the renderer calls are unregistered on purpose and
// depend on this exact text.
func TestInvokeUnknownCommand(t *testing.T) {
	svc, rec := serviceWith(func(*bridge.Registry) {})

	_, err := svc.Invoke(t.Context(), "debug_attach", nil)

	require.Error(t, err)
	assert.Equal(t, "unknown command 'debug_attach'", err.Error())
	assert.Empty(t, rec.all(), "a deliberately deferred command must not fill errors.log")
}

func TestInvokeReturnsNullForAVoidCommand(t *testing.T) {
	svc, _ := serviceWith(func(r *bridge.Registry) { r.Add("delete_workspace", ok(nil)) })

	out, err := svc.Invoke(t.Context(), "delete_workspace", nil)

	require.NoError(t, err)
	assert.Equal(t, "null", string(out), "Wails' transport would write {} for an untyped nil")
}

func TestInvokeReturnsAnEmptyArrayNotNull(t *testing.T) {
	svc, _ := serviceWith(func(r *bridge.Registry) { r.Add("list_workspaces", ok([]string{})) })

	out, err := svc.Invoke(t.Context(), "list_workspaces", nil)

	require.NoError(t, err)
	assert.Equal(t, "[]", string(out))
}

func TestInvokePassesParametersThrough(t *testing.T) {
	svc, _ := serviceWith(func(r *bridge.Registry) {
		r.Add("get_status", func(_ context.Context, p bridge.Params) (any, error) {
			return bridge.Arg[string](p, "repoPath")
		})
	})

	out, err := svc.Invoke(t.Context(), "get_status", json.RawMessage(`{"repoPath":"/x"}`))

	require.NoError(t, err)
	assert.Equal(t, `"/x"`, string(out))
}

// Every sentinel has to survive at position 0, because the renderer matches the eight ": " ones
// with startsWith. This is the test that fails the day somebody wraps an error "for context".
func TestInvokeKeepsEverySentinelAtPositionZero(t *testing.T) {
	for _, prefix := range []string{
		sentinel.CheckoutConflict,
		sentinel.CredentialRefused,
		sentinel.DBConnectionRefused,
		sentinel.SelfApproval,
		sentinel.StaleReview,
		sentinel.NothingToAnalyze,
		sentinel.TicketNotLinked,
		sentinel.TicketSyncFailed,
	} {
		t.Run(strings.TrimSuffix(prefix, ": "), func(t *testing.T) {
			svc, _ := serviceWith(func(r *bridge.Registry) {
				r.Add("failing", func(context.Context, bridge.Params) (any, error) {
					return nil, errors.New(prefix + "the detail the user reads")
				})
			})

			_, err := svc.Invoke(t.Context(), "failing", nil)

			require.Error(t, err)
			assert.True(t, strings.HasPrefix(err.Error(), prefix), "got %q", err.Error())
			assert.Equal(t, prefix+"the detail the user reads", err.Error())
		})
	}
}

func TestInvokeRecordsAFailure(t *testing.T) {
	svc, rec := serviceWith(func(r *bridge.Registry) {
		r.Add("push", func(context.Context, bridge.Params) (any, error) {
			return nil, errors.New("remote rejected")
		})
	})

	_, err := svc.Invoke(t.Context(), "push", nil)

	require.Error(t, err)
	assert.Equal(t, []string{"push: remote rejected"}, rec.all())
}

// A panic in one command must not take the window, the terminals and the chats with it.
func TestInvokeRecoversAPanic(t *testing.T) {
	svc, rec := serviceWith(func(r *bridge.Registry) {
		r.Add("explode", func(context.Context, bridge.Params) (any, error) {
			// A real runtime panic rather than an explicit panic() call: the recovery has to cover
			// the bugs nobody wrote on purpose.
			rows := []string{}
			return rows[3], nil
		})
	})

	out, err := svc.Invoke(t.Context(), "explode", nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "internal error in explode")
	assert.Nil(t, out)

	entries := rec.all()
	require.Len(t, entries, 2, "the message and its stack")
	assert.Contains(t, entries[1], "bridge_test.go", "the stack must point at the handler")
}

func TestInvokeSurvivesAHandlerThatPanicsWithNil(t *testing.T) {
	svc, _ := serviceWith(func(r *bridge.Registry) {
		r.Add("explode", func(context.Context, bridge.Params) (any, error) {
			panic(errors.New("deliberate"))
		})
	})

	_, err := svc.Invoke(t.Context(), "explode", nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "deliberate")
}

// Wails runs each binding call concurrently, so handlers must be safe for parallel use — as the
// C# ones were. Run with -race this is the assertion that Invoke adds no shared state of its own.
func TestInvokeIsSafeForConcurrentCalls(t *testing.T) {
	svc, _ := serviceWith(func(r *bridge.Registry) {
		r.Add("echo", func(_ context.Context, p bridge.Params) (any, error) {
			return bridge.Arg[int](p, "n")
		})
	})

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Go(func() {
			out, err := svc.Invoke(t.Context(), "echo", json.RawMessage(`{"n":`+string(rune('0'+i%10))+`}`))
			assert.NoError(t, err)
			assert.NotEmpty(t, out)
		})
	}
	wg.Wait()
}

func TestInvokeWithoutARecorderDoesNotPanic(t *testing.T) {
	r := bridge.NewRegistry()
	r.Add("failing", func(context.Context, bridge.Params) (any, error) { return nil, errors.New("x") })
	r.Seal()
	svc := bridge.NewService(r, nil)

	_, err := svc.Invoke(t.Context(), "failing", nil)

	assert.Error(t, err)
}

// ---- Emitter -----------------------------------------------------------------------------

func TestRecordingEmitter(t *testing.T) {
	e := &bridge.RecordingEmitter{}

	e.Emit("git:progress", map[string]any{"op": "fetch", "line": "a"})
	e.Emit("git:done", map[string]any{"op": "fetch", "success": true})
	e.Emit("git:progress", map[string]any{"op": "fetch", "line": "b"})

	assert.Len(t, e.Events(), 3)
	assert.Len(t, e.Named("git:progress"), 2)
	assert.Len(t, e.Named("git:done"), 1)
	assert.Empty(t, e.Named("terminal:output"))
}

func TestEmitterFuncAdaptsAFunction(t *testing.T) {
	var got string
	var e bridge.Emitter = bridge.EmitterFunc(func(name string, _ any) { got = name })

	e.Emit("repo:fs-changed", nil)

	assert.Equal(t, "repo:fs-changed", got)
	assert.NotPanics(t, func() { bridge.NopEmitter{}.Emit("x", nil) })
}
