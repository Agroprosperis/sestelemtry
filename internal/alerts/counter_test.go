package alerts

import (
	"strings"
	"testing"
	"time"
)

func TestBuildCounterMessage(t *testing.T) {
	kyiv, _ := time.LoadLocation("Europe/Kyiv")
	start := time.Date(2026, 9, 26, 8, 12, 5, 0, kyiv)
	end := start.Add(26 * time.Hour)
	gap := 6890.0

	msg, ok := BuildCounterMessage([]CounterEvent{{
		OrganizationID:   "de",
		OrganizationName: "Дубовязівський елеватор",
		MetricKey:        "accumulated_electricity_purchased_kwh",
		Kind:             CounterKindDropout,
		StartedAt:        start,
		ValueBefore:      93642,
		ValueAfter:       8683,
	}}, start.Add(20*time.Minute), kyiv)
	if !ok {
		t.Fatal("expected a message")
	}
	if msg.Subject != "СЕС: збій лічильника — Дубовязівський елеватор — імпорт з мережі" {
		t.Fatalf("subject = %q", msg.Subject)
	}
	for _, want := range []string{"26.09.2026 08:12:05", "8 683 кВт·год замість 93 642 кВт·год", "SmartLogger"} {
		if !strings.Contains(msg.Body, want) {
			t.Fatalf("body misses %q:\n%s", want, msg.Body)
		}
	}

	msg, _ = BuildCounterMessage([]CounterEvent{{
		OrganizationID: "de",
		MetricKey:      "accumulated_electricity_purchased_kwh",
		Kind:           CounterKindDropout,
		Closed:         true,
		StartedAt:      start,
		EndedAt:        &end,
		GapKwh:         &gap,
	}, {
		OrganizationID:   "pde",
		OrganizationName: "Поділля елеватор",
		MetricKey:        "accumulated_electricity_purchased_kwh",
		Kind:             CounterKindFrozen,
		StartedAt:        time.Date(2026, 8, 18, 0, 0, 0, 0, kyiv),
		ValueBefore:      63291,
	}}, end, kyiv)
	if msg.Subject != "СЕС: збої лічильників — 1" {
		t.Fatalf("subject = %q", msg.Subject)
	}
	for _, want := range []string{"обрив тривав 1 день 2 год", "дораховано 6 890 кВт·год", "не змінюється з 18.08.2026", "63 291"} {
		if !strings.Contains(msg.Body, want) {
			t.Fatalf("body misses %q:\n%s", want, msg.Body)
		}
	}

	if _, ok := BuildCounterMessage(nil, end, kyiv); ok {
		t.Fatal("no events, no message")
	}
}
