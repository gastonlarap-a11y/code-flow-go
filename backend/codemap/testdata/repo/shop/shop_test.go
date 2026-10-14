package shop

import (
	"context"
	"testing"
)

func TestAddItem(t *testing.T) {
	if err := newService().AddItem(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// newService is a test helper: the tests behind it are one step further up.
func newService() *Service { return &Service{store: &sqlStore{}} }
