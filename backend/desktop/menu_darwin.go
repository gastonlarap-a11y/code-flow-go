//go:build darwin

package desktop

import "github.com/wailsapp/wails/v3/pkg/application"

// installApplicationMenu builds the macOS menu bar (BOOT-013/014/015).
//
// Two of the three submenus are load-bearing rather than cosmetic:
//
//   - Edit exists so ⌘C, ⌘V, ⌘X and ⌘A reach the webview at all. Without an Edit menu carrying the
//     roles, macOS never routes those key equivalents and copy/paste silently stops working
//     everywhere in the app — including in Monaco and in every text field.
//   - Quit is a custom item, never application.Quit. The Quit role goes through the window's
//     close path, which this app intercepts to hide to the tray, so ⌘Q would only hide the window
//     and the app could never be quit from the keyboard. This is the single subtlest rule in the
//     desktop layer and 2.x carries the same comment.
func (d *Desktop) installApplicationMenu() {
	menu := application.NewMenu()

	appMenu := menu.AddSubmenu("CodeFlow")
	appMenu.AddRole(application.About)
	appMenu.AddSeparator()
	appMenu.AddRole(application.Hide)
	appMenu.AddRole(application.HideOthers)
	appMenu.AddRole(application.UnHide)
	appMenu.AddSeparator()
	appMenu.Add("Quit CodeFlow").
		SetAccelerator("CmdOrCtrl+Q").
		OnClick(func(*application.Context) { d.RequestQuit("the macOS menu's Quit item") })

	edit := menu.AddSubmenu("Edit")
	edit.AddRole(application.Undo)
	edit.AddRole(application.Redo)
	edit.AddSeparator()
	edit.AddRole(application.Cut)
	edit.AddRole(application.Copy)
	edit.AddRole(application.Paste)
	edit.AddRole(application.SelectAll)

	window := menu.AddSubmenu("Window")
	window.AddRole(application.Minimise)
	window.AddRole(application.Zoom)
	window.AddRole(application.CloseWindow)

	d.app.Menu.SetApplicationMenu(menu)
}
