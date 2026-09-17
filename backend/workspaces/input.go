package workspaces

import (
	"encoding/json"
	"fmt"
)

// unmarshalInput decodes a nested parameter object.
//
// `create_project` is the one command that takes a whole record rather than named scalars — the
// renderer sends `{ input: NewProject }` — so its fields arrive in the renderer's *snake_case*,
// not in the camelCase the other commands use for their arguments. That inconsistency is 2.x's,
// and `NewProject`'s struct tags match it exactly.
func unmarshalInput(raw json.RawMessage, target any) error {
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("parameter 'input' has the wrong shape: %w", err)
	}
	return nil
}
