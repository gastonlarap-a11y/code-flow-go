package usage

import (
	"context"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
)

// Register adds the indicator's one command (USAGE-001).
//
// One, because the panel draws its four sections together: a command per section would be four
// round trips to render one pill and four chances for the sections to disagree about what "now"
// means.
func Register(r *bridge.Registry, service *Service) {
	r.Add("usage_snapshot", func(ctx context.Context, _ bridge.Params) (any, error) {
		return service.Snapshot(ctx)
	})
}
