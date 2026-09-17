package desktop

import (
	"errors"
	"strings"
	"testing"

	"github.com/gastonlarap-a11y/code-flow/backend/app"
	"github.com/gastonlarap-a11y/code-flow/backend/platform"
	"github.com/stretchr/testify/assert"
)

// An internal test, unusually for this codebase: the three helpers below have no window, no
// application and no reason to be exported, but each of them is a silent-failure shape — a wrong
// filter pattern shows the user an empty file picker, a byte-wise truncation writes a replacement
// character into a log, and an empty string where null belongs becomes a path a caller tries to
// open.
//
// Everything else in this package needs a running Wails application and is covered by the Phase 1
// manual checklist instead.

func TestFilterPatternBuildsWhatTheDialogExpects(t *testing.T) {
	for name, tc := range map[string]struct {
		extensions []string
		want       string
	}{
		"one extension":             {[]string{"png"}, "*.png"},
		"several":                   {[]string{"png", "jpg", "webp"}, "*.png;*.jpg;*.webp"},
		"a leading dot is dropped":  {[]string{".png", "jpg"}, "*.png;*.jpg"},
		"surrounding space":         {[]string{" png "}, "*.png"},
		"an empty entry is skipped": {[]string{"png", "", "  "}, "*.png"},
		"nothing at all":            {nil, ""},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, filterPattern(tc.extensions))
		})
	}
}

// Cancel must reach the renderer as null: all four callers branch on
// `typeof result === "string"`, so an empty string would read as a chosen path of "".
func TestFirstOrNilTurnsCancelIntoNull(t *testing.T) {
	assert.Nil(t, firstOrNil("", nil), "a cancelled dialog")
	assert.Nil(t, firstOrNil("", errors.New("dismissed")), "an error")
	assert.Nil(t, firstOrNil("/some/path", errors.New("failed anyway")), "an error wins over a value")

	selected := firstOrNil("/Users/x/repo", nil)
	if assert.NotNil(t, selected) {
		assert.Equal(t, "/Users/x/repo", *selected)
	}
}

// Go strings are bytes and C# strings were UTF-16, so every "cap at N characters" rule in this
// port has to say which unit it means. Cutting a multi-byte rune in half writes U+FFFD into
// shell.log, which is the one file a support session reads.
func TestTruncateRunesCountsCodePoints(t *testing.T) {
	assert.Equal(t, "short", truncateRunes("short", 120))
	assert.Equal(t, "abc", truncateRunes("abcdef", 3))
	assert.Equal(t, "", truncateRunes("abc", 0))

	// Six code points, but sixteen bytes.
	accented := "áéíóúñ"
	assert.Equal(t, accented, truncateRunes(accented, 6))
	assert.Equal(t, "áéí", truncateRunes(accented, 3))

	// Emoji are four bytes each; a byte-wise cut would split one.
	assert.Equal(t, "👍👍", truncateRunes("👍👍👍", 2))
}

func TestQuitReasonIsCappedAtTheDocumentedLimit(t *testing.T) {
	assert.Equal(t, quitReasonLimit, len([]rune(truncateRunes(strings.Repeat("x", 500), quitReasonLimit))))
	assert.Equal(t, 120, quitReasonLimit, "the cap the 2.x preload applied")
}

// SidecarStatus is a pass-through, but its JSON shape is the contract the renderer's store reads
// (`current.logsDirectory`), so it is worth pinning that the struct travels intact.
func TestSidecarStatusReportsWhatStartupFound(t *testing.T) {
	paths := platform.NewPaths(t.TempDir())
	state := app.State{Status: app.StatusDown, Detail: "storage failed", LogsDirectory: paths.Logs()}

	got := NewHostService(state, paths).SidecarStatus()

	assert.Equal(t, app.StatusDown, got.Status)
	assert.Equal(t, "storage failed", got.Detail)
	assert.Equal(t, paths.Logs(), got.LogsDirectory)
}
