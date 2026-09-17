package security

import (
	"context"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/git"
)

// Deps is what this feature needs from the composition root.
type Deps struct {
	Store *Store
}

// Register adds the nine credential commands.
//
// Note what is absent: there is no `get_ado_pat`, no `get_github_token`, no `get_ai_api_key`.
// Nothing here returns a secret to the renderer, ever. The UI only needs to know whether one is
// configured, and the backend is the only thing that reads the value — which is what keeps a token
// out of the renderer's memory, out of a devtools console and out of any log the renderer writes.
//
// The parameter names are the renderer's own: `{org}`, `{host}`, `{provider}`, and the secret
// under `{pat}`, `{token}`, `{key}` respectively. They are inconsistent, and they are kept that
// way — the renderer's 2.x wrappers send exactly these.
func Register(r *bridge.Registry, deps Deps) {
	register := func(prefix, scopeParam, secretParam string, keyFor func(string) string) {
		r.Add("set_"+prefix, func(_ context.Context, p bridge.Params) (any, error) {
			scope, err := bridge.Arg[string](p, scopeParam)
			if err != nil {
				return nil, err
			}
			secret, err := bridge.Arg[string](p, secretParam)
			if err != nil {
				return nil, err
			}
			return nil, AsCommandError(deps.Store.Set(keyFor(scope), secret))
		})

		r.Add("has_"+prefix, func(_ context.Context, p bridge.Params) (any, error) {
			scope, err := bridge.Arg[string](p, scopeParam)
			if err != nil {
				return nil, err
			}
			present, err := deps.Store.Has(keyFor(scope))
			if err != nil {
				return nil, AsCommandError(err)
			}
			return present, nil
		})

		r.Add("delete_"+prefix, func(_ context.Context, p bridge.Params) (any, error) {
			scope, err := bridge.Arg[string](p, scopeParam)
			if err != nil {
				return nil, err
			}
			return nil, AsCommandError(deps.Store.Delete(keyFor(scope)))
		})
	}

	register("ado_pat", "org", "pat", ADOPATKey)
	register("github_token", "host", "token", GitHubTokenKey)
	register("ai_api_key", "provider", "key", AIAPIKey)

	// The pre-commit gate. It lives with the credential store because both belong to the security
	// domain, but they have nothing else in common: this one never touches the keychain, it reads
	// what is about to be committed.
	r.Add("scan_staged_secrets", func(ctx context.Context, p bridge.Params) (any, error) {
		repoPath, err := bridge.Arg[string](p, "repoPath")
		if err != nil {
			return nil, err
		}
		// Reading the diff is the only part that can fail; the scan itself always answers, empty
		// or not.
		diff, err := git.StagedDiff(ctx, repoPath)
		if err != nil {
			return nil, err
		}
		return ScanDiff(diff), nil
	})
}
