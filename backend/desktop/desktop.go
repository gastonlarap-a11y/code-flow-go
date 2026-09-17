// Package desktop is the only package that imports Wails.
//
// Everything Electron's main process did in 2.x lives here: the window and its lifecycle, closing
// to the tray, the macOS menu, single-instance handling, quitting with a reason, and the host
// capabilities the renderer calls (dialogs, clipboard, external links, logs).
//
// Keeping Wails inside this package is a deliberate containment. v3 is a pre-release that
// publishes a beta almost daily (§12, R1), so its API churn has to have a blast radius. Feature
// packages receive interfaces — bridge.Emitter, and the small ones declared here — which also
// means every feature is testable without a window or a runtime.
package desktop

import (
	_ "embed"
	"os"
	"os/signal"
	"runtime"
	"sync/atomic"
	"syscall"

	"github.com/gastonlarap-a11y/code-flow/backend/app"
	"github.com/gastonlarap-a11y/code-flow/backend/diagnostics"
	"github.com/gastonlarap-a11y/code-flow/backend/platform"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/safego"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// trayPNG is the 32×32 tray icon, the same file 2.x shipped. Embedded rather than read from disk
// because a tray that silently fails to appear (2.x behaviour when assets/tray.png was missing) is
// the kind of bug nobody reports and everybody works around.
//
//go:embed assets/tray.png
var trayPNG []byte

// Options is what main hands the desktop layer.
type Options struct {
	ShellLog *diagnostics.ShellLog
	Paths    platform.Paths
	State    app.State

	// Host is the service main already put in Options.Services. Install attaches it, which is the
	// step that gives it a window to open dialogs against.
	Host *HostService
}

// Desktop owns the window and the application-level behaviour around it.
type Desktop struct {
	app  *application.App
	win  *application.WebviewWindow
	log  *diagnostics.ShellLog
	opts Options

	// quitting distinguishes a quit somebody asked for from a window close. Without it, the close
	// interception below would cancel the window teardown of a real quit and the app would refuse
	// to exit.
	quitting atomic.Bool
}

// New builds the desktop layer. The Wails application does not exist yet at this point — main
// needs the HostService to put in Options.Services before it can create one — so Install does the
// wiring once it does.
func New(opts Options) *Desktop {
	return &Desktop{log: opts.ShellLog, opts: opts}
}

// Install wires the window, tray, menu, signal handler and reopen behaviour to a created
// application. It is called after application.New and before Run.
func (d *Desktop) Install(a *application.App) {
	d.app = a
	if d.opts.Host != nil {
		d.opts.Host.attach(d)
	}
	d.win = d.newMainWindow()
	d.installCloseToBackground()
	d.installTray()
	d.installApplicationMenu()
	d.installReopen()
	d.installSignalHandler()
	d.installFileDrop()
}

// Window is the main window, for the HostService and tests.
func (d *Desktop) Window() *application.WebviewWindow { return d.win }

// RequestQuit is the only way the application exits deliberately (BOOT-008, BOOT-036).
//
// Every deliberate exit names who asked for it, and the name lands in shell.log. That sounds like
// a nicety until an app that hides on close starts disappearing for somebody: the difference
// between "the tray's Quit item", "a SIGTERM from outside the app" and a macOS logout is the whole
// diagnosis, and 2.x had no way to tell them apart until it started logging this line.
//
// app.Quit destroys the application directly and does not run the window's WindowClosing hook, so
// the close-to-tray interception can never block a real quit. The flag is still set first, because
// a window closing for other reasons during teardown must not be intercepted either.
func (d *Desktop) RequestQuit(reason string) {
	d.log.Info("[shell] quitting: " + reason)
	d.quitting.Store(true)
	if d.app != nil {
		d.app.Quit()
	}
}

// LogExternalQuit is Options.ShouldQuit: a quit that did not come through RequestQuit.
//
// On macOS the Dock's Quit item, a logout and a shutdown all reach applicationShouldTerminate,
// which calls this before any cleanup. Returning true always — refusing to quit when the OS is
// shutting down would be a worse bug than the one this logs.
func (d *Desktop) LogExternalQuit() bool {
	if !d.quitting.Load() {
		d.log.Warn("[shell] quitting: a request from outside the app (Dock, logout or the OS)")
		d.quitting.Store(true)
	}
	return true
}

// installSignalHandler replaces Wails' default one.
//
// Wails' handler quits without naming anything, which would put a hole in BOOT-036 exactly where
// it matters most: a process killed by a service manager, a CI runner or a crash-loop supervisor
// is the case where the log is the only witness.
func (d *Desktop) installSignalHandler() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	safego.Go("signal-handler", func() {
		received, ok := <-signals
		if !ok {
			return
		}
		d.RequestQuit("a " + received.String() + " from outside the app")
	})
}

// installReopen restores the window when the Dock icon is clicked (BOOT-011).
//
// It is the counterpart of closing to the tray on macOS: with no window on screen, the Dock icon
// is the only way back, and an app that ignores a click on it reads as broken.
func (d *Desktop) installReopen() {
	if runtime.GOOS != "darwin" {
		return
	}
	d.app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) {
		d.ShowMain()
	})
}
