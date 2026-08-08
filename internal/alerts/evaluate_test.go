package alerts

import (
	"testing"
	"time"
)

var (
	now = time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	ke  = Device{OrganizationID: "ke", OrganizationName: "Кролевецький елеватор", Host: "10.24.40.238"}
	pde = Device{OrganizationID: "pde", OrganizationName: "Поділля елеватор", Host: "10.33.40.241"}
)

func at(d time.Duration) *time.Time {
	t := now.Add(d)
	return &t
}

func defaults() Thresholds {
	return Thresholds{StaleAfter: 10 * time.Minute, RepeatInterval: 6 * time.Hour, NotifyRecovery: true}
}

func kinds(events []Event) []Kind {
	out := make([]Kind, 0, len(events))
	for _, e := range events {
		out = append(out, e.Kind)
	}
	return out
}

func TestEvaluateFirstSightHealthyIsSilent(t *testing.T) {
	// A fresh deployment must not email about sites that are fine.
	res := Evaluate(
		[]Observation{{Device: ke, LastSampleAt: at(-2 * time.Second)}},
		map[Key]State{},
		now,
		defaults(),
	)
	if len(res.Events) != 0 {
		t.Fatalf("expected no events, got %v", kinds(res.Events))
	}
	st := res.States[ke.Key()]
	if st.State != StateOK {
		t.Fatalf("state = %q, want %q", st.State, StateOK)
	}
	if st.LastNotifiedAt != nil {
		t.Fatal("healthy device must not record a notification")
	}
}

func TestEvaluateGoesDown(t *testing.T) {
	last := at(-25 * time.Minute)
	res := Evaluate(
		[]Observation{{Device: ke, LastSampleAt: last}},
		map[Key]State{ke.Key(): {State: StateOK, Since: now.Add(-time.Hour)}},
		now,
		defaults(),
	)
	if got := kinds(res.Events); len(got) != 1 || got[0] != KindDown {
		t.Fatalf("events = %v, want [down]", got)
	}
	// The outage starts at the last sample, not at the moment we
	// noticed, so the email reports the real duration.
	if !res.Events[0].Since.Equal(*last) {
		t.Fatalf("since = %v, want %v", res.Events[0].Since, *last)
	}
	if got, want := res.Events[0].Duration(), 25*time.Minute; got != want {
		t.Fatalf("duration = %v, want %v", got, want)
	}
	st := res.States[ke.Key()]
	if st.State != StateDown || st.LastNotifiedAt == nil || !st.LastNotifiedAt.Equal(now) {
		t.Fatalf("state = %+v, want down notified at now", st)
	}
}

func TestEvaluateNeverReportedDeviceIsDown(t *testing.T) {
	res := Evaluate(
		[]Observation{{Device: ke, LastSampleAt: nil}},
		map[Key]State{},
		now,
		defaults(),
	)
	if got := kinds(res.Events); len(got) != 1 || got[0] != KindDown {
		t.Fatalf("events = %v, want [down]", got)
	}
	if !res.Events[0].Since.Equal(now) {
		t.Fatalf("with no sample the outage must be anchored at the check time, got %v", res.Events[0].Since)
	}
}

func TestEvaluateSuppressesRepeatUntilInterval(t *testing.T) {
	th := defaults()
	notified := now.Add(-5 * time.Hour)
	prev := map[Key]State{ke.Key(): {
		State:          StateDown,
		Since:          now.Add(-6 * time.Hour),
		LastNotifiedAt: &notified,
	}}
	res := Evaluate([]Observation{{Device: ke, LastSampleAt: at(-6 * time.Hour)}}, prev, now, th)
	if len(res.Events) != 0 {
		t.Fatalf("expected silence before repeat_interval, got %v", kinds(res.Events))
	}
	if st := res.States[ke.Key()]; st.LastNotifiedAt == nil || !st.LastNotifiedAt.Equal(notified) {
		t.Fatalf("notification timestamp must be preserved, got %+v", st)
	}
}

func TestEvaluateRemindsAfterInterval(t *testing.T) {
	th := defaults()
	notified := now.Add(-6 * time.Hour)
	since := now.Add(-7 * time.Hour)
	prev := map[Key]State{ke.Key(): {State: StateDown, Since: since, LastNotifiedAt: &notified}}
	res := Evaluate([]Observation{{Device: ke, LastSampleAt: nil}}, prev, now, th)
	if got := kinds(res.Events); len(got) != 1 || got[0] != KindReminder {
		t.Fatalf("events = %v, want [reminder]", got)
	}
	if !res.Events[0].Since.Equal(since) {
		t.Fatalf("reminder must keep the original outage start, got %v", res.Events[0].Since)
	}
	if st := res.States[ke.Key()]; st.Since != since {
		t.Fatalf("stored since changed: %v", st.Since)
	}
}

func TestEvaluateRepeatIntervalZeroDisablesReminders(t *testing.T) {
	th := defaults()
	th.RepeatInterval = 0
	notified := now.Add(-30 * 24 * time.Hour)
	prev := map[Key]State{ke.Key(): {State: StateDown, Since: notified, LastNotifiedAt: &notified}}
	res := Evaluate([]Observation{{Device: ke, LastSampleAt: nil}}, prev, now, th)
	if len(res.Events) != 0 {
		t.Fatalf("expected no reminder, got %v", kinds(res.Events))
	}
}

func TestEvaluateRetriesWhenPreviousSendFailed(t *testing.T) {
	// last_notified_at is nil after a failed delivery: the operator was
	// never told, so the next check must announce it again.
	prev := map[Key]State{ke.Key(): {State: StateDown, Since: now.Add(-time.Hour)}}
	res := Evaluate([]Observation{{Device: ke, LastSampleAt: nil}}, prev, now, defaults())
	if got := kinds(res.Events); len(got) != 1 || got[0] != KindDown {
		t.Fatalf("events = %v, want [down]", got)
	}
}

func TestEvaluateRecovery(t *testing.T) {
	notified := now.Add(-time.Hour)
	since := now.Add(-2 * time.Hour)
	prev := map[Key]State{ke.Key(): {State: StateDown, Since: since, LastNotifiedAt: &notified}}
	res := Evaluate([]Observation{{Device: ke, LastSampleAt: at(-time.Second)}}, prev, now, defaults())
	if got := kinds(res.Events); len(got) != 1 || got[0] != KindRecovered {
		t.Fatalf("events = %v, want [recovered]", got)
	}
	if got, want := res.Events[0].Duration(), 2*time.Hour; got != want {
		t.Fatalf("outage duration = %v, want %v", got, want)
	}
	st := res.States[ke.Key()]
	if st.State != StateOK || !st.Since.Equal(now) {
		t.Fatalf("state = %+v, want ok since now", st)
	}
}

func TestEvaluateRecoverySilentWhenDisabled(t *testing.T) {
	th := defaults()
	th.NotifyRecovery = false
	notified := now.Add(-time.Hour)
	prev := map[Key]State{ke.Key(): {State: StateDown, Since: now.Add(-2 * time.Hour), LastNotifiedAt: &notified}}
	res := Evaluate([]Observation{{Device: ke, LastSampleAt: at(-time.Second)}}, prev, now, th)
	if len(res.Events) != 0 {
		t.Fatalf("expected no events, got %v", kinds(res.Events))
	}
	if st := res.States[ke.Key()]; st.LastNotifiedAt != nil {
		t.Fatal("a silent recovery must clear the notification marker")
	}
}

func TestEvaluateExactThresholdIsNotDown(t *testing.T) {
	// stale_after is a "longer than" bound: a sample exactly at the
	// threshold still counts as alive.
	res := Evaluate(
		[]Observation{{Device: ke, LastSampleAt: at(-10 * time.Minute)}},
		map[Key]State{ke.Key(): {State: StateOK, Since: now.Add(-time.Hour)}},
		now,
		defaults(),
	)
	if len(res.Events) != 0 {
		t.Fatalf("expected no events, got %v", kinds(res.Events))
	}
}

func TestEvaluateKeepsHealthySince(t *testing.T) {
	since := now.Add(-72 * time.Hour)
	prev := map[Key]State{ke.Key(): {State: StateOK, Since: since}}
	res := Evaluate([]Observation{{Device: ke, LastSampleAt: at(-time.Second)}}, prev, now, defaults())
	if st := res.States[ke.Key()]; !st.Since.Equal(since) {
		t.Fatalf("since = %v, want unchanged %v", st.Since, since)
	}
}

func TestEvaluateHandlesMultipleDevices(t *testing.T) {
	prev := map[Key]State{
		ke.Key():  {State: StateOK, Since: now.Add(-time.Hour)},
		pde.Key(): {State: StateDown, Since: now.Add(-3 * time.Hour), LastNotifiedAt: at(-3 * time.Hour)},
	}
	res := Evaluate([]Observation{
		{Device: ke, LastSampleAt: at(-time.Hour)},
		{Device: pde, LastSampleAt: at(-time.Second)},
	}, prev, now, defaults())
	if got := kinds(res.Events); len(got) != 2 || got[0] != KindDown || got[1] != KindRecovered {
		t.Fatalf("events = %v, want [down recovered]", got)
	}
	if len(res.States) != 2 {
		t.Fatalf("states = %d, want 2", len(res.States))
	}
}

func TestRevertNotificationsAfterFailedSend(t *testing.T) {
	previous := now.Add(-8 * time.Hour)
	prev := map[Key]State{
		ke.Key():  {State: StateDown, Since: now.Add(-9 * time.Hour), LastNotifiedAt: &previous},
		pde.Key(): {State: StateOK, Since: now.Add(-9 * time.Hour)},
	}
	res := Evaluate([]Observation{
		{Device: ke, LastSampleAt: nil},
		{Device: pde, LastSampleAt: nil},
	}, prev, now, defaults())
	if got := kinds(res.Events); len(got) != 2 {
		t.Fatalf("events = %v, want reminder + down", got)
	}

	res.RevertNotifications(prev, res.Events)

	if st := res.States[ke.Key()]; st.LastNotifiedAt == nil || !st.LastNotifiedAt.Equal(previous) {
		t.Fatalf("reminder rollback = %+v, want %v", st.LastNotifiedAt, previous)
	}
	if st := res.States[pde.Key()]; st.LastNotifiedAt != nil {
		t.Fatal("a device notified for the first time must roll back to nil")
	}
	// The state transition itself stays: only the notification marker
	// rolls back, so the next tick retries the email without treating
	// the outage as brand new.
	if st := res.States[pde.Key()]; st.State != StateDown {
		t.Fatalf("state = %q, want %q", st.State, StateDown)
	}
}

func TestDeviceLabelFallsBackToID(t *testing.T) {
	d := Device{OrganizationID: "ke", Host: "10.0.0.1"}
	if got, want := d.Label(), "ke (10.0.0.1)"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
}
