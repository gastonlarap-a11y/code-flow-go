package main

import (
	"example.com/shop/shop"
	"example.com/shop/third_party/wails/v3/pkg/application"
)

func main() {
	_ = application.NewService(&shop.Service{})
}
