// Package shop is the fixture the code map's Go half is tested against: every relation the map
// draws appears here once.
package shop

import (
	"context"
	"fmt"

	"example.com/shop/third_party/bun"
	"example.com/shop/third_party/gorm"
)

// Item is one thing on sale.
type Item struct {
	bun.BaseModel `bun:"table:items"`
	ID            int64
}

// Customer is a GORM model, whose table GORM names by convention.
type Customer struct{ ID int64 }

// Order names its table explicitly, and no DDL in the fixture creates it.
type Order struct{ ID int64 }

// TableName is GORM's way of naming a model's table.
func (Order) TableName() string { return "orders" }

// Ghost would be "ghosts" by convention, and the schema has no such table.
type Ghost struct{ ID int64 }

// Store saves items.
type Store interface {
	Save(ctx context.Context, item *Item) error
}

type sqlStore struct{ db *bun.DB }

func (s *sqlStore) Save(ctx context.Context, item *Item) error {
	_ = s.db.NewInsert().Model(item)
	return nil
}

// Closer is satisfied through an embedded field.
type Closer interface{ Close() error }

type base struct{}

func (base) Close() error { return nil }

type wrapped struct{ base }

// Service is bound to the frontend by main.
type Service struct {
	store Store
	db    *bun.DB
	orm   *gorm.DB
}

// AddItem saves through the interface.
func (s *Service) AddItem(ctx context.Context) error {
	return s.store.Save(ctx, &Item{})
}

const listSQL = "SELECT id FROM items JOIN carts ON carts.id = items.id"

// ListItems reads with a raw query.
func (s *Service) ListItems(ctx context.Context) ([]Item, error) {
	_ = listSQL
	return nil, nil
}

// Count hands its model to a generic helper.
func (s *Service) Count(ctx context.Context) int {
	return countRows(s.db, (*Item)(nil))
}

func countRows(db *bun.DB, model any) int {
	_ = db.NewSelect().Model(model)
	return 0
}

// Customers goes through GORM.
func (s *Service) Customers() {
	var customers []Customer
	s.orm.Find(&customers)
	s.orm.Create(&Order{})
	s.orm.Find(&[]Ghost{})
}

// Dynamic builds its table name at run time.
func Dynamic(table string) string {
	return fmt.Sprintf("SELECT * FROM %s", table)
}

func helper() error { return nil }

// Registry registers handlers under a string key.
type Registry struct{}

// Add registers h under name.
func (r *Registry) Add(name string, h func() error) {}

// Register wires the commands.
func Register(r *Registry, s *Service) {
	r.Add("shop_list_items", func() error {
		_, err := s.ListItems(context.Background())
		return err
	})
	r.Add("/api/items/{id}", helper)
	r.Add("not a key", func() error { return nil })
	run(helper)
}

func run(fn func() error) { _ = fn() }

func closeAll(c Closer) error { return c.Close() }

var _ = closeAll(wrapped{})
