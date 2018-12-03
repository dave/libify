package libify

import (
	"context"
	"strings"

	"github.com/pkg/errors"
	"golang.org/x/tools/go/packages"
)

func LoadAllPackages(ctx context.Context, path, dir string, tests bool, filter func(string) bool) ([]string, error) {
	cfg := &packages.Config{
		Mode:    packages.LoadImports,
		Tests:   tests,
		Context: ctx,
		Dir:     dir,
	}

	pkgs, err := packages.Load(cfg, path)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	var out []string
	doneID := map[string]bool{}
	donePath := map[string]bool{}

	var process func(*packages.Package)
	process = func(p *packages.Package) {

		// here we have:
		//
		// | PkgPath | ID              |
		// | X       | X               | just non-test files
		// | X       | X [X.test]      | all files in X package (including tests)
		// | X_test  | X_test [X.test] | just test files in X_test package (this is missing if no X_test tests)
		//

		if !filter(p.PkgPath) {
			return
		}

		if !doneID[p.ID] {
			doneID[p.ID] = true
			for _, imp := range p.Imports {
				process(imp)
			}
		}

		if !donePath[p.PkgPath] && !strings.HasSuffix(p.PkgPath, "_test") && !strings.HasSuffix(p.PkgPath, ".test") {
			donePath[p.PkgPath] = true
			out = append(out, p.PkgPath)
		}

	}

	for _, pkg := range pkgs {

		// here we have:
		//
		// | PkgPath | ID              |
		// | X       | X               | just non-test files
		// | X       | X [X.test]      | all files in X package (including tests)
		// | X_test  | X_test [X.test] | just test files in X_test package (this is missing if no X_test tests)
		// | X.test  | X.test          | generated files
		//

		process(pkg)
	}

	if tests {

		// packages.Load with Test:true only returns test packages in the specified package (not
		// imports), so a second run is needed to pick up the imports of all _test packages.
		pkgs, err = packages.Load(cfg, out...)
		if err != nil {
			return nil, errors.WithStack(err)
		}

		for _, pkg := range pkgs {
			process(pkg)
		}

	}

	return out, nil
}
