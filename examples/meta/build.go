package main

import (
	"github.com/pescuma/go-build"
)

// This file builds itself

func main() {
	cfg := build.NewBuilderConfig()
	cfg.MainFileNames = []string{"build.go"}

	b, err := build.NewBuilder(cfg)
	if err != nil {
		panic(err)
	}

	err = b.RunTarget("all")
	if err != nil {
		panic(err)
	}
}
