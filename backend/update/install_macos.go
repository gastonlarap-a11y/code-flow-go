package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/proc"
)

/*
Installing a macOS update in place (BOOT-022).

**What this replaces, and why.** The handover used to be one call: `open <dmg>`, which mounts the
image and leaves it to the user to drag the app over the installed one. Three things went wrong with
that, and all three were visible on a machine that had updated a dozen times:

  - the volume is never detached, so every update leaves another "CodeFlow" in Finder's sidebar and
    in Launchpad — which is what "it installed a second copy" looks like from the outside;
  - the downloaded image is never removed, so `~/Downloads` accumulates one per release;
  - and nothing is actually replaced. The new version sits on a mounted volume until somebody drags
    it, so an update that reports success can leave the old build in place.

So the updater now does what `scripts/install-macos.sh` does — the same sequence, proven by the same
release workflow that smoke-tests it on every build: mount with `-nobrowse` so no volume appears,
copy the bundle beside the installed one, swap the two by rename, and detach.

**Replacing a bundle that is running is deliberate and safe here.** The swap is two renames, so the
running process keeps the directory it was launched from until it exits; and this app embeds its
assets in the binary (`go:embed`), so after start-up it reads almost nothing from the bundle. The
new version is what the next launch gets, which is what the *Restart* button is for.
*/

// errNoBundle says the running binary is not inside an app bundle — a `task build` binary, or a
// test. There is nothing to replace, so the caller falls back to handing the image to the shell.
var errNoBundle = errors.New("not running from an app bundle")

// installTimeout bounds the whole sequence. Copying a 60 MB bundle is seconds; a minute is the
// point at which something is wrong rather than slow.
const installTimeout = 2 * time.Minute

// installMacOS replaces the installed bundle with the one inside a verified disk image.
func (s *Service) installMacOS(ctx context.Context, image string) error {
	bundle, err := runningBundle()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, installTimeout)
	defer cancel()

	mount, err := os.MkdirTemp("", "codeflow-update-*")
	if err != nil {
		return fmt.Errorf("preparing a mount point: %w", err)
	}
	defer func() { _ = os.RemoveAll(mount) }() // best effort: an empty directory in the temp dir

	// `-nobrowse` is the whole reason a volume no longer appears in Finder, and `-readonly` keeps
	// the image from being modified by a failed copy.
	if err := run(ctx, "hdiutil", "attach", image, "-mountpoint", mount, "-nobrowse", "-readonly", "-quiet"); err != nil {
		return fmt.Errorf("mounting the disk image: %w", err)
	}
	// Detached on every path out, which is the defect this function exists to fix.
	defer func() { _ = run(context.WithoutCancel(ctx), "hdiutil", "detach", mount, "-quiet") }()

	source := filepath.Join(mount, filepath.Base(bundle))
	if info, err := os.Stat(source); err != nil || !info.IsDir() {
		return fmt.Errorf("the disk image holds no %s", filepath.Base(bundle))
	}

	return swapBundle(ctx, source, bundle)
}

/*
swapBundle puts the new bundle where the old one is, and puts the old one back if it cannot.

Copied beside the target rather than over it, then exchanged by rename: a half-finished copy over a
live install is an application that no longer starts, and a rename is the one step that cannot be
half-done.
*/
func swapBundle(ctx context.Context, source, target string) error {
	staged := target + ".new"
	previous := target + ".old"

	_ = os.RemoveAll(staged)   // a leftover from an interrupted attempt
	_ = os.RemoveAll(previous) //nolint:errcheck // same, and neither is fatal

	if err := run(ctx, "ditto", source, staged); err != nil {
		_ = os.RemoveAll(staged)
		return fmt.Errorf("copying the new version beside the installed one: %w", err)
	}

	// The image came from this app's own download, not a browser, so it carries no quarantine flag
	// — cleared anyway, because a quarantined bundle is refused by Gatekeeper on an unsigned build
	// and that failure would surface as "the update did nothing".
	_ = run(ctx, "xattr", "-dr", "com.apple.quarantine", staged)

	if err := os.Rename(target, previous); err != nil {
		_ = os.RemoveAll(staged)
		return fmt.Errorf("moving the installed version aside: %w", err)
	}

	if err := os.Rename(staged, target); err != nil {
		// Put it back: leaving no application at all is far worse than not updating.
		_ = os.Rename(previous, target)
		_ = os.RemoveAll(staged)
		return fmt.Errorf("moving the new version into place: %w", err)
	}

	_ = os.RemoveAll(previous) // the old version, no longer referenced by anything
	return nil
}

/*
runningBundle answers the `.app` this process is running from.

Derived from the executable rather than assumed to be `/Applications/CodeFlow.app`, because a user
who keeps it in `~/Applications` is updating that one — and because assuming the path is how an
updater installs a second copy somewhere the user does not look.
*/
func runningBundle() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("finding the running binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	return bundleFor(executable)
}

// bundleFor is the path arithmetic of runningBundle, split out so it can be tested without being
// the binary under test.
func bundleFor(executable string) (string, error) {
	// …/CodeFlow.app/Contents/MacOS/CodeFlow
	macOS := filepath.Dir(executable)
	contents := filepath.Dir(macOS)
	bundle := filepath.Dir(contents)

	if filepath.Base(macOS) != "MacOS" ||
		filepath.Base(contents) != "Contents" ||
		!strings.HasSuffix(bundle, ".app") {
		return "", errNoBundle
	}

	return bundle, nil
}

// run executes one step of the install. Through `shared/proc` like every other child process, so it
// gets its own process group and an environment that cannot carry a credential (SEC-007).
func run(ctx context.Context, name string, args ...string) error {
	output, err := proc.Command(ctx, name, args...).CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, detail)
	}
	return nil
}
