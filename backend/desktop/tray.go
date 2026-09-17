package desktop

import (
	"github.com/wailsapp/wails/v3/pkg/application"
)

// installTray puts CodeFlow in the system tray (BOOT-012).
//
// The tray is not decoration: closing the window hides it, so without a tray icon there is no way
// back on Windows and no way to quit at all. It is the other half of BOOT-007.
//
// Click semantics, read from Wails' systemtray.go: with OnClick set and no OnRightClick, a left
// click runs our handler and a right click opens the menu — Wails only fills in the handlers that
// are missing. That is exactly BOOT-012's behaviour, where a left click shows the window and does
// not open the menu.
//
// AttachWindow is deliberately not used: it turns the icon into a toggle that repositions the
// window next to the tray, which is a different application.
func (d *Desktop) installTray() {
	menu := application.NewMenu()
	menu.Add("Show CodeFlow").OnClick(func(*application.Context) { d.ShowMain() })
	menu.AddSeparator()
	menu.Add("Quit CodeFlow").OnClick(func(*application.Context) { d.RequestQuit("the tray's Quit item") })

	tray := d.app.SystemTray.New()
	tray.SetIcon(trayPNG)
	tray.SetTooltip("CodeFlow")
	tray.SetMenu(menu)
	tray.OnClick(func() { d.ShowMain() })
}
