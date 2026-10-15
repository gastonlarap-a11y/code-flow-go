//go:build !cgo

package repomap

// A build without cgo has no tree-sitter: every file is on the map with its language and lines,
// and none has declarations or imports. It exists so `GOOS=windows go build ./...` and the
// cross-platform lint still compile; a release is always built with cgo, and the `parser` smoke
// probe fails one that is not (REPOMAP-003).

// GrammarsLinked says whether this build parses any language.
const GrammarsLinked = false

// extractorID names what produced a cache's facts; a cache written by another extractor is not
// read.
const extractorID = "plain/1"

type plainParser struct{}

func newParser() parser { return plainParser{} }

func (plainParser) parse(Lang, []byte, *Facts) {}

func (plainParser) close() {}
