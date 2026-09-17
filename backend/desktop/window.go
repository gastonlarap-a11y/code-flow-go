package desktop

import (
	"runtime"
	"time"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/safego"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// Window geometry, unchanged from 2.x so an existing user's muscle memory and screenshots still
// match. There is no window-state persistence, also as before.
const (
	windowWidth     = 1440
	windowHeight    = 900
	windowMinWidth  = 1024
	windowMinHeight = 640
)

// fullscreenPoll is how the hide-after-fullscreen wait is bounded: 40 checks, 50 ms apart.
const (
	fullscreenPollInterval = 50 * time.Millisecond
	fullscreenPollLimit    = 40
)

func (d *Desktop) newMainWindow() *application.WebviewWindow {
	win := d.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:      "main",
		Title:     "CodeFlow",
		Width:     windowWidth,
		Height:    windowHeight,
		MinWidth:  windowMinWidth,
		MinHeight: windowMinHeight,
		URL:       "/",

		// Shown once the runtime says it is ready, which is Electron's ready-to-show: a window
		// presented before the renderer has painted flashes white, and on a dark theme that is
		// jarring enough that 2.x fixed it the same way.
		Hidden: true,

		// Windows draws its own caption buttons in the React header (WindowControls.tsx); macOS
		// keeps the native traffic lights over the webview.
		Frameless: runtime.GOOS != "darwin",

		EnableFileDrop: true,

		// Left at its default (false) deliberately. The Wails runtime's --default-contextmenu: auto
		// shows the native menu only on editable elements or selected text, which is the same
		// "only when applicable" rule 2.x implemented by hand in installContextMenu.
		DefaultContextMenuDisabled: false,

		// BOOT-029: exactly one capability was ever granted, and it was clipboard *write*, which
		// now goes through Go. Everything the webview can ask for is refused here.
		Permissions: map[application.PermissionType]application.Permission{
			application.PermissionMicrophone:    application.PermissionDeny,
			application.PermissionCamera:        application.PermissionDeny,
			application.PermissionGeolocation:   application.PermissionDeny,
			application.PermissionNotifications: application.PermissionDeny,
			application.PermissionClipboardRead: application.PermissionDeny,
		},

		Mac: application.MacWindow{
			// Native traffic lights floating over the webview, which is what 2.x got from
			// titleBarStyle: "hidden". The renderer reserves room for them with MacControlsSpacer.
			TitleBar: application.MacTitleBarHiddenInset,
		},
	})

	win.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) {
		win.Show()
	})
	return win
}

// installCloseToBackground makes the window's close button hide it (BOOT-007).
//
// CodeFlow keeps terminals, AI runs and stream connections alive across a close; quitting on the
// red button would drop all of them, which is why 2.x intercepted it and why the tray exists.
func (d *Desktop) installCloseToBackground() {
	d.win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if d.quitting.Load() {
			return // a RequestQuit is in progress: let the window close
		}
		e.Cancel()
		d.hideToBackground()
	})
}

// hideToBackground hides the window, leaving fullscreen first on macOS (BOOT-009/010).
//
// Hiding a window that is still in a fullscreen Space leaves macOS showing an empty black Space
// the user has to swipe out of by hand. Leaving fullscreen is animated and asynchronous, so the
// hide has to wait for it — polling IsFullscreen is the exact "transition finished" signal, and 40
// checks caps a transition that never completes.
func (d *Desktop) hideToBackground() {
	if runtime.GOOS != "darwin" || !d.win.IsFullscreen() {
		d.win.Hide()
		return
	}

	d.win.UnFullscreen()
	safego.Go("hide-after-fullscreen", func() {
		for range fullscreenPollLimit {
			if !d.win.IsFullscreen() {
				break
			}
			time.Sleep(fullscreenPollInterval)
		}
		// Window calls from a goroutine must be marshalled to the main thread (§12, R16).
		application.InvokeSync(func() { d.win.Hide() })
	})
}

// ShowMain brings the window back from the tray, the Dock or a second launch.
//
// All three steps are needed and in this order: a hidden window is not minimised, a minimised one
// is not hidden, and neither is focused. 2.x learned each of them separately.
func (d *Desktop) ShowMain() {
	if d.win == nil {
		return
	}
	application.InvokeSync(func() {
		d.win.Show()
		d.win.UnMinimise()
		d.win.Focus()
	})
}

// installFileDrop forwards dropped files to the renderer (BOOT-022).
//
// In 2.x the preload resolved a dropped File to a path with webUtils.getPathForFile, because the
// browser's File object does not carry one. Wails reports the paths natively, and only for a drop
// onto an element carrying data-file-drop-target — which is why ImportModal's full-screen backdrop
// gets the attribute rather than an inner zone: the modal accepts a drop anywhere while it is open.
func (d *Desktop) installFileDrop() {
	d.win.OnWindowEvent(events.Common.WindowFilesDropped, func(e *application.WindowEvent) {
		paths := e.Context().DroppedFiles()
		if len(paths) == 0 {
			return
		}
		d.app.Event.Emit("codeflow:files-dropped", map[string]any{"paths": paths})
	})
}
