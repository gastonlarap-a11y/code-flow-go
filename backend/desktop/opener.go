package desktop

import (
	"errors"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Opener hands a path to the operating system: a file to its default application, a directory to
// Explorer or Finder.
//
// Same shape and same reason as Emitter — created before application.New so the features that need
// it can be registered, bound afterwards. It is what keeps backend/files free of Wails.
type Opener struct {
	app atomic.Pointer[application.App]
}

// NewOpener returns an opener that refuses until Bind is called.
func NewOpener() *Opener { return &Opener{} }

// Bind attaches the created application.
func (o *Opener) Bind(a *application.App) { o.app.Store(a) }

// errNoWindow is what a call before Bind gets. Unlike a dropped event, refusing is right here: the
// user clicked something and nothing happening with no message is the worst of the three outcomes.
var errNoWindow = errors.New("the desktop is not ready yet")

// OpenFile opens a file with its default application.
func (o *Opener) OpenFile(path string) error {
	a := o.app.Load()
	if a == nil {
		return errNoWindow
	}
	return a.Browser.OpenFile(path)
}

// RevealInFileManager opens a directory in the file manager.
//
// `false` for selectFile: the paths this is given are directories to open, not files to highlight,
// and asking the file manager to select a directory behaves differently on each platform.
func (o *Opener) RevealInFileManager(path string) error {
	a := o.app.Load()
	if a == nil {
		return errNoWindow
	}
	return a.Env.OpenFileManager(path, false)
}
