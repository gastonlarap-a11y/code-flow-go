package update

import (
	"errors"
	"fmt"
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

// handOff opens the downloaded artefact the way the platform's shell would.
//
// It is one call on both platforms and two different outcomes, which is exactly what 2.x did and
// why `InstallKind` exists. On Windows the shell runs the NSIS installer. On macOS it mounts the
// disk image — `open <dmg>` mounts, it does not reveal in Finder, despite the 2.x method being
// called `RevealInFileManager`.
//
// # Nothing quits the app here
//
// The command returns, `updateStore` moves to `ready`, and the user's *Restart* is what quits. So
// on Windows the next version's installer runs **while CodeFlow is still running**, which is not an
// accident to be fixed here: the installer is the half that deals with it, and the NSIS
// close-running-app logic covers 3.x → 3.y for the same reason it covers 2.7.1 → 3.0.0.
func (s *Service) handOff(path string) error {
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
