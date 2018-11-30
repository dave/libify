package main

import (
	"context"
	"fmt"
	"os"

	"github.com/dave/libify/libgo"
)

func main() {
	if err := libgo.Main(context.Background(), compileOptions()); err != nil {
		fmt.Printf("%+v", err)
		os.Exit(1)
	}
}

func compileOptions() libgo.Options {
	return libgo.Options{
		PathFrom: "cmd/compile",
		PathTo:   "github.com/dave/compile",
		DisableTests: map[string]map[string]bool{
			"cmd/compile_test":             {"TestFormats": true},
			"cmd/compile/internal/gc_test": {"TestBuiltin": true},
		},
	}
}

func linkOptions() libgo.Options {
	return libgo.Options{
		PathFrom: "cmd/link",
		PathTo:   "github.com/dave/link",
		DisableTests: map[string]map[string]bool{
			"cmd/link": {"TestDWARFiOS": true},
		},
	}
}
