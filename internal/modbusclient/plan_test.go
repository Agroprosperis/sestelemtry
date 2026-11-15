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

func TestPlanChunks_readBlockSpansGaps(t *testing.T) {
	entries := []registers.ResolvedEntry{
		{Entry: registers.Entry{MetricKey: "a"}, PDUStart: 0, WordCount: 2, PDUEnd: 1, ReadBlock: 1},
		{Entry: registers.Entry{MetricKey: "b"}, PDUStart: 38, WordCount: 2, PDUEnd: 39, ReadBlock: 1},
		{Entry: registers.Entry{MetricKey: "c"}, PDUStart: 199, WordCount: 1, PDUEnd: 199},
		{Entry: registers.Entry{MetricKey: "d"}, PDUStart: 210, WordCount: 1, PDUEnd: 210},
	}
	ch := PlanChunks(entries)
	want := []ReadChunk{{Start: 0, Quantity: 40}, {Start: 199, Quantity: 1}, {Start: 210, Quantity: 1}}
	if len(ch) != len(want) {
		t.Fatalf("chunks = %+v, want %+v", ch, want)
	}
	for i := range want {
		if ch[i] != want[i] {
			t.Fatalf("chunks = %+v, want %+v", ch, want)
		}
	}
}

func TestPlanChunks_readBlockRespectsReadLimit(t *testing.T) {
	t.Setenv("SESTELEMETRY_MAX_REGISTERS_PER_READ", "20")
	entries := []registers.ResolvedEntry{
		{Entry: registers.Entry{MetricKey: "a"}, PDUStart: 0, WordCount: 2, PDUEnd: 1, ReadBlock: 1},
		{Entry: registers.Entry{MetricKey: "b"}, PDUStart: 38, WordCount: 2, PDUEnd: 39, ReadBlock: 1},
	}
	if ch := PlanChunks(entries); len(ch) != 2 {
		t.Fatalf("a block longer than the read limit must split, got %+v", ch)
	}
}

// TestPlanChunks_encombiPoll pins the request count of one Encombi
// poll: the handoff allows at most 3 requests a second on average and
// the collector polls every 5 s.
func TestPlanChunks_encombiPoll(t *testing.T) {
	cat, err := registers.Load("../../registers/encombi_ecpvx.yaml")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := cat.Resolve(0)
	if err != nil {
		t.Fatal(err)
	}
	ch := PlanChunks(resolved)
	if len(ch) != 8 {
		t.Fatalf("Encombi poll = %d requests, want 8: %+v", len(ch), ch)
	}
	for _, e := range resolved {
		found := false
		for _, c := range ch {
			if e.PDUStart >= c.Start && e.PDUEnd < c.Start+c.Quantity {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s (%d) is in no read: %+v", e.MetricKey, e.PDUStart, ch)
		}
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
