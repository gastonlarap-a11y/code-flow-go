package platform

import (
	"context"
	"fmt"
	"os"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
)

// Deps is what this feature needs from the composition root. Every feature package declares one,
// which is what keeps main a list of wirings rather than a place where behaviour accumulates.
type Deps struct {
	Paths Paths
}

// Register adds this package's commands to the registry.
//
// Phase 1 has one. It is here rather than in app because the marker it writes is a filesystem
// location, and this package owns every path CodeFlow knows.
func Register(r *bridge.Registry, deps Deps) {
	r.Add("reset_app_data", func(_ context.Context, _ bridge.Params) (any, error) {
		return nil, resetAppData(deps.Paths)
	})
}

// resetAppData asks the next launch to wipe {base} (BOOT-017).
//
// It does not delete anything itself, and that split is deliberate rather than lazy: the database
// is open, and on Windows a live SQLite connection holds files that cannot be removed. Writing an
// empty marker and letting the reset-marker start-up stage do the work is the only ordering where
// the deletion happens with nothing holding the directory.
//
// The renderer completes the flow by calling host.quit(); the same two steps as 2.x.
//
// The keychain is untouched. Resetting the application's data is not revoking the user's tokens,
// and people rely on that — a reset is what you try when the app misbehaves, not when you are
// leaving.
func resetAppData(paths Paths) error {
	if err := os.MkdirAll(paths.Base(), DirPerm); err != nil {
		return fmt.Errorf("create the base directory: %w", err)
	}

	marker, err := os.Create(paths.ResetMarker())
	if err != nil {
		return fmt.Errorf("write the reset marker: %w", err)
	}
	if err := marker.Close(); err != nil {
		return fmt.Errorf("write the reset marker: %w", err)
	}
	return nil
}
