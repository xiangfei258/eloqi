package mock

import (
	"errors"
	"reflect"
	"testing"
)

func TestAutotypeRecordsText(t *testing.T) {
	a := &Autotype{}
	if err := a.Type("first"); err != nil {
		t.Fatal(err)
	}
	if err := a.Type("second"); err != nil {
		t.Fatal(err)
	}

	if got, want := a.Typed(), []string{"first", "second"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Typed = %v, want %v", got, want)
	}
}

func TestAutotypeInjectedError(t *testing.T) {
	want := errors.New("injection failed")
	a := &Autotype{Err: want}
	if err := a.Type("x"); err != want {
		t.Fatalf("Type err = %v, want %v", err, want)
	}
	if len(a.Typed()) != 0 {
		t.Fatalf("Typed = %v, want empty after error", a.Typed())
	}
}
