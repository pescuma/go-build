package main

import (
	"github.com/pescuma/go-build"
)

func main() {
	b, err := build.NewBuilder(nil)
	if err != nil {
		panic(err)
	}

	err = b.RunTarget("all")
	if err != nil {
		panic(err)
	}
}
