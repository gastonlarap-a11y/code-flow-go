package archify

import (
	"encoding/json"
	"errors"
	"strings"
)

// Reading the document out of a model's answer (ARCHIFY-007).

// ErrNoDocument is an answer with no JSON object in it.
var ErrNoDocument = errors.New("the AI answered no diagram")

// extractDocument finds the JSON object in a model's answer: the whole answer when it is one, else
// the first balanced `{…}` that parses — a model asked for JSON alone still writes a sentence before
// it now and then, or wraps it in a fence.
func extractDocument(answer string) ([]byte, error) {
	text := strings.TrimSpace(answer)
	if json.Valid([]byte(text)) && strings.HasPrefix(text, "{") {
		return []byte(text), nil
	}
	for start := strings.IndexByte(text, '{'); start >= 0; {
		if end := balancedEnd(text, start); end > start {
			candidate := text[start : end+1]
			var object map[string]json.RawMessage
			if json.Unmarshal([]byte(candidate), &object) == nil {
				return []byte(candidate), nil
			}
		}
		next := strings.IndexByte(text[start+1:], '{')
		if next < 0 {
			break
		}
		start += 1 + next
	}
	return nil, ErrNoDocument
}

// balancedEnd is the index of the brace that closes the one at start, stepping over strings, or -1.
func balancedEnd(text string, start int) int {
	depth, inString, escaped := 0, false, false
	for i := start; i < len(text); i++ {
		c := text[i]
		switch {
		case escaped:
			escaped = false
		case inString && c == '\\':
			escaped = true
		case c == '"':
			inString = !inString
		case inString:
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}
