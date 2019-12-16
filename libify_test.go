package libify

import (
	"context"
	"fmt"
	"go/format"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestFoo(t *testing.T) {
	type tc struct {
		solo, skip  bool
		name, desc  string
		path        string
		src, expect map[string]string
	}
	tests := []struct {
		name          string
		root          string
		setup, expect map[string]string
		cases         []tc
	}{
		{
			name: "simple",
			root: "root",
			setup: map[string]string{
				"go.mod": "module root",
			},
			expect: map[string]string{
				"go.mod": "module root",
			},
			cases: []tc{
				{
					name: "simple",
					desc: "simple example",
					path: "root/a",
					src: map[string]string{
						"a/a.go": `package a

							func A(){}
						`,
					},
					expect: map[string]string{
						"a/a.go": `package a

							func A(pstate *PackageState) {}
						`,
						"a/package-state.go": `package a

							type PackageState struct {
							}

							func NewPackageState() *PackageState {
								pstate := &PackageState{}
								return pstate
							}
						`,
					},
				},
				{
					name: "var",
					desc: "simple package level var",
					path: "root/a",
					src: map[string]string{
						"a/a.go": `package a

							func A(){}

							var B int
						`,
					},
					expect: map[string]string{
						"a/a.go": `package a

							func A(pstate *PackageState) {}
						`,
						"a/package-state.go": `package a

							type PackageState struct {
								// Package level vars
								B int
							}

							func NewPackageState() *PackageState {
								pstate := &PackageState{}
								return pstate
							}
						`,
					},
				},
				{
					name: "import",
					desc: "simple import",
					path: "root/a",
					src: map[string]string{
						"a/a.go": `package a

							import "root/b"

							func A(){
								b.B()
							}
						`,
						"b/b.go": `package b

							func B(){}
						`,
					},
					expect: map[string]string{
						"a/a.go": `package a

							import "root/b"

							func A(pstate *PackageState) {
								b.B(pstate.b)
							}
						`,
						"a/package-state.go": `package a

							import "root/b"

							type PackageState struct {
								// Package imports
								b *b.PackageState
							}

							func NewPackageState(bPackageState *b.PackageState) *PackageState {
								pstate := &PackageState{}
								pstate.b = bPackageState
								return pstate
							}
						`,
						"b/b.go": `package b

							func B(pstate *PackageState) {}
						`,
						"b/package-state.go": `package b

							type PackageState struct {
							}

							func NewPackageState() *PackageState {
								pstate := &PackageState{}
								return pstate
							}
						`,
					},
				},
				{
					name: "struct",
					desc: "simple package level struct",
					path: "root/a",
					src: map[string]string{
						"a/a.go": `package a

							type T struct {
								i int
							}
						`,
					},
					expect: map[string]string{
						"a/a.go": `package a

							type T struct {
								pstate *PackageState
								i int
							}
						`,
						"a/package-state.go": `package a

							type PackageState struct{
							}

							func NewPackageState() *PackageState {
								pstate := &PackageState{}
								return pstate
							}
						`,
					},
				},
			},
		},
	}
	var solo, skip bool
	for _, tst := range tests {
		for _, c := range tst.cases {
			if c.solo {
				solo = true
			}
			if c.skip {
				skip = true
			}
		}
	}
	for _, test := range tests {
		for _, c := range test.cases {
			if c.skip || (solo && !c.solo) {
				continue
			}
			t.Run(test.name+"/"+c.name, func(t *testing.T) {
				dir, err := TempDir(test.setup)
				defer os.RemoveAll(dir)
				if err != nil {
					t.Fatal(err)
				}
				if err := AddToDir(dir, c.src); err != nil {
					t.Fatal(err)
				}
				options := Options{
					Path:     c.path,
					RootPath: test.root,
					RootDir:  dir,
					Out:      ioutil.Discard,
				}
				if err := Main(context.Background(), options); err != nil {
					t.Fatal(err)
				}
				expect := map[string]string{}
				for k, v := range test.expect {
					expect[k] = v
				}
				for k, v := range c.expect {
					expect[k] = v
				}
				compareDir(t, dir, expect)
			})
		}
	}
	if solo || skip {
		t.Error("tests skipped")
	}
}

func compareDir(t *testing.T, dir string, expect map[string]string) {
	t.Helper()
	found := map[string]string{}
	walk := func(fpath string, info os.FileInfo, err error) error {
		if info.IsDir() {
			return nil
		}
		relfpath, err := filepath.Rel(dir, fpath)
		if err != nil {
			t.Fatal(err)
		}
		b, err := ioutil.ReadFile(fpath)
		if err != nil {
			t.Fatal(err)
		}
		found[relfpath] = string(b)
		return nil
	}
	if err := filepath.Walk(dir, walk); err != nil {
		t.Fatal(err)
	}
	var keysFound []string
	var keysExpect []string
	for k := range found {
		keysFound = append(keysFound, k)
	}
	for k := range expect {
		keysExpect = append(keysExpect, k)
	}
	sort.Strings(keysFound)
	sort.Strings(keysExpect)
	keysFoundJoined := strings.Join(keysFound, " ")
	keysExpectJoined := strings.Join(keysExpect, " ")
	t.Run("files", func(t *testing.T) {
		compare(t, keysExpectJoined, keysFoundJoined)
	})
	done := map[string]bool{}
	for k, v := range found {
		if done[k] {
			continue
		}
		t.Run(k, func(t *testing.T) {
			if strings.HasSuffix(k, ".go") {
				compareSrc(t, expect[k], v)
			} else {
				compare(t, strings.TrimSpace(expect[k]), strings.TrimSpace(v))
			}
		})
	}
}

func TestMain_load(t *testing.T) {
	dir, err := TempDir(map[string]string{
		"main/main.go": "package main \n\n import \"root/a\" \n\n func main(){a.A()}",
		"a/a.go":       "package a \n\n func A(){}",
		"go.mod":       "module root",
	})
	if err != nil {
		t.Fatal(err)
	}
	l := libifier{
		options: Options{
			Path:     "root/main",
			RootPath: "root",
			RootDir:  dir,
			Out:      ioutil.Discard,
		},
	}
	if err := l.load(context.Background()); err != nil {
		t.Fatal(err)
	}
	expect := "[root/a root/main]"
	found := fmt.Sprint(l.paths)
	compare(t, expect, found)
}

func TestMain_findPackageLevelVars(t *testing.T) {
	dir, err := TempDir(map[string]string{
		"main/main.go": "package main \n\n import \"root/a\" \n\n var N, M int \n\n func main(){a.A()}",
		"a/a.go":       "package a \n\n var A string \n\n func A(){}",
		"go.mod":       "module root",
	})
	if err != nil {
		t.Fatal(err)
	}
	l := libifier{
		options: Options{
			Path:     "root/main",
			RootPath: "root",
			RootDir:  dir,
			Out:      ioutil.Discard,
		},
	}
	if err := l.load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := l.findPackageLevelVars(); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, path := range l.paths {
		var vars []string
		for ob := range l.packages[path].packageLevelVarObject {
			vars = append(vars, ob.Name())
		}
		sort.Strings(vars)
		out = append(out, fmt.Sprintf("%s: %v", path, vars))
	}
	expect := "root/a: [A], root/main: [M N]"
	found := strings.Join(out, ", ")
	compare(t, expect, found)
}

func compare(t *testing.T, expect, found string) {
	t.Helper()
	if expect != found {
		t.Errorf("\nexpect: %q\nfound : %q", expect, found)
	}
}

func compareSrc(t *testing.T, expect, found string) {
	t.Helper()
	bFound, err := format.Source([]byte(found))
	if err != nil {
		t.Fatal(err)
	}
	bExpect, err := format.Source([]byte(expect))
	if err != nil {
		t.Fatal(err)
	}
	expect = string(bExpect)
	found = string(bFound)
	if expect != found {
		t.Errorf("\nexpect: %q\nfound : %q", expect, found)
	}
}
