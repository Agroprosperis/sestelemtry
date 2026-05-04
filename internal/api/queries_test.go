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

// TestBucketAtLeastOneDay locks in the bucket-routing decision so a
// future regression can't accidentally route the day-preset's 5-minute
// (or hour-bucket) queries through the daily CAGG. Day-preset reads
// must always hit the raw hypertable to preserve intra-day granularity.
func TestBucketAtLeastOneDay(t *testing.T) {
	cases := []struct {
		bucket string
		want   bool
	}{
		{"", false},
		{"5 minutes", false},
		{"15 minutes", false},
		{"30 seconds", false},
		{"1 hour", false},
		{"6 hours", false},
		{"1 day", true},
		{"2 days", true},
		{"1 week", true},
		{"1 month", true},
		{"1 year", true},
		{"  1 DAY  ", true},
	}
	for _, tc := range cases {
		if got := bucketAtLeastOneDay(tc.bucket); got != tc.want {
			t.Errorf("bucketAtLeastOneDay(%q) = %v, want %v", tc.bucket, got, tc.want)
		}
	}
}
