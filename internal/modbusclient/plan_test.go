package modbusclient

import (
	"testing"

	"github.com/nesh/sestelemetry/internal/registers"
)

func TestPlanChunks_mergeAdjacent(t *testing.T) {
	entries := []registers.ResolvedEntry{
		{Entry: registers.Entry{MetricKey: "a"}, PDUStart: 10, WordCount: 2, PDUEnd: 11},
		{Entry: registers.Entry{MetricKey: "b"}, PDUStart: 12, WordCount: 2, PDUEnd: 13},
	}
	ch := PlanChunks(entries)
	if len(ch) != 1 {
		t.Fatalf("want 1 merged read, got %d %+v", len(ch), ch)
	}
	if ch[0].Start != 10 || ch[0].Quantity != 4 {
		t.Fatalf("merged chunk: %+v", ch[0])
	}
}

func TestSliceForEntry(t *testing.T) {
	e := registers.ResolvedEntry{
		Entry:     registers.Entry{MetricKey: "x"},
		PDUStart:  1,
		WordCount: 2,
		PDUEnd:    2,
	}
	b := []byte{0, 1, 0, 2, 0, 3, 0, 4}
	got := SliceForEntry(0, b, e)
	if len(got) != 4 {
		t.Fatal(len(got))
	}
}
