package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/gastonlarap-a11y/code-flow/backend/app"
	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/bridge/jsonwire"
	"github.com/gastonlarap-a11y/code-flow/backend/platform"
	"github.com/gastonlarap-a11y/code-flow/backend/storage"
)

// The 3.0 side of the oracle.
//
// In process rather than as a spawned binary, and for the reason the whole port exists: there is no
// transport left to drive. `bridge.Service.Invoke` is what the renderer reaches, and building the
// same registry `main.go` builds is what makes this the real answer rather than a second copy of
// the wiring — the same argument `backend/app/contract_test.go` makes about inspecting the real
// registry.

type newCore struct {
	registry *bridge.Registry
	db       *storage.DB
}

func startNewCore(ctx context.Context, base string) (*newCore, error) {
	paths := platform.NewPaths(base)
	if err := paths.EnsureStartupDirectories(); err != nil {
		return nil, fmt.Errorf("create the base directory: %w", err)
	}

	db, err := storage.Open(ctx, paths.Database())
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", paths.Database(), err)
	}

	// The failure observer is the usage indicator's, and the oracle replays requests rather than
	// watching quotas, so it is discarded here.
	registry, _ := app.BuildRegistry(app.Deps{Paths: paths, DB: db, Version: "3.0.0"})

	return &newCore{registry: registry, db: db}, nil
}

// Call runs one command and marshals its answer exactly as the bridge would.
//
// Through `jsonwire.Marshal` rather than `json.Marshal`, because that is the encoder the real
// service uses: it is what turns a nil `any` into `null` instead of `{}`, and a difference there is
// a difference the renderer would see.
func (c *newCore) Call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	handler, found := c.registry.Lookup(method)
	if !found {
		// The 2.x wording, byte for byte, so an unregistered command compares as one.
		return nil, fmt.Errorf("unknown command '%s'", method)
	}

	raw, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("encode the parameters for %s: %w", method, err)
	}

	answer, err := handler(ctx, bridge.NewParams(raw))
	if err != nil {
		return nil, err
	}
	return jsonwire.Marshal(answer)
}

func (c *newCore) stop() {
	if c.db != nil {
		_ = c.db.Close()
	}
}

// Base is where this side keeps its data, for the path normalisation.
func (c *newCore) Base(base string) string { return filepath.Clean(base) }
