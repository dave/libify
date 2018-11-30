package libgo

import (
	"bytes"
	"context"
	"fmt"
	"go/build"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/dave/dst/decorator/resolver/guess"

	"github.com/dave/dst/decorator"

	"github.com/dave/dst"

	"github.com/pkg/errors"
	"golang.org/x/tools/go/packages"
)

// Main converts a Go internal command line app to a library
func Main(ctx context.Context, options Options) error {

	l := &libgoer{
		options: options,
	}

	if err := l.loadAndFilter(ctx); err != nil {
		return errors.WithStack(err)
	}

	if err := l.loadFiltered(ctx); err != nil {
		return errors.WithStack(err)
	}

	if err := l.updateImportsAndDisableTests(); err != nil {
		return errors.WithStack(err)
	}

	if err := l.save(); err != nil {
		return errors.WithStack(err)
	}

	return nil
}

type libgoer struct {
	options Options
	paths   []string
	pkgs    []*decorator.Package
}

func (l *libgoer) save() error {
	fmt.Println("save")
	defer fmt.Println("save done")

	for _, pkg := range l.pkgs {
		if len(pkg.Syntax) == 0 {
			continue
		}
		if strings.HasSuffix(pkg.PkgPath, ".test") {
			continue
		}
		fmt.Println("save", pkg.PkgPath)
		pkgPathNoTest := strings.TrimSuffix(pkg.PkgPath, "_test")
		newPathNoTest := l.convertPath(pkgPathNoTest)
		newPathWithTest := l.convertPath(pkg.PkgPath)
		oldDir := filepath.Join(build.Default.GOROOT, "src", pkgPathNoTest)
		newDir := filepath.Join(build.Default.GOPATH, "src", newPathNoTest)
		if fi, err := os.Stat(filepath.Join(oldDir, "testdata")); err == nil && fi.IsDir() {
			if err := Copy(filepath.Join(oldDir, "testdata"), filepath.Join(newDir, "testdata")); err != nil {
				return errors.WithStack(err)
			}
		}
		if err := os.MkdirAll(newDir, 0777); err != nil {
			return errors.WithStack(err)
		}
		res := decorator.NewRestorerWithImports(newPathWithTest, guess.New())
		for _, file := range pkg.Syntax {
			_, fname := filepath.Split(pkg.Decorator.Filenames[file])
			fpath := filepath.Join(newDir, fname)
			buf := &bytes.Buffer{}
			if err := res.Fprint(buf, file); err != nil {
				return errors.WithStack(err)
			}
			if err := ioutil.WriteFile(fpath, buf.Bytes(), 0666); err != nil {
				return errors.WithStack(err)
			}
		}
	}
	return nil
}

func (l *libgoer) convertPath(p string) string {
	if !l.includePath(p) {
		return p
	}
	return path.Join(l.options.PathTo, p)
}

func (l *libgoer) includePath(p string) bool {
	return strings.HasPrefix(p, "cmd/") || strings.HasPrefix(p, "internal/")
}

func (l *libgoer) updateImportsAndDisableTests() error {
	fmt.Println("updateImports")
	defer fmt.Println("updateImports done")
	for _, pkg := range l.pkgs {
		for _, file := range pkg.Syntax {
			dst.Inspect(file, func(n dst.Node) bool {
				switch n := n.(type) {
				case *dst.Ident:
					if n.Path == "" {
						return true
					}
					newPath := l.convertPath(n.Path)
					if newPath != n.Path {
						n.Path = newPath
					}
				case *dst.FuncDecl:
					tests, ok := l.options.DisableTests[pkg.PkgPath]
					if !ok {
						return true
					}
					if !tests[n.Name.Name] {
						return true
					}
					skip := &dst.ExprStmt{
						X: &dst.CallExpr{
							Fun: &dst.SelectorExpr{
								X:   dst.NewIdent("t"),
								Sel: dst.NewIdent("Skip"),
							},
						},
					}
					skip.Decs.Start.Replace("// test disabled")
					skip.Decs.Before = dst.EmptyLine
					skip.Decs.After = dst.EmptyLine
					n.Body.List = []dst.Stmt{skip}
				}
				return true
			})

		}
	}
	return nil
}

func (l *libgoer) loadFiltered(ctx context.Context) error {
	fmt.Println("loadFiltered")
	defer fmt.Println("loadFiltered done")

	cfg := &packages.Config{
		Mode:    packages.LoadSyntax,
		Tests:   true,
		Context: ctx,
		Dir:     filepath.Join(build.Default.GOROOT, "src", l.options.PathFrom),
	}

	pkgs, err := decorator.Load(cfg, l.paths...)
	if err != nil {
		return errors.WithStack(err)
	}

	for _, pkg := range pkgs {
		l.pkgs = append(l.pkgs, pkg)
	}
	return nil
}

func (l *libgoer) loadAndFilter(ctx context.Context) error {
	fmt.Println("loadAndFilter")
	defer fmt.Println("loadAndFilter done")

	cfg := &packages.Config{
		Mode:    packages.LoadImports,
		Tests:   true,
		Context: ctx,
		Dir:     filepath.Join(build.Default.GOROOT, "src", l.options.PathFrom),
	}

	pkgs, err := packages.Load(cfg, l.options.PathFrom)
	if err != nil {
		return errors.WithStack(err)
	}

	done := map[string]bool{}

	var process func(*packages.Package)
	process = func(p *packages.Package) {
		if !l.includePath(p.PkgPath) {
			return
		}
		if done[p.ID] {
			return
		}
		done[p.ID] = true
		for _, imp := range p.Imports {
			process(imp)
		}
		if !strings.HasSuffix(p.PkgPath, ".test") {
			l.paths = append(l.paths, p.PkgPath)
		}
	}

	for _, pkg := range pkgs {
		process(pkg)
	}

	// packages.Load with Test:true only returns test packages in the specified package (not
	// imports), so a second run is needed to picks up all _test packages.
	pkgs, err = packages.Load(cfg, l.paths...)
	if err != nil {
		return errors.WithStack(err)
	}

	for _, pkg := range pkgs {
		process(pkg)
	}

	return nil
}

type Options struct {
	PathFrom     string
	PathTo       string
	DisableTests map[string]map[string]bool
}
