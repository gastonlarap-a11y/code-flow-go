//go:build !darwin

package desktop

// installApplicationMenu does nothing outside macOS (BOOT-013).
//
// 2.x called Menu.setApplicationMenu(null) on Windows and Linux. The window there is frameless and
// the renderer draws its own header, so a native menu bar would appear above it as a second,
// unstyled strip. The keyboard shortcuts an Edit menu carries on macOS are handled by the webview
// itself on Windows, so nothing is lost by having none.
func (d *Desktop) installApplicationMenu() {}
