package platform_test

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gastonlarap-a11y/code-flow/backend/platform"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultPathsUsesTheLiteralWindowsBase(t *testing.T) {
	base := platform.DefaultPaths().Base()

	if runtime.GOOS == "windows" {
		assert.Equal(t, `C:\CodeFlow`, base, "BOOT-003 / DIVERGENCE-BOOT-a: the literal, not %LOCALAPPDATA%")
		return
	}
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, "CodeFlow"), base)
}

func TestPathsLayout(t *testing.T) {
	p := platform.NewPaths(filepath.Join("tmp", "CodeFlow"))
	base := filepath.Join("tmp", "CodeFlow")

	assert.Equal(t, filepath.Join(base, "logs"), p.Logs())
	assert.Equal(t, filepath.Join(base, "repos"), p.Repos())
	assert.Equal(t, filepath.Join(base, "codeflow.db"), p.Database())
	assert.Equal(t, filepath.Join(base, ".reset-pending"), p.ResetMarker())
	assert.Equal(t, filepath.Join(base, "tickets"), p.Tickets())
	assert.Equal(t, filepath.Join(base, "pr-link-reviews"), p.PRLinkReviews())
	assert.Equal(t, filepath.Join(base, "workspaces", "w1"), p.Workspace("w1"))
	assert.Equal(t, filepath.Join(base, "workspaces", "w1", "skills"), p.WorkspaceSkills("w1"))
	assert.Equal(t, filepath.Join(base, "workspaces", "w1", "mcp.json"), p.WorkspaceMCPConfig("w1"))
}

// BOOT-005 names three directories and an order. Anything else created at start-up would show up
// in a fresh install as a folder the user never asked for.
func TestStartupCreatesExactlyThreeDirectories(t *testing.T) {
	base := filepath.Join(t.TempDir(), "CodeFlow")
	p := platform.NewPaths(base)

	require.NoError(t, p.EnsureStartupDirectories())

	assert.Equal(t, []string{base, filepath.Join(base, "logs"), filepath.Join(base, "repos")}, p.StartupDirectories())

	entries, err := os.ReadDir(base)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	assert.ElementsMatch(t, []string{"logs", "repos"}, names)
}

func TestEnsureStartupDirectoriesIsIdempotent(t *testing.T) {
	p := platform.NewPaths(filepath.Join(t.TempDir(), "CodeFlow"))

	require.NoError(t, p.EnsureStartupDirectories())
	assert.NoError(t, p.EnsureStartupDirectories(), "a second launch must not fail on its own directories")
}

func TestEnsureStartupDirectoriesReportsWhyItFailed(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "a-file")
	require.NoError(t, os.WriteFile(blocked, []byte("not a directory"), 0o644))

	err := platform.NewPaths(blocked).EnsureStartupDirectories()

	require.Error(t, err)
	assert.Contains(t, err.Error(), blocked, "the message has to name the path to be actionable")
}

func TestExtractPath(t *testing.T) {
	const marker = "__CODEFLOW_ENV__"

	for name, tc := range map[string]struct {
		output string
		want   string
		ok     bool
	}{
		"a clean dump": {
			marker + "\nHOME=/Users/x\nPATH=/usr/bin:/bin\nSHELL=/bin/zsh\n" + marker + "\n",
			"/usr/bin:/bin", true,
		},
		"a noisy profile before and after the block": {
			"Welcome to zsh!\nnvm: v22 loaded\n" + marker + "\nPATH=/opt/homebrew/bin:/usr/bin\n" + marker + "\nbye\n",
			"/opt/homebrew/bin:/usr/bin", true,
		},
		"an equals sign inside the value": {
			marker + "\nPATH=/opt/weird=dir/bin:/usr/bin\n" + marker,
			"/opt/weird=dir/bin:/usr/bin", true,
		},
		"no marker at all": {"PATH=/usr/bin\n", "", false},
		"a single marker means the shell died midway": {
			marker + "\nPATH=/usr/bin\n", "", false,
		},
		"an empty PATH is not a PATH": {
			marker + "\nPATH=\nHOME=/Users/x\n" + marker, "", false,
		},
		"no PATH line in the block": {
			marker + "\nHOME=/Users/x\n" + marker, "", false,
		},
		"a PATH outside the block is ignored": {
			"PATH=/should/not/win\n" + marker + "\nHOME=/Users/x\n" + marker,
			"", false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := platform.ExtractPath(tc.output)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestMergePath(t *testing.T) {
	for name, tc := range map[string]struct {
		captured, inherited, want string
	}{
		"captured entries come first": {
			"/opt/homebrew/bin", "/usr/bin:/bin", "/opt/homebrew/bin:/usr/bin:/bin",
		},
		"a duplicate keeps its first position": {
			"/usr/bin:/opt/homebrew/bin", "/usr/bin:/bin", "/usr/bin:/opt/homebrew/bin:/bin",
		},
		"empty entries are dropped": {
			"/opt/bin::", ":/usr/bin:", "/opt/bin:/usr/bin",
		},
		"an undefined inherited PATH": {"/opt/bin", "", "/opt/bin"},
		"an undefined captured PATH":  {"", "/usr/bin", "/usr/bin"},
		"both undefined":              {"", "", ""},
		"a repeated entry within one source appears once": {
			"/opt/bin:/opt/bin", "", "/opt/bin",
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, platform.MergePath(tc.captured, tc.inherited))
		})
	}
}

// ApplyLoginShellPath must be safe to call on any platform and must never leave PATH empty.
func TestApplyLoginShellPathLeavesAUsablePath(t *testing.T) {
	before := os.Getenv("PATH")
	t.Cleanup(func() { _ = os.Setenv("PATH", before) })

	assert.NotPanics(t, func() { platform.ApplyLoginShellPath(nil) })
	assert.NotEmpty(t, os.Getenv("PATH"))

	// Whatever the shell said, nothing the process already had may disappear.
	for entry := range strings.SplitSeq(before, ":") {
		if entry != "" {
			assert.Contains(t, os.Getenv("PATH"), entry)
		}
	}
}

func TestIsTransientNetwork(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"nil":               {nil, false},
		"dns failure":       {errors.New("dial tcp: lookup github.com: no such host"), true},
		"bsd resolver":      {errors.New("nodename nor servname provided, or not known"), true},
		"glibc resolver":    {errors.New("Name or service not known"), true},
		"systemd resolver":  {errors.New("Temporary failure in name resolution"), true},
		"no address":        {errors.New("No address associated with hostname"), true},
		"refused":           {errors.New("dial tcp 127.0.0.1:443: connect: connection refused"), true},
		"unreachable":       {errors.New("connect: network is unreachable"), true},
		"network down":      {errors.New("network is down"), true},
		"no route":          {errors.New("connect: no route to host"), true},
		"unrelated failure": {errors.New("unexpected EOF"), false},
		"a 401 is not this": {errors.New("401 Unauthorized"), false},

		// Deliberately absent from the list: the far side may already have acted.
		"a timeout is not transient":           {errors.New("context deadline exceeded"), false},
		"a client timeout is not transient":    {errors.New("Client.Timeout exceeded while awaiting headers"), false},
		"a handshake timeout is not transient": {errors.New("net/http: TLS handshake timeout"), false},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, platform.IsTransientNetwork(tc.err))
		})
	}
}

// A wrapper is free to write its own message and drop the cause's text, which is exactly what
// makes walking the chain rather than testing Error() worth the code.
func TestIsTransientNetworkWalksTheChain(t *testing.T) {
	wrapped := fmt.Errorf("fetch releases: %w", fmt.Errorf("get https://api.github.com: %w",
		errors.New("dial tcp: lookup api.github.com: no such host")))

	assert.True(t, platform.IsTransientNetwork(wrapped))
}

func TestIsTransientNetworkChecksEveryJoinedBranch(t *testing.T) {
	joined := errors.Join(errors.New("unexpected EOF"), errors.New("connect: connection refused"))

	assert.True(t, platform.IsTransientNetwork(joined))
}

func TestIsTransientNetworkIgnoresAJoinOfUnrelatedFailures(t *testing.T) {
	joined := errors.Join(errors.New("unexpected EOF"), errors.New("401 Unauthorized"))

	assert.False(t, platform.IsTransientNetwork(joined))
}

// failingTransport fails n times with err, then delegates.
type failingTransport struct {
	err       error
	remaining int
	attempts  int
	next      http.RoundTripper
}

func (f *failingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.attempts++
	if f.remaining > 0 {
		f.remaining--
		return nil, f.err
	}
	return f.next.RoundTrip(req)
}

func TestRetryTransportReplaysATransientFailureOnce(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(server.Close)

	inner := &failingTransport{err: errors.New("connect: connection refused"), remaining: 1, next: http.DefaultTransport}
	client := &http.Client{Transport: &platform.TransientRetryTransport{Next: inner}}

	resp, err := do(t, client, http.MethodGet, server.URL, nil)

	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	assert.Equal(t, http.StatusTeapot, resp.StatusCode)
	assert.Equal(t, 2, inner.attempts, "exactly one replay")
}

// Once, not until it works: a real outage has to surface.
func TestRetryTransportGivesUpAfterOneReplay(t *testing.T) {
	inner := &failingTransport{err: errors.New("connect: connection refused"), remaining: 99, next: http.DefaultTransport}
	client := &http.Client{Transport: &platform.TransientRetryTransport{Next: inner}}

	// bodyclose cannot see that a request returning an error also returns a nil response; these
	// three tests assert exactly that error, so there is never a body to close.
	_, err := do(t, client, http.MethodGet, "http://example.invalid", nil) //nolint:bodyclose

	require.Error(t, err)
	assert.Equal(t, 2, inner.attempts)
}

func TestRetryTransportDoesNotReplayANonTransientFailure(t *testing.T) {
	inner := &failingTransport{err: errors.New("unexpected EOF"), remaining: 99, next: http.DefaultTransport}
	client := &http.Client{Transport: &platform.TransientRetryTransport{Next: inner}}

	_, err := do(t, client, http.MethodGet, "http://example.invalid", nil) //nolint:bodyclose

	require.Error(t, err)
	assert.Equal(t, 1, inner.attempts)
}

// A body has already been consumed by the first attempt; replaying it would send an empty payload,
// which is worse than the failure it was trying to paper over.
func TestRetryTransportDoesNotReplayARequestWithABody(t *testing.T) {
	inner := &failingTransport{err: errors.New("connect: connection refused"), remaining: 99, next: http.DefaultTransport}
	client := &http.Client{Transport: &platform.TransientRetryTransport{Next: inner}}

	_, err := do(t, client, http.MethodPost, "http://example.invalid", strings.NewReader(`{"a":1}`)) //nolint:bodyclose

	require.Error(t, err)
	assert.Equal(t, 1, inner.attempts)
}

// do issues a request with the test's context, which is also what the production callers do — the
// shared client's five-minute timeout is a backstop, not the per-call deadline.
func do(t *testing.T, client *http.Client, method, url string, body io.Reader) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, body)
	require.NoError(t, err)
	return client.Do(req)
}

func TestSharedClientCarriesTheRetryTransportAndTheFiveMinuteTimeout(t *testing.T) {
	client := platform.NewSharedHTTPClient()

	assert.IsType(t, &platform.TransientRetryTransport{}, client.Transport)
	assert.Equal(t, 5*60, int(client.Timeout.Seconds()))
}
