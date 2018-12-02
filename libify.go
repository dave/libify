package libify

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"io"
	"os"
	"strings"
	"time"

	"github.com/dave/dst"
	"github.com/dave/dst/dstutil"

	"github.com/dave/dst/decorator"
	"github.com/pkg/errors"
	"golang.org/x/tools/go/packages"
)

func Main(ctx context.Context, options Options) error {

	if options.Out == nil {
		options.Out = os.Stdout
	}

	l := &libifier{options: options}

	if err := l.load(ctx); err != nil {
		return errors.WithStack(err)
	}

	if err := l.findPackageLevelVars(); err != nil {
		return errors.WithStack(err)
	}

	return nil
}

// Libifier converts a command line app to a library
type libifier struct {
	options  Options
	paths    []string
	packages map[string]*libifyPkg
}

func newLibifyPkg(path string) *libifyPkg {
	return &libifyPkg{
		path:             path,
		packageLevelVars: map[types.Object]bool{},
	}
}

type libifyPkg struct {
	path             string
	pkg              *decorator.Package
	tst              *decorator.Package
	packageLevelVars map[types.Object]bool
}

func (l *libifier) findPackageLevelVars() error {
	for _, lp := range l.packages {
		for _, file := range lp.pkg.Syntax {
			if strings.HasSuffix(lp.pkg.Decorator.Filenames[file], "_test.go") {
				continue
			}
			dstutil.Apply(file, func(c *dstutil.Cursor) bool {
				switch n := c.Node().(type) {
				case *dst.GenDecl:
					if n.Tok != token.VAR {
						return true
					}
					if _, ok := c.Parent().(*dst.DeclStmt); ok {
						// skip vars inside functions
						return true
					}

					for _, spec := range n.Specs {
						spec := spec.(*dst.ValueSpec)
						// look up the object in the types.Defs
						for _, id := range spec.Names {
							if id.Name == "_" {
								continue
							}
							def, ok := lp.pkg.TypesInfo.Defs[lp.pkg.Decorator.Ast.Nodes[id].(*ast.Ident)]
							if !ok {
								panic(fmt.Sprintf("can't find %s in defs", id.Name))
							}
							lp.packageLevelVars[def] = true
						}
					}
				}
				return true
			}, nil)
		}
	}
	return nil
}

func (l *libifier) load(ctx context.Context) error {
	fmt.Fprintln(l.options.Out, "load")
	defer fmt.Fprintln(l.options.Out, "load done")

	filter := func(p string) bool { return strings.HasPrefix(p, l.options.RootPath) }

	start := time.Now()
	var err error
	l.paths, err = LoadAllPackages(ctx, l.options.Path, l.options.RootDir, filter)
	if err != nil {
		return errors.WithStack(err)
	}
	end := time.Now()
	fmt.Fprintf(l.options.Out, "Loaded %d paths in %v seconds\n", len(l.paths), end.Sub(start).Seconds())

	config := &packages.Config{
		Mode:    packages.LoadSyntax,
		Tests:   true,
		Context: ctx,
		Dir:     l.options.RootDir,
	}

	l.packages = map[string]*libifyPkg{}

	start = time.Now()
	pkgs, err := decorator.Load(config, l.paths...)
	if err != nil {
		return errors.WithStack(err)
	}
	end = time.Now()
	fmt.Fprintf(l.options.Out, "Loaded %d packages in %v seconds\n", len(l.paths), end.Sub(start).Seconds())

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

		pth := strings.TrimSuffix(pkg.PkgPath, "_test")
		if l.packages[pth] == nil {
			l.packages[pth] = newLibifyPkg(pth)
		}
		p := l.packages[pth]

		if isTestPath {
			p.tst = pkg
			continue
		}

		if isTestID {
			p.pkg = pkg
		} else {
			// for non test id (e.g. id == "fmt"), only store if the variation with test files
			// enabled (e.g. id == "fmt [fmt.test]") has not been stored yet.
			if p.pkg == nil {
				p.pkg = pkg
			}
		}
	}

	return nil
}

type Options struct {
	Path     string
	RootPath string
	RootDir  string
	Out      io.Writer
}
