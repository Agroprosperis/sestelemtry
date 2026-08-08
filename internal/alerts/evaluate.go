// Package alerts decides when a monitored site has stopped reporting
// telemetry and turns that decision into an email.
//
// The package is deliberately free of database and network access: the
// watchdog daemon reads freshness timestamps and previous state, hands
// them to Evaluate as plain values, and gets back the list of things to
// announce plus the state to persist. That keeps the interesting logic
// (thresholds, de-duplication, reminder cadence) unit-testable without a
// Postgres or an SMTP server.
package alerts

import "time"

// Connectivity states. They match the strings persisted in
// device_alert_state so the daemon can pass them through unchanged.
const (
	StateOK   = "ok"
	StateDown = "down"
)

// Kind distinguishes the three announcements the watchdog can make.
type Kind string

const (
	// KindDown is the first email about an outage.
	KindDown Kind = "down"
	// KindReminder repeats an ongoing outage every RepeatInterval so a
	// multi-day fault does not fall off the operator's radar.
	KindReminder Kind = "reminder"
	// KindRecovered reports that a device started reporting again.
	KindRecovered Kind = "recovered"
)

// Device identifies one monitored Modbus endpoint.
type Device struct {
	OrganizationID string
	// OrganizationName is the human label from the config (e.g.
	// "Кролевецький елеватор"); falls back to the id when unset.
	OrganizationName string
	Host             string
}

// Key is the map key for a device: the same pair that forms the primary
// key of device_alert_state.
type Key struct {
	OrganizationID string
	Host           string
}

// Key returns the identity of the device.
func (d Device) Key() Key {
	return Key{OrganizationID: d.OrganizationID, Host: d.Host}
}

// Label renders the device for humans: organization name plus host.
func (d Device) Label() string {
	name := d.OrganizationName
	if name == "" {
		name = d.OrganizationID
	}
	if d.Host == "" {
		return name
	}
	return name + " (" + d.Host + ")"
}

// Observation is one freshness reading for a device. LastSampleAt is nil
// when the device wrote nothing inside the watchdog's lookback window —
// either it has been down for a long time, or it has never reported.
type Observation struct {
	Device
	LastSampleAt *time.Time
}

// State is what the watchdog concluded about a device last time.
type State struct {
	State string
	// Since is when the current State began: for StateDown it is the
	// start of the outage, which the email renders as a duration.
	Since          time.Time
	LastSampleAt   *time.Time
	LastNotifiedAt *time.Time
}

// Thresholds are the tunables from the `alerts` config block.
type Thresholds struct {
	// StaleAfter is the silence that counts as a lost connection.
	StaleAfter time.Duration
	// RepeatInterval re-announces an ongoing outage. Zero or negative
	// disables reminders.
	RepeatInterval time.Duration
	// NotifyRecovery enables the "back online" email.
	NotifyRecovery bool
}

// Event is a single thing to announce in this tick's email.
type Event struct {
	Kind Kind
	Device
	// LastSampleAt is the freshest telemetry seen at the time of the
	// check; nil when the device produced nothing in the lookback
	// window.
	LastSampleAt *time.Time
	// Since is the start of the outage this event talks about. For
	// KindRecovered it is when the outage began, so the email can
	// report how long the site was dark.
	Since time.Time
	// At is the check timestamp, so durations render consistently
	// across every event in one email.
	At time.Time
}

// Duration is how long the outage has lasted (KindDown, KindReminder)
// or did last (KindRecovered).
func (e Event) Duration() time.Duration {
	if e.Since.IsZero() || e.At.Before(e.Since) {
		return 0
	}
	return e.At.Sub(e.Since)
}

// Result carries the announcements for this tick and the state to
// persist afterwards.
type Result struct {
	Events []Event
	// States holds an entry for every observed device, whether or not
	// it produced an event, so the daemon can persist last_sample_at
	// even on a quiet tick.
	States map[Key]State
}

// Evaluate compares fresh observations against the previous state and
// returns what to announce.
//
// Rules:
//   - A device is down when it has no sample at all in the lookback
//     window, or its freshest sample is older than StaleAfter.
//   - The first tick that sees a device down emits KindDown. The outage
//     start is the last known sample time (so the email reports the real
//     duration, not the time since we noticed).
//   - While it stays down, KindReminder is emitted once per
//     RepeatInterval after the last successful notification.
//   - Coming back emits KindRecovered when NotifyRecovery is on.
//   - A device the watchdog has never seen before and that is healthy
//     produces no event: a fresh deployment must not email about sites
//     that are perfectly fine.
//
// Notification timestamps in the returned States assume the email is
// sent successfully; call Result.RevertNotifications when it is not.
func Evaluate(observations []Observation, prev map[Key]State, now time.Time, th Thresholds) Result {
	res := Result{States: make(map[Key]State, len(observations))}
	for _, obs := range observations {
		key := obs.Key()
		down := isDown(obs.LastSampleAt, now, th.StaleAfter)
		before, seen := prev[key]

		switch {
		case !seen:
			if !down {
				res.States[key] = State{State: StateOK, Since: now, LastSampleAt: obs.LastSampleAt}
				continue
			}
			since := outageStart(obs.LastSampleAt, now)
			res.Events = append(res.Events, event(KindDown, obs, since, now))
			res.States[key] = State{
				State:          StateDown,
				Since:          since,
				LastSampleAt:   obs.LastSampleAt,
				LastNotifiedAt: &now,
			}

		case down && before.State != StateDown:
			since := outageStart(obs.LastSampleAt, now)
			res.Events = append(res.Events, event(KindDown, obs, since, now))
			res.States[key] = State{
				State:          StateDown,
				Since:          since,
				LastSampleAt:   obs.LastSampleAt,
				LastNotifiedAt: &now,
			}

		case down:
			// Still down. Re-announce when the previous send failed
			// (nothing recorded yet) or the reminder is due.
			next := State{
				State:          StateDown,
				Since:          before.Since,
				LastSampleAt:   obs.LastSampleAt,
				LastNotifiedAt: before.LastNotifiedAt,
			}
			switch {
			case before.LastNotifiedAt == nil:
				res.Events = append(res.Events, event(KindDown, obs, before.Since, now))
				next.LastNotifiedAt = &now
			case th.RepeatInterval > 0 && now.Sub(*before.LastNotifiedAt) >= th.RepeatInterval:
				res.Events = append(res.Events, event(KindReminder, obs, before.Since, now))
				next.LastNotifiedAt = &now
			}
			res.States[key] = next

		case before.State == StateDown:
			// Recovered.
			next := State{State: StateOK, Since: now, LastSampleAt: obs.LastSampleAt}
			if th.NotifyRecovery {
				res.Events = append(res.Events, event(KindRecovered, obs, before.Since, now))
				next.LastNotifiedAt = &now
			}
			res.States[key] = next

		default:
			// Healthy and stayed healthy: keep the original `since` so
			// the row reflects how long the link has been stable.
			res.States[key] = State{
				State:        StateOK,
				Since:        before.Since,
				LastSampleAt: obs.LastSampleAt,
			}
		}
	}
	return res
}

// RevertNotifications rolls last_notified_at back to its previous value
// for every device that produced an event. The daemon calls this when
// the email could not be delivered, so the next check retries instead of
// recording a notification that never reached anyone.
func (r Result) RevertNotifications(prev map[Key]State) {
	for _, e := range r.Events {
		key := e.Key()
		next, ok := r.States[key]
		if !ok {
			continue
		}
		if before, seen := prev[key]; seen {
			next.LastNotifiedAt = before.LastNotifiedAt
		} else {
			next.LastNotifiedAt = nil
		}
		r.States[key] = next
	}
}

func event(kind Kind, obs Observation, since, now time.Time) Event {
	return Event{
		Kind:         kind,
		Device:       obs.Device,
		LastSampleAt: obs.LastSampleAt,
		Since:        since,
		At:           now,
	}
}

func isDown(last *time.Time, now time.Time, staleAfter time.Duration) bool {
	if last == nil {
		return true
	}
	return now.Sub(*last) > staleAfter
}

// outageStart prefers the last known sample as the beginning of the
// outage; a device that never reported has no such anchor, so the
// check time is used instead.
func outageStart(last *time.Time, now time.Time) time.Time {
	if last == nil {
		return now
	}
	return *last
}
