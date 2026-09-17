package desktop

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/app"
	"github.com/gastonlarap-a11y/code-flow/backend/platform"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// quitReasonLimit caps the reason the renderer may supply, as the 2.x preload did. It is a defence
// against a caller passing an error's whole text — or a remote host's response — into a line that
// is meant to be readable in a log.
const quitReasonLimit = 120

// HostService is the second bound service: the capabilities that are the *window's*, not a
// command's.
//
// They are kept out of the command registry on purpose. A registry command is a backend operation
// with a name the specification tracks and a test-vector fixture; opening a native save dialog is
// neither, and 2.x drew the same line — these arrived through the preload's window.codeflow
// namespace rather than through the IPC transport.
//
// The renderer reaches it by its fully-qualified name:
//
//	github.com/gastonlarap-a11y/code-flow/backend/desktop.HostService
type HostService struct {
	desktop *Desktop
	state   app.State
	paths   platform.Paths
}

// NewHostService builds the service. The Desktop is attached later by Install, because main needs
// this service before the Wails application (and therefore the window) exists.
func NewHostService(state app.State, paths platform.Paths) *HostService {
	return &HostService{state: state, paths: paths}
}

// attach is called by Desktop.Install.
func (h *HostService) attach(d *Desktop) { h.desktop = d }

// DialogFilter is one entry of the renderer's `{ name, extensions }[]`.
type DialogFilter struct {
	Name       string   `json:"name"`
	Extensions []string `json:"extensions"`
}

// DialogOptions covers both pickers; the renderer's OpenDialogOptions and SaveDialogOptions are
// the same three fields once `multiple` and `directory` have chosen the method.
type DialogOptions struct {
	Title       string         `json:"title"`
	DefaultPath string         `json:"defaultPath"`
	Filters     []DialogFilter `json:"filters"`
}

// SidecarStatus answers what start-up did (BOOT-032, reinterpreted).
//
// The name is 2.x's and stays: there is no sidecar any more, but the renderer's store, its banner
// and its strings are all called this, and renaming them would be a renderer change with no user
// visible effect. What it reports is now "did start-up succeed", which is the same question the
// user was really asking.
func (h *HostService) SidecarStatus() app.State {
	return h.state
}

// OpenFile shows a file picker. It returns nil — JS null — when the user cancels, which is what
// all four callers branch on with `typeof result === "string"`.
func (h *HostService) OpenFile(options DialogOptions) *string {
	dialog := h.desktop.app.Dialog.OpenFile()
	dialog.CanChooseFiles(true)
	dialog.CanChooseDirectories(false)
	applyDialogOptions(dialog, options)

	return firstOrNil(dialog.PromptForSingleSelection())
}

// OpenDirectory shows a directory picker.
func (h *HostService) OpenDirectory(options DialogOptions) *string {
	dialog := h.desktop.app.Dialog.OpenFile()
	dialog.CanChooseFiles(false)
	dialog.CanChooseDirectories(true)
	applyDialogOptions(dialog, options)

	return firstOrNil(dialog.PromptForSingleSelection())
}

// SaveFile shows a save picker.
func (h *HostService) SaveFile(options DialogOptions) *string {
	dialog := h.desktop.app.Dialog.SaveFile()

	// A save panel has no title in Wails' API (and on macOS the title bar of an NSSavePanel is not
	// settable either); SetMessage is the line the user actually reads, so the renderer's title
	// goes there rather than being dropped.
	if options.Title != "" {
		dialog.SetMessage(options.Title)
	}

	// defaultPath is sometimes a bare filename and sometimes a full path, depending on the caller.
	// Splitting it covers both: a bare name has no directory part, so SetDirectory is skipped.
	if options.DefaultPath != "" {
		if dir := filepath.Dir(options.DefaultPath); dir != "." && dir != string(filepath.Separator) {
			dialog.SetDirectory(dir)
		}
		dialog.SetFilename(filepath.Base(options.DefaultPath))
	}

	for _, filter := range options.Filters {
		dialog.AddFilter(filter.Name, filterPattern(filter.Extensions))
	}

	return firstOrNil(dialog.PromptForSingleSelection())
}

// OpenExternal opens a URL in the system browser.
//
// The scheme gate stays in Go, exactly where 2.x put it. It is the boundary between "the renderer
// asked to show a link" and "the renderer asked the OS to run something": file://, and on Windows
// anything the shell knows how to execute, are handed to the operating system with the user's own
// privileges. A renderer bug, or markdown from a reviewed pull request, must not be able to reach
// that.
func (h *HostService) OpenExternal(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("not a URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("refusing to open a %q URL; only http and https are allowed", parsed.Scheme)
	}
	return h.desktop.app.Browser.OpenURL(rawURL)
}

// ClipboardWrite puts text on the clipboard (BOOT-033).
//
// It goes through Go rather than navigator.clipboard because WKWebView refuses a write without a
// user gesture with NotAllowedError — measured — and several of the renderer's copy buttons write
// after an await, by which point the gesture has expired.
func (h *HostService) ClipboardWrite(text string) error {
	if !h.desktop.app.Clipboard.SetText(text) {
		return fmt.Errorf("the system clipboard refused the write")
	}
	return nil
}

// OpenLogs reveals the logs directory in Finder or Explorer.
//
// MkdirAll first: the one moment a user is most likely to click "open logs" is when start-up
// failed, and the directories stage is one of the stages that can fail. Opening a file manager on
// a path that does not exist reads as a second bug on top of the first.
func (h *HostService) OpenLogs() error {
	if err := os.MkdirAll(h.paths.Logs(), platform.DirPerm); err != nil {
		return fmt.Errorf("create the logs directory: %w", err)
	}
	return h.desktop.app.Env.OpenFileManager(h.paths.Logs(), false)
}

// Quit exits the application, naming the renderer's reason (BOOT-036).
//
// Callers: Settings' quit button, the reset-app-data flow, and the updater's hand-off.
func (h *HostService) Quit(reason string) {
	h.desktop.RequestQuit("the renderer: " + truncateRunes(reason, quitReasonLimit))
}

func applyDialogOptions(dialog *application.OpenFileDialogStruct, options DialogOptions) {
	if options.Title != "" {
		dialog.SetTitle(options.Title)
	}
	if options.DefaultPath != "" {
		dialog.SetDirectory(options.DefaultPath)
	}
	for _, filter := range options.Filters {
		dialog.AddFilter(filter.Name, filterPattern(filter.Extensions))
	}
}

// filterPattern turns the renderer's ["png", "jpg"] into the "*.png;*.jpg" Wails expects. A
// leading dot is tolerated because the renderer is inconsistent about it.
func filterPattern(extensions []string) string {
	patterns := make([]string, 0, len(extensions))
	for _, ext := range extensions {
		ext = strings.TrimPrefix(strings.TrimSpace(ext), ".")
		if ext == "" {
			continue
		}
		patterns = append(patterns, "*."+ext)
	}
	return strings.Join(patterns, ";")
}

// firstOrNil turns a dialog result into the renderer's `string | null`. A cancelled dialog returns
// an empty string and no error, which must not become an empty path the caller then tries to open.
func firstOrNil(selection string, err error) *string {
	if err != nil || selection == "" {
		return nil
	}
	return &selection
}

// truncateRunes caps by Unicode code point, not by byte. Go strings are bytes and C# strings were
// UTF-16, so every "cap at N characters" rule in this port has to say which unit it means — cutting
// a multi-byte rune in half produces a replacement character in a log line.
func truncateRunes(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit])
}
