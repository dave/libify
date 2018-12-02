package libify

import (
	"context"
	"fmt"
	"go/build"
	"path/filepath"
	"strings"
	"time"

	"github.com/dave/dst/decorator"
	"github.com/pkg/errors"
	"golang.org/x/tools/go/packages"
)

func Main(ctx context.Context, options Options) error {

	l := &libifier{options: options}

	if err := l.load(ctx); err != nil {
		return errors.WithStack(err)
	}
	return nil
}

// Libifier converts a command line app to a library
type libifier struct {
	options  Options
	paths    []string
	packages map[string]*decorator.Package
	tests    map[string]*decorator.Package
}

// Foo is temporary
func (l *libifier) load(ctx context.Context) error {
	fmt.Println("load")
	defer fmt.Println("load done")

	filter := func(p string) bool { return strings.HasPrefix(p, l.options.Root) }
	dir := filepath.Join(build.Default.GOPATH, "src", l.options.Path)

	start := time.Now()
	paths, err := LoadAllPackages(ctx, l.options.Path, dir, filter)
	if err != nil {
		return errors.WithStack(err)
	}
	end := time.Now()
	fmt.Printf("Loaded %d paths in %v seconds\n", len(paths), end.Sub(start).Seconds())

	config := &packages.Config{
		Mode:    packages.LoadSyntax,
		Tests:   true,
		Context: ctx,
		Dir:     dir,
	}

	l.packages = map[string]*decorator.Package{}
	l.tests = map[string]*decorator.Package{}

	start = time.Now()
	pkgs, err := decorator.Load(config, paths...)
	if err != nil {
		return errors.WithStack(err)
	}
	end = time.Now()
	fmt.Printf("Loaded %d packages in %v seconds\n", len(paths), end.Sub(start).Seconds())

	for _, pkg := range pkgs {

		// here we have:
		//
		// | PkgPath | ID              |
		// | X       | X               | just non-test files
		// | X       | X [X.test]      | all files in X package (including tests)
		// | X_test  | X_test [X.test] | just test files in X_test package (this is missing if no X_test tests)
		// | X.test  | X.test          | generated files
		//
		isTestPath := strings.HasSuffix(pkg.PkgPath, "_test")
		isTestID := strings.HasSuffix(pkg.ID, ".test]")
		isTestGen := strings.HasSuffix(pkg.ID, ".test")

		if isTestGen {
			continue
		}

		if isTestPath {
			l.tests[strings.TrimSuffix(pkg.PkgPath, "_test")] = pkg
			continue
		}

		if isTestID {
			l.packages[pkg.PkgPath] = pkg
		} else {
			// for non test id (e.g. id == "fmt"), only store if the variation with test files
			// enabled (e.g. id == "fmt [fmt.test]") has not been stored yet.
			if _, ok := l.packages[pkg.PkgPath]; !ok {
				l.packages[pkg.PkgPath] = pkg
			}
		}
	}

	return nil
}

type Options struct {
	Root, Path string
}
