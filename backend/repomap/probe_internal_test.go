package repomap

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The smoke probe passes exactly when the build has its grammars, and says why when it has none.
func TestTheParserProbeTellsAGrammarlessBuildApart(t *testing.T) {
	err := ProbeParser()
	if GrammarsLinked {
		assert.NoError(t, err)
		return
	}
	require.ErrorIs(t, err, ErrNoGrammars)
}
