package main

import (
	"context"
	"fmt"
	"go/build"
	"os"
	"path/filepath"

	"github.com/dave/libify/libgo"
)

func main() {
	if err := libgo.Main(context.Background(), linkOptions()); err != nil {
		fmt.Printf("%+v", err)
		os.Exit(1)
	}
}

func compileOptions() libgo.Options {
	return libgo.Options{
		From:     "cmd/compile",
		RootPath: "github.com/dave/compile",
		RootDir:  filepath.Join(build.Default.GOPATH, "src", "github.com/dave/compile"),
		DisableTests: map[string]map[string]bool{
			"cmd/compile_test":             {"TestFormats": true},
			"cmd/compile/internal/gc_test": {"TestBuiltin": true},
		},
		Init: false,
	}
}

func linkOptions() libgo.Options {
	return libgo.Options{
		From:     "cmd/link",
		RootPath: "github.com/dave/link",
		RootDir:  filepath.Join(build.Default.GOPATH, "src", "github.com/dave/link"),
		DisableTests: map[string]map[string]bool{
			"cmd/link": {"TestDWARFiOS": true},
		},
		Init: false,
	}
}
