package main

import (
	"github.com/pescuma/go-build"
)

func main() {
	b, err := build.CreateBuilder(nil)
	if err != nil {
		panic(err)
	}

	b.RunTarget("all")
}
