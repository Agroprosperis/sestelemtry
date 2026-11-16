package edge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Write events (ems_sl_write_cutover.md §4, §9; handoff §9.1).
const (
	EvWriteEnabled        = "WRITE_ENABLED"
	EvWriteDisabled       = "WRITE_DISABLED"
	EvWriteStop           = "WRITE_STOP"
	EvWriteFault          = "WRITE_FAULT"
	EvManualClamped       = "MANUAL_COMMAND_CLAMPED"
	EvWriteOffAfterReboot = "WRITE_OFF_AFTER_RESTART"
	EvWriteDenied         = "WRITE_ENABLE_DENIED"
)

// WriteGate is the device-local write flag (ems_sl_write_cutover.md §4,
// handoff §9.1 п. 1) and the only way into a SmartLogger write. It is
// off by default, never comes from the cloud or a mode, and changes only
// when a person asks on the local console or a stop turns it off; every
// change is an event. The file keeps what it was for the journal: the
// service always starts with it off, so a restart never resumes writing
// on its own.
type WriteGate struct {
	path string
	on   atomic.Bool

	mu    sync.Mutex
	state GateState
}

// GateState is the persisted record of the last change.
type GateState struct {
	Enabled bool      `json:"enabled"`
	At      time.Time `json:"at"`
	By      string    `json:"by"`
	Reason  string    `json:"reason,omitempty"`
}

// OpenWriteGate loads the flag file and comes up off. The returned event
// is non-nil when the flag had been on before this start.
func OpenWriteGate(path string, now time.Time) (*WriteGate, *Event, error) {
	g := &WriteGate{path: path}
	raw, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		return g, nil, nil
	case err != nil:
		return nil, nil, fmt.Errorf("write gate: %w", err)
	}
	var prev GateState
	if err := json.Unmarshal(raw, &prev); err != nil {
		// A torn file is no reason to start writing either.
		prev = GateState{}
	}
	g.state = prev
	if !prev.Enabled {
		return g, nil, nil
	}
	g.state = GateState{Enabled: false, At: now.UTC(), By: "edge", Reason: "перезапуск сервісу"}
	if err := g.persist(); err != nil {
		return nil, nil, err
	}
	ev := &Event{
		TS: now.UTC(), Severity: SevAlarm, Code: EvWriteOffAfterReboot,
		Message: "запис у SmartLogger вимкнено після перезапуску — вмикає лише людина",
		Context: map[string]any{"enabled_at": prev.At, "enabled_by": prev.By},
	}
	return g, ev, nil
}

// Enabled is checked right before every Modbus write.
func (g *WriteGate) Enabled() bool { return g != nil && g.on.Load() }

// State returns the last change.
func (g *WriteGate) State() GateState {
	if g == nil {
		return GateState{}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.state
}

// Enable turns writing on. The caller has already checked the
// preconditions.
func (g *WriteGate) Enable(by string, now time.Time) (Event, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.on.Load() {
		return Event{}, fmt.Errorf("запис уже ввімкнено")
	}
	g.state = GateState{Enabled: true, At: now.UTC(), By: by}
	if err := g.persist(); err != nil {
		g.state.Enabled = false
		return Event{}, err
	}
	g.on.Store(true)
	return Event{
		TS: now.UTC(), Severity: SevWarning, Code: EvWriteEnabled,
		Message: "запис у SmartLogger увімкнено: " + by,
		Context: map[string]any{"by": by},
	}, nil
}

// Disable turns writing off. It never fails to stop the writes: the flag
// drops first, a failed file write is only reported.
func (g *WriteGate) Disable(by, reason string, now time.Time) (Event, error) {
	g.on.Store(false)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.state = GateState{Enabled: false, At: now.UTC(), By: by, Reason: reason}
	err := g.persist()
	return Event{
		TS: now.UTC(), Severity: SevWarning, Code: EvWriteDisabled,
		Message: "запис у SmartLogger вимкнено: " + reason,
		Context: map[string]any{"by": by, "reason": reason},
	}, err
}

func (g *WriteGate) persist() error {
	if g.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(g.path), 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(g.state)
	if err != nil {
		return err
	}
	tmp := g.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, g.path)
}
