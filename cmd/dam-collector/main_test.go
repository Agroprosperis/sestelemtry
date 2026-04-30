package main

import (
	"testing"
	"time"
)

func TestNextRunAt_TodayLater(t *testing.T) {
	tz, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, 5, 1, 9, 30, 0, 0, tz) // 09:30 local
	got := nextRunAt(now, tz, 14, 0)
	want := time.Date(2026, 5, 1, 14, 0, 0, 0, tz)
	if !got.Equal(want) {
		t.Fatalf("nextRunAt today-later: got %v want %v", got, want)
	}
}

func TestNextRunAt_AfterTodayWindow(t *testing.T) {
	tz, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, 5, 1, 16, 0, 0, 0, tz) // 16:00 local, run_at=14:00
	got := nextRunAt(now, tz, 14, 0)
	want := time.Date(2026, 5, 2, 14, 0, 0, 0, tz)
	if !got.Equal(want) {
		t.Fatalf("nextRunAt next-day: got %v want %v", got, want)
	}
}

func TestNextRunAt_EqualMomentSchedulesTomorrow(t *testing.T) {
	tz, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, 5, 1, 14, 0, 0, 0, tz) // exact match
	got := nextRunAt(now, tz, 14, 0)
	want := time.Date(2026, 5, 2, 14, 0, 0, 0, tz)
	if !got.Equal(want) {
		t.Fatalf("nextRunAt equal-moment: got %v want %v", got, want)
	}
}

func TestTargetDate_TomorrowOffset(t *testing.T) {
	tz, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, 5, 1, 14, 0, 0, 0, tz)
	got := targetDate(now, tz, 1)
	want := time.Date(2026, 5, 2, 0, 0, 0, 0, tz)
	if !got.Equal(want) {
		t.Fatalf("targetDate offset=1: got %v want %v", got, want)
	}
}

func TestTargetDate_YesterdayOffset(t *testing.T) {
	tz, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, 5, 1, 0, 30, 0, 0, tz)
	got := targetDate(now, tz, -1)
	want := time.Date(2026, 4, 30, 0, 0, 0, 0, tz)
	if !got.Equal(want) {
		t.Fatalf("targetDate offset=-1: got %v want %v", got, want)
	}
}
