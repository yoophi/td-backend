package domain

import (
	"errors"
	"testing"
)

func TestObservedRevision(t *testing.T) {
	for _, n := range []int64{1, 9, 9223372036854775807} {
		got, e := Observe(`"` + Revision(n) + `"`)
		if e != nil || got != n {
			t.Fatalf("%d: %d %v", n, got, e)
		}
	}
	if _, e := Observe(""); !errors.Is(e, ErrPreconditionRequired) {
		t.Fatal(e)
	}
	for _, value := range []string{`*`, `r-1`, `W/"r-1"`, `"r-1", "r-2"`, `"r-0"`, `"r--1"`, `"r-+1"`, `"r-01"`, `"r-"`, `"r-9223372036854775808"`, `"r-1 "`, `"r-1"junk`} {
		var invalid *ValidationError
		if _, e := Observe(value); !errors.As(e, &invalid) {
			t.Errorf("accepted %q: %v", value, e)
		}
	}
}
