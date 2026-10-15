package main

import (
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/counterstitch"
)

func TestDayRanges(t *testing.T) {
	kyiv, _ := time.LoadLocation("Europe/Kyiv")
	day := func(m time.Month, d, h int) time.Time { return time.Date(2026, m, d, h, 0, 0, 0, kyiv) }
	yesterday := day(10, 14, 0)

	got := dayRanges([]counterstitch.Span{
		{From: day(7, 20, 10), To: day(7, 20, 16)}, // a gap ramp inside one day
		{From: day(7, 21, 2), To: day(7, 21, 2)},   // the next flip, merged with it
		{From: day(9, 1, 5), To: day(9, 1, 5)},     // an isolated step
		{From: day(10, 13, 9), To: day(10, 15, 9)}, // runs into today: cut at yesterday
		{From: day(10, 15, 1), To: day(10, 15, 1)}, // today only: left to the today pass
	}, kyiv, yesterday)

	want := [][2]time.Time{
		{day(7, 20, 0), day(7, 23, 0)},
		{day(9, 1, 0), day(9, 3, 0)},
		{day(10, 13, 0), day(10, 14, 0)},
	}
	if len(got) != len(want) {
		t.Fatalf("ranges = %v", got)
	}
	for i := range want {
		if !got[i][0].Equal(want[i][0]) || !got[i][1].Equal(want[i][1]) {
			t.Fatalf("range %d = %v, want %v", i, got[i], want[i])
		}
	}
}
