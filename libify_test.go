package libify

import "testing"

func TestLibifier_Foo(t *testing.T) {
	l := Libifier{}
	expect := 1
	found := l.Foo()
	if expect != found {
		t.Fatalf("expect %v, found %v", expect, found)
	}
}
