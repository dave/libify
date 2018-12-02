package libify

import (
	"context"
	"fmt"
	"testing"

	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/dave/dst/decorator/dummy"
)

func TestLibifier_Foo(t *testing.T) {
	dir, err := dummy.TempDir(map[string]string{
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
		},
	}
	if err := l.load(context.Background()); err != nil {
		t.Fatal(err)
	}
	expect := "[root/a root/main]"
	found := fmt.Sprint(l.paths)
	compare(t, expect, found)
}

func compare(t *testing.T, expect, found string) {
	if expect != found {
		t.Errorf("diff:\n%s", diff(expect, found))
	}
}

func diff(expect, found string) string {
	dmp := diffmatchpatch.New()
	return dmp.DiffPrettyText(dmp.DiffMain(expect, found, false))
}
