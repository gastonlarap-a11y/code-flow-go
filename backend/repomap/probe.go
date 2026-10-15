package repomap

import (
	"errors"
	"fmt"
)

// ErrNoGrammars is a build without tree-sitter: it was compiled without cgo, so the map would show
// every file and no declaration.
var ErrNoGrammars = errors.New("this build has no tree-sitter grammars: it was compiled without cgo")

// ProbeParser parses a known snippet and checks its declaration comes back — what the smoke test
// asks of a packaged build before it is published (REPOMAP-003).
func ProbeParser() error {
	if !GrammarsLinked {
		return ErrNoGrammars
	}
	p := newParser()
	defer p.close()
	facts := extract(p, LangTypeScript, []byte("export class Probe { size = 1 }\n"))
	if !facts.Parsed || len(facts.Decls) != 1 || facts.Decls[0].Name != "Probe" || len(facts.Decls[0].Fields) != 1 {
		return fmt.Errorf("the TypeScript grammar read %d declarations from a one-class snippet", len(facts.Decls)) //nolint:err113 // a probe's own finding
	}
	return nil
}
