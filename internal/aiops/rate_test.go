package aiops

import "testing"

func TestRateBucket(t *testing.T) {
	h := &Handler{ratePerMin: 2}
	if !h.allow("a") || !h.allow("a") {
		t.Fatal("initial burst should pass")
	}
	if h.allow("a") {
		t.Fatal("third call should be limited")
	}
	if !h.allow("b") {
		t.Fatal("bucket must be per source")
	}
}
