package main

import (
	"context"
	"fmt"
	"os"

	"github.com/dave/libify/libgo"
)

const INIT = false

func main() {
	if err := libgo.Main(context.Background(), linkOptions()); err != nil {
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
		Init: INIT,
	}
}

func linkOptions() libgo.Options {
	return libgo.Options{
		PathFrom: "cmd/link",
		PathTo:   "github.com/dave/link",
		DisableTests: map[string]map[string]bool{
			"cmd/link": {"TestDWARFiOS": true},
		},
		Init: INIT,
	}
}
