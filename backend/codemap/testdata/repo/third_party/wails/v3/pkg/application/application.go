// Package application stands in for Wails v3's: the map recognises it by its import path's suffix.
package application

// Service is a bound service.
type Service struct{}

// NewService binds instance's exported methods to the frontend.
func NewService[T any](instance *T) Service { return Service{} }
