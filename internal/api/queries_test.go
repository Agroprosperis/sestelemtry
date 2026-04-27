package api

import "testing"

func TestParseCSV(t *testing.T) {
	got := ParseCSV("a, b ,,,c")
	if len(got) != 3 {
		t.Fatalf("expected 3 values, got %d", len(got))
	}
	if got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("unexpected values: %#v", got)
	}
}
