package libify

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dave/dst"
	"github.com/dave/dst/decorator"
	"github.com/dave/dst/decorator/resolver/guess"
	"github.com/dave/dst/dstutil"
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

	// ===== NO READING AFTER HERE ======
	// ===== NO WRITING BEFORE HERE =====

	if err := l.deleteVars(); err != nil {
		return errors.WithStack(err)
	}

	if err := l.addStateFiles(); err != nil {
		return errors.WithStack(err)
	}

	if err := l.save(); err != nil {
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
		path:                         path,
		pathNoVendor:                 stripVendor(path),
		packageLevelVarObject:        map[types.Object]bool{},
		packageLevelVarGenDecl:       map[*dst.GenDecl]bool{},
		packageLevelVarValueSpec:     map[*dst.ValueSpec]bool{},
		packageStateImportFieldNames: map[string]string{},
	}
}

type libifyPkg struct {
	path                         string
	pathNoVendor                 string
	pkg                          *decorator.Package
	tst                          *decorator.Package
	packageLevelVarGenDecl       map[*dst.GenDecl]bool
	packageLevelVarObject        map[types.Object]bool
	packageLevelVarValueSpec     map[*dst.ValueSpec]bool
	packageStateImportFieldNames map[string]string
}

func (l *libifier) addStateFiles() error {
	fmt.Fprintln(l.options.Out, "addStateFiles")
	defer fmt.Fprintln(l.options.Out, "addStateFiles done")
	for _, lp := range l.packages {
		u := uniqueNamePicker{}
		if len(lp.packageLevelVarGenDecl) == 0 {
			continue
		}
		f := &dst.File{
			Name: dst.NewIdent(lp.pkg.Name),
		}

		var fields []*dst.Field

		importFields, err := l.generatePackageStateImportFields(lp, u)
		if err != nil {
			return errors.WithStack(err)
		}
		sort.Slice(importFields, func(i, j int) bool {
			return importFields[i].Names[0].Name < importFields[j].Names[0].Name
		})
		fields = append(fields, importFields...)

		if len(importFields) > 0 {
			importFields[0].Decs.Before = dst.EmptyLine
			importFields[0].Decs.Start.Prepend("// Package imports")
		}

		varFields, err := l.generatePackageStateVarFields(lp, u)
		if err != nil {
			return errors.WithStack(err)
		}
		sort.Slice(varFields, func(i, j int) bool {
			return varFields[i].Names[0].Name < varFields[j].Names[0].Name
		})
		fields = append(fields, varFields...)

		if len(varFields) > 0 {
			varFields[0].Decs.Before = dst.EmptyLine
			varFields[0].Decs.Start.Prepend("// Package level vars")
		}

		f.Decls = append(f.Decls, &dst.GenDecl{
			Tok: token.TYPE,
			Specs: []dst.Spec{
				&dst.TypeSpec{
					Name: dst.NewIdent("PackageState"),
					Type: &dst.StructType{
						Fields: &dst.FieldList{
							List: fields,
						},
					},
				},
			},
		})

		lp.pkg.Syntax = append(lp.pkg.Syntax, f)
		lp.pkg.Decorator.Filenames[f] = filepath.Join(lp.pkg.Dir, "package-state.go")
	}
	return nil
}

func (l *libifier) generatePackageStateVarFields(lp *libifyPkg, u uniqueNamePicker) ([]*dst.Field, error) {
	var fields []*dst.Field
	for vs := range lp.packageLevelVarValueSpec {
		if vs.Type != nil {
			// if a type is specified, we can add the names as one field
			infoType := lp.pkg.TypesInfo.Types[lp.pkg.Decorator.Ast.Nodes[vs.Type].(ast.Expr)]
			var names []*dst.Ident
			for _, v := range vs.Names {
				names = append(names, dst.Clone(v).(*dst.Ident))
			}
			f := &dst.Field{
				Names: names,
				Type:  l.typeToAstTypeSpec(infoType.Type, lp.path),
			}
			fields = append(fields, f)
			continue
		}
		// if spec.Type is nil, we must separate the name / value pairs
		for i, name := range vs.Names {
			if name.Name == "_" {
				continue
			}
			value := vs.Values[i]
			infoType := lp.pkg.TypesInfo.Types[lp.pkg.Decorator.Ast.Nodes[value].(ast.Expr)]
			f := &dst.Field{
				Names: []*dst.Ident{dst.Clone(name).(*dst.Ident)},
				Type:  l.typeToAstTypeSpec(infoType.Type, lp.path),
			}
			fields = append(fields, f)
		}
	}
	return fields, nil
}

func (l *libifier) generatePackageStateImportFields(lp *libifyPkg, u uniqueNamePicker) ([]*dst.Field, error) {
	var fields []*dst.Field
	var imports []string
	for _, imp := range lp.pkg.Imports {
		implp, ok := l.packages[imp.PkgPath]
		if !ok {
			continue
		}
		if len(implp.packageLevelVarGenDecl) == 0 {
			continue
		}
		imports = append(imports, stripVendor(imp.PkgPath))
	}

	sort.Strings(imports)

	for _, impPath := range imports {

		imp := lp.pkg.Imports[impPath]

		name := u.pick(imp.Name)
		lp.packageStateImportFieldNames[imp.PkgPath] = name

		f := &dst.Field{
			Names: []*dst.Ident{dst.NewIdent(name)},
			Type: &dst.StarExpr{
				X: &dst.Ident{
					Name: "PackageState",
					Path: stripVendor(imp.PkgPath),
				},
			},
		}
		fields = append(fields, f)
	}
	return fields, nil
}

func (l *libifier) deleteVars() error {
	fmt.Fprintln(l.options.Out, "deleteVars")
	defer fmt.Fprintln(l.options.Out, "deleteVars done")
	for _, lp := range l.packages {
		for _, file := range lp.pkg.Syntax {
			dstutil.Apply(file, func(c *dstutil.Cursor) bool {
				switch n := c.Node().(type) {
				case *dst.GenDecl:
					if lp.packageLevelVarGenDecl[n] {
						c.Delete()
					}
				}
				return true
			}, nil)
		}
	}
	return nil
}

func (l *libifier) save() error {
	fmt.Fprintln(l.options.Out, "save")
	defer fmt.Fprintln(l.options.Out, "save done")
	for _, lp := range l.packages {
		if err := lp.pkg.SaveWithResolver(guess.New()); err != nil {
			return errors.WithStack(err)
		}
	}
	return nil
}

func (l *libifier) findPackageLevelVars() error {
	fmt.Fprintln(l.options.Out, "findPackageLevelVars")
	defer fmt.Fprintln(l.options.Out, "findPackageLevelVars done")
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

					lp.packageLevelVarGenDecl[n] = true

					for _, spec := range n.Specs {
						spec := spec.(*dst.ValueSpec)

						lp.packageLevelVarValueSpec[spec] = true

						// look up the object in the types.Defs
						for _, id := range spec.Names {
							if id.Name == "_" {
								continue
							}
							def, ok := lp.pkg.TypesInfo.Defs[lp.pkg.Decorator.Ast.Nodes[id].(*ast.Ident)]
							if !ok {
								panic(fmt.Sprintf("can't find %s in defs", id.Name))
							}
							lp.packageLevelVarObject[def] = true
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

func stripVendor(path string) string {
	findVendor := func(path string) (index int, ok bool) {
		// Two cases, depending on internal at start of string or not.
		// The order matters: we must return the index of the final element,
		// because the final one is where the effective import path starts.
		switch {
		case strings.Contains(path, "/vendor/"):
			return strings.LastIndex(path, "/vendor/") + 1, true
		case strings.HasPrefix(path, "vendor/"):
			return 0, true
		}
		return 0, false
	}
	i, ok := findVendor(path)
	if !ok {
		return path
	}
	return path[i+len("vendor/"):]
}

type uniqueNamePicker map[string]bool

// findAlias finds a unique alias given a path and a preferred alias
func (u uniqueNamePicker) pick(name string) string {

	preferred := name

	// if the current name has a conflict, increment a modifier until a non-conflicting name is
	// found
	modifier := 1
	current := preferred
	for u[current] {
		current = fmt.Sprintf("%s%d", preferred, modifier)
		modifier++
	}

	u[current] = true

	return current
}

func (l *libifier) typeToAstTypeSpec(t types.Type, path string) dst.Expr {
	switch t := t.(type) {
	case *types.Basic:
		switch t.Kind() {
		case types.Bool, types.Int, types.Int8, types.Int16, types.Int32, types.Int64, types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64, types.Uintptr, types.Float32, types.Float64, types.Complex64, types.Complex128, types.String:
			return dst.NewIdent(t.Name())
		case types.UnsafePointer:
			panic("TODO: types.UnsafePointer not implemented")
		case types.UntypedBool:
			return dst.NewIdent("bool")
		case types.UntypedInt:
			return dst.NewIdent("int")
		case types.UntypedRune:
			return dst.NewIdent("rune")
		case types.UntypedFloat:
			return dst.NewIdent("float64")
		case types.UntypedComplex:
			return dst.NewIdent("complex64")
		case types.UntypedString:
			return dst.NewIdent("string")
		case types.UntypedNil:
			panic("TODO: types.UntypedNil not implemented")
		}
	case *types.Array:
		return &dst.ArrayType{
			Len: &dst.BasicLit{
				Kind:  token.INT,
				Value: fmt.Sprint(t.Len()),
			},
			Elt: l.typeToAstTypeSpec(t.Elem(), path),
		}
	case *types.Slice:
		return &dst.ArrayType{
			Elt: l.typeToAstTypeSpec(t.Elem(), path),
		}
	case *types.Struct:
		var fields []*dst.Field
		for i := 0; i < t.NumFields(); i++ {
			f := &dst.Field{
				Names: []*dst.Ident{dst.NewIdent(t.Field(i).Name())},
				Type:  l.typeToAstTypeSpec(t.Field(i).Type(), path),
			}
			fields = append(fields, f)
		}
		return &dst.StructType{
			Fields: &dst.FieldList{
				List: fields,
			},
		}

	case *types.Pointer:
		return &dst.StarExpr{
			X: l.typeToAstTypeSpec(t.Elem(), path),
		}
	case *types.Tuple:
		panic("tuple?")
	case *types.Signature:
		params := &dst.FieldList{}
		for i := 0; i < t.Params().Len(); i++ {
			f := &dst.Field{
				Names: []*dst.Ident{dst.NewIdent(t.Params().At(i).Name())},
				Type:  l.typeToAstTypeSpec(t.Params().At(i).Type(), path),
			}
			params.List = append(params.List, f)
		}
		var results *dst.FieldList
		if t.Results().Len() > 0 {
			results = &dst.FieldList{}
			for i := 0; i < t.Results().Len(); i++ {
				f := &dst.Field{
					Names: []*dst.Ident{dst.NewIdent(t.Results().At(i).Name())},
					Type:  l.typeToAstTypeSpec(t.Results().At(i).Type(), path),
				}
				results.List = append(results.List, f)
			}
		}
		return &dst.FuncType{
			Params:  params,
			Results: results,
		}
	case *types.Interface:
		methods := &dst.FieldList{}
		for i := 0; i < t.NumEmbeddeds(); i++ {
			f := &dst.Field{
				Type: l.typeToAstTypeSpec(t.Embedded(i), path),
			}
			methods.List = append(methods.List, f)
		}
		for i := 0; i < t.NumExplicitMethods(); i++ {
			f := &dst.Field{
				Names: []*dst.Ident{dst.NewIdent(t.ExplicitMethod(i).Name())},
				Type:  l.typeToAstTypeSpec(t.ExplicitMethod(i).Type(), path),
			}
			methods.List = append(methods.List, f)
		}

		return &dst.InterfaceType{
			Methods: methods,
		}
	case *types.Map:
		return &dst.MapType{
			Key:   l.typeToAstTypeSpec(t.Key(), path),
			Value: l.typeToAstTypeSpec(t.Elem(), path),
		}
	case *types.Chan:
		var dir dst.ChanDir
		switch t.Dir() {
		case types.SendOnly:
			dir = dst.SEND
		case types.RecvOnly:
			dir = dst.RECV
		}
		return &dst.ChanType{
			Dir:   dir,
			Value: l.typeToAstTypeSpec(t.Elem(), path),
		}
	case *types.Named:
		if t.Obj().Pkg() == nil || stripVendor(t.Obj().Pkg().Path()) == stripVendor(path) {
			return &dst.Ident{Name: t.Obj().Name()}
		}
		return &dst.Ident{Name: t.Obj().Name(), Path: stripVendor(t.Obj().Pkg().Path())}
	}
	panic(fmt.Sprintf("unsupported type %T", t))
}
