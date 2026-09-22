package update

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/proc"
)

/*
Making *Restart now* restart (BOOT-039).

The button quit and stopped there, on both platforms, and the user opened the app again themselves.
That was honest enough while macOS could not install on its own — there was nothing to come back
to — but once the bundle is swapped before the command returns (BOOT-038) the only thing between
the user and the new version is a launch nobody performs.

An application cannot relaunch itself: whatever would do it dies with the process. So something
outside it has to wait. A detached shell polls for this process to disappear and then opens the
bundle — the same trick every self-updating macOS app uses, and about as much code as describing it.

**Scheduled, never assumed.** It is armed by its own command, called just before the quit that
follows *Restart*, so an ordinary quit — the tray, ⌘Q, the Settings button — does not resurrect the
app minutes later.
*/

// ScheduleRelaunch arranges for the app to be opened again once this process exits.
//
// Answers `errNoBundle` where there is nothing to reopen — a `task build` binary, a test — which
// the caller reports as "nothing scheduled" rather than as a failure.
func ScheduleRelaunch(ctx context.Context, goos string) error {
	if goos != "darwin" {
		// Windows' NSIS installer owns the restart there, and nothing else ships.
		return nil
	}

	bundle, err := runningBundle()
	if err != nil {
		return err
	}

	// Poll rather than wait: `wait` only works on a child, and this process is the parent. A fifth
	// of a second is far below noticing and far above spinning.
	//
	// The extra pause after the process is gone is for the operating system to finish releasing the
	// bundle, so `open` does not race the exit and start the copy that is on its way out.
	script := fmt.Sprintf(
		`while kill -0 %s 2>/dev/null; do sleep 0.2; done; sleep 0.5; open %s`,
		strconv.Quote(strconv.Itoa(os.Getpid())), strconv.Quote(bundle),
	)

	// `WithoutCancel`, deliberately: a child bound to the request's context is killed the moment
	// the command returns, which is before it has anything to do. Detaching from the cancellation
	// while keeping the context is the narrow version of that — `context.Background()` would throw
	// away whatever else the request carried. It goes through `shared/proc` like every child, so it
	// leads its own process group and cannot be handed a credential (SEC-007).
	waiter := proc.Command(context.WithoutCancel(ctx), "/bin/sh", "-c", script)
	if err := waiter.Start(); err != nil {
		return fmt.Errorf("scheduling the relaunch: %w", err)
	}

	// Started and never waited on, and deliberately **not** adopted into the app's job: outliving
	// this process is the entire point of it. The zombie it would otherwise become is collected by
	// init the moment this process exits, which is seconds away.
	return nil
}
