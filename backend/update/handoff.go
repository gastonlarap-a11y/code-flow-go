package update

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// Handing the verified artefact to the operating system (§2.9).

// Opener hands a path to the operating system.
//
// Declared here rather than imported so this package stays free of Wails, and with one method
// rather than the file package's two: the updater opens a file and never reveals a directory.
type Opener interface {
	// OpenFile opens a file with its default application.
	OpenFile(path string) error
}

/*
handOff installs the verified artefact, or hands it to the platform's shell when it cannot.

**macOS installs in place** (BOOT-022). It used to be `open <dmg>`, which mounts the image and
leaves the user to drag the app across — and which never detached the volume, never removed the
download and never replaced anything. `installMacOS` does what `scripts/install-macos.sh` does
instead. A build not running from an app bundle — `task build`, a test — has nothing to replace, so
it falls back to opening the image, which is the old behaviour and the right one there.

**Windows is unchanged.** The shell runs the NSIS installer, which is the half that knows how to
deal with a running application.

# Nothing quits the app here

The command returns, `updateStore` moves to `ready`, and the user's *Restart* is what quits. On
macOS the swap has already happened by then and the next launch is the new version; on Windows the
installer runs while CodeFlow is still running, which the NSIS close-running-app logic covers for
3.x → 3.y exactly as it covered 2.7.1 → 3.0.0.
*/
func (s *Service) handOff(ctx context.Context, path string) error {
	if s.goos == "darwin" {
		switch err := s.installMacOS(ctx, path); {
		case err == nil:
			// The image has been copied out of; keeping it only fills Downloads with one per
			// release, which is half of what this change is fixing.
			_ = os.Remove(path)
			return nil
		case errors.Is(err, errNoBundle):
			// Not an installed application. Fall through and open it, as before.
		default:
			return fmt.Errorf("the update was downloaded and verified to %s, but installing it failed: %w", path, err)
		}
	}

	if s.opener == nil {
		// Reachable in a headless test and, in principle, before the window is bound. The artefact
		// is downloaded and verified either way, so this names what did not happen rather than
		// pretending the whole command failed.
		return errors.New("the update was downloaded and verified, but there is no desktop to open it with")
	}
	if err := s.opener.OpenFile(path); err != nil {
		return fmt.Errorf("the update was downloaded and verified to %s, but opening it failed: %w", path, err)
	}
	return nil
}
