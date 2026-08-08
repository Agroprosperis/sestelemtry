package alerts

import (
	"strings"
	"testing"
	"time"
)

func downEvent(d Device, last time.Duration) Event {
	return Event{Kind: KindDown, Device: d, LastSampleAt: at(last), Since: now.Add(last), At: now}
}

func TestBuildMessageEmpty(t *testing.T) {
	if _, ok := BuildMessage(nil, time.UTC); ok {
		t.Fatal("no events must produce no message")
	}
}

func TestBuildMessageSingleOutage(t *testing.T) {
	msg, ok := BuildMessage([]Event{downEvent(ke, -25*time.Minute)}, time.UTC)
	if !ok {
		t.Fatal("expected a message")
	}
	if want := "СЕС: втрачено звʼязок — Кролевецький елеватор (10.24.40.238)"; msg.Subject != want {
		t.Fatalf("subject = %q, want %q", msg.Subject, want)
	}
	for _, want := range []string{
		"Втрачено звʼязок (1):",
		"Кролевецький елеватор (10.24.40.238)",
		"останні дані: 08.08.2026 11:35:00, немає 25 хв",
		"Перевірено: 08.08.2026 12:00:00",
	} {
		if !strings.Contains(msg.Body, want) {
			t.Fatalf("body missing %q:\n%s", want, msg.Body)
		}
	}
}

// TestBuildMessageOneMailPerTick is the whole point of batching: a
// site-wide network drop must not fan out into one email per elevator.
func TestBuildMessageOneMailPerTick(t *testing.T) {
	msg, ok := BuildMessage([]Event{
		downEvent(ke, -25*time.Minute),
		downEvent(pde, -40*time.Minute),
	}, time.UTC)
	if !ok {
		t.Fatal("expected a message")
	}
	if want := "СЕС: втрачено звʼязок — 2 пристрої"; msg.Subject != want {
		t.Fatalf("subject = %q, want %q", msg.Subject, want)
	}
	if !strings.Contains(msg.Body, "Втрачено звʼязок (2):") {
		t.Fatalf("body must list both devices:\n%s", msg.Body)
	}
	if !strings.Contains(msg.Body, "Поділля елеватор") || !strings.Contains(msg.Body, "Кролевецький елеватор") {
		t.Fatalf("body missing a device:\n%s", msg.Body)
	}
}

func TestBuildMessageReminderOnlySubject(t *testing.T) {
	e := downEvent(ke, -3*time.Hour)
	e.Kind = KindReminder
	msg, _ := BuildMessage([]Event{e}, time.UTC)
	if !strings.HasPrefix(msg.Subject, "СЕС: звʼязок досі відсутній") {
		t.Fatalf("subject = %q", msg.Subject)
	}
	if !strings.Contains(msg.Body, "Звʼязок досі відсутній (1):") {
		t.Fatalf("body:\n%s", msg.Body)
	}
}

func TestBuildMessageRecovery(t *testing.T) {
	e := Event{Kind: KindRecovered, Device: pde, LastSampleAt: at(-time.Second), Since: now.Add(-64 * time.Minute), At: now}
	msg, _ := BuildMessage([]Event{e}, time.UTC)
	if want := "СЕС: звʼязок відновлено — Поділля елеватор (10.33.40.241)"; msg.Subject != want {
		t.Fatalf("subject = %q, want %q", msg.Subject, want)
	}
	if !strings.Contains(msg.Body, "простій тривав 1 год 4 хв") {
		t.Fatalf("body:\n%s", msg.Body)
	}
}

// TestBuildMessageMixedPrefersOutage keeps the alarming half of a mixed
// tick in the subject line, where an operator scanning an inbox sees it.
func TestBuildMessageMixedPrefersOutage(t *testing.T) {
	msg, _ := BuildMessage([]Event{
		{Kind: KindRecovered, Device: pde, LastSampleAt: at(-time.Second), Since: now.Add(-time.Hour), At: now},
		downEvent(ke, -25*time.Minute),
	}, time.UTC)
	if !strings.HasPrefix(msg.Subject, "СЕС: втрачено звʼязок") {
		t.Fatalf("subject = %q", msg.Subject)
	}
	if !strings.Contains(msg.Body, "Звʼязок відновлено (1):") {
		t.Fatalf("recovery must still be listed:\n%s", msg.Body)
	}
}

func TestBuildMessageNoSampleAtAll(t *testing.T) {
	e := Event{Kind: KindDown, Device: ke, Since: now, At: now}
	msg, _ := BuildMessage([]Event{e}, time.UTC)
	if !strings.Contains(msg.Body, "даних немає взагалі") {
		t.Fatalf("body:\n%s", msg.Body)
	}
}

func TestBuildMessageRendersInLocation(t *testing.T) {
	kyiv, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	msg, _ := BuildMessage([]Event{downEvent(ke, -25*time.Minute)}, kyiv)
	if !strings.Contains(msg.Body, "останні дані: 08.08.2026 14:35:00") {
		t.Fatalf("timestamps must render in the configured zone:\n%s", msg.Body)
	}
}

func TestBuildTestMessageListsFleet(t *testing.T) {
	msg := BuildTestMessage([]Device{ke, pde}, now, time.UTC)
	if msg.Subject != "СЕС: тест сповіщень" {
		t.Fatalf("subject = %q", msg.Subject)
	}
	if !strings.Contains(msg.Body, "Під наглядом (2):") {
		t.Fatalf("body:\n%s", msg.Body)
	}
	if !strings.Contains(msg.Body, "Кролевецький елеватор (10.24.40.238)") {
		t.Fatalf("body:\n%s", msg.Body)
	}
}

func TestBuildTestMessageWarnsOnEmptyFleet(t *testing.T) {
	msg := BuildTestMessage(nil, now, time.UTC)
	if !strings.Contains(msg.Body, "немає жодного пристрою") {
		t.Fatalf("body:\n%s", msg.Body)
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{30 * time.Second, "менше хвилини"},
		{time.Minute, "1 хв"},
		{25 * time.Minute, "25 хв"},
		{time.Hour, "1 год"},
		{64 * time.Minute, "1 год 4 хв"},
		{25 * time.Hour, "1 день 1 год"},
		{48 * time.Hour, "2 дні"},
		{5 * 24 * time.Hour, "5 днів"},
		{11 * 24 * time.Hour, "11 днів"},
		{21 * 24 * time.Hour, "21 день"},
	}
	for _, c := range cases {
		if got := formatDuration(c.in); got != c.want {
			t.Errorf("formatDuration(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDeviceCountPlurals(t *testing.T) {
	cases := map[int]string{1: "1 пристрій", 2: "2 пристрої", 5: "5 пристроїв", 11: "11 пристроїв", 21: "21 пристрій"}
	for n, want := range cases {
		if got := deviceCount(n); got != want {
			t.Errorf("deviceCount(%d) = %q, want %q", n, got, want)
		}
	}
}
