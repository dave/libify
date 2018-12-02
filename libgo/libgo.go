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
	"time"

	"github.com/dave/dst"
	"github.com/dave/dst/decorator"
	"github.com/dave/dst/decorator/resolver/guess"
	"github.com/dave/libify"
	"github.com/pkg/errors"
	"golang.org/x/tools/go/packages"
	"gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/plumbing/object"
)

// Main converts a Go internal command line app to a library
func Main(ctx context.Context, options Options) error {

	l := &libgoer{
		options: options,
	}

	if options.Init {

		if err := l.prepDir(); err != nil {
			return errors.WithStack(err)
		}

		if err := l.load(ctx); err != nil {
			return errors.WithStack(err)
		}

		if err := l.updateImportsAndDisableTests(); err != nil {
			return errors.WithStack(err)
		}

		if err := l.save(); err != nil {
			return errors.WithStack(err)
		}

		if err := l.commit(); err != nil {
			return errors.WithStack(err)
		}

	} else {

		if err := l.reset(); err != nil {
			return errors.WithStack(err)
		}

	}

	// PathFrom is the package path of the command - e.g. cmd/link
	// PathTo is the path root of the new package paths - e.g. github.com/foo/bar
	// So the new path to the command will be github.com/foo/bar/cmd/link
	pathCmd := path.Join(options.PathTo, options.PathFrom)

	if err := libify.Main(ctx, libify.Options{Root: options.PathTo, Path: pathCmd}); err != nil {
		return errors.WithStack(err)
	}

	return nil
}

type libgoer struct {
	options Options
	pkgs    []*decorator.Package
	repo    *git.Repository
}

func (l *libgoer) reset() error {
	fmt.Println("reset")
	defer fmt.Println("reset done")

	dir := filepath.Join(build.Default.GOPATH, "src", l.options.PathTo)
	r, err := git.PlainOpen(dir)
	if err != nil {
		return errors.WithStack(err)
	}
	l.repo = r

	w, err := l.repo.Worktree()
	if err != nil {
		return errors.WithStack(err)
	}

	h, err := r.Head()
	if err != nil {
		return errors.WithStack(err)
	}

	if err := w.Reset(&git.ResetOptions{Commit: h.Hash(), Mode: git.HardReset}); err != nil {
		return errors.WithStack(err)
	}
	return nil
}

func (l *libgoer) commit() error {
	fmt.Println("commit")
	defer fmt.Println("commit done")

	w, err := l.repo.Worktree()
	if err != nil {
		return errors.WithStack(err)
	}

	if _, err := w.Add("."); err != nil {
		return errors.WithStack(err)
	}

	options := &git.CommitOptions{Author: &object.Signature{When: time.Now()}}
	if _, err := w.Commit("init", options); err != nil {
		return errors.WithStack(err)
	}

	return nil
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
	if !filter(p) {
		return p
	}
	return path.Join(l.options.PathTo, p)
}

func filter(p string) bool {
	return strings.HasPrefix(p, "cmd/") || strings.HasPrefix(p, "internal/")
}

func (l *libgoer) updateImportsAndDisableTests() error {
	fmt.Println("updateImportsAndDisableTests")
	defer fmt.Println("updateImportsAndDisableTests done")

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

func (l *libgoer) load(ctx context.Context) error {
	fmt.Println("load")
	defer fmt.Println("load done")

	dir := filepath.Join(build.Default.GOROOT, "src", l.options.PathFrom)
	pth := l.options.PathFrom

	start := time.Now()
	paths, err := libify.LoadAllPackages(ctx, pth, dir, filter)
	if err != nil {
		return errors.WithStack(err)
	}
	end := time.Now()
	fmt.Printf("Loaded %d paths in %v seconds\n", len(paths), end.Sub(start).Seconds())

	cfg := &packages.Config{
		Mode:    packages.LoadSyntax,
		Tests:   true,
		Context: ctx,
		Dir:     filepath.Join(build.Default.GOROOT, "src", l.options.PathFrom),
	}

	start = time.Now()
	pkgs, err := decorator.Load(cfg, paths...)
	if err != nil {
		return errors.WithStack(err)
	}
	end = time.Now()
	fmt.Printf("Loaded %d packages in %v seconds\n", len(paths), end.Sub(start).Seconds())

	m := map[string]*decorator.Package{}

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
			l.pkgs = append(l.pkgs, pkg)
			continue
		}

		if isTestID {
			m[pkg.PkgPath] = pkg
		} else {
			// for non test id (e.g. id == "fmt"), only store if the variation with test files
			// enabled (e.g. id == "fmt [fmt.test]") has not been stored yet.
			if _, ok := m[pkg.PkgPath]; !ok {
				m[pkg.PkgPath] = pkg
			}
		}
	}

	for _, p := range m {
		l.pkgs = append(l.pkgs, p)
	}

	return nil
}

func (l *libgoer) prepDir() error {
	fmt.Println("prepDir")
	defer fmt.Println("prepDir done")

	dir := filepath.Join(build.Default.GOPATH, "src", l.options.PathTo)
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		// don't delete the dir, or the terminal will grumble
		fis, err := ioutil.ReadDir(dir)
		if err != nil {
			return errors.WithStack(err)
		}
		for _, fi := range fis {
			fpath := filepath.Join(dir, fi.Name())
			if err := os.RemoveAll(fpath); err != nil {
				return errors.WithStack(err)
			}
		}
	}
	if err := os.MkdirAll(dir, 0777); err != nil {
		return errors.WithStack(err)
	}
	r, err := git.PlainInit(dir, false)
	if err != nil {
		return errors.WithStack(err)
	}
	l.repo = r
	return nil
}

type Options struct {
	PathFrom     string
	PathTo       string
	DisableTests map[string]map[string]bool
	Init         bool
}
