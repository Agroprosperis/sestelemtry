package edge

import (
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/grid-x/modbus"

	"github.com/nesh/sestelemetry/internal/registers"
)

type fakeHolding struct {
	hold func(start, qty uint16) ([]byte, error)
	unit func(unitID byte, start, qty uint16) ([]byte, error)
}

func (f fakeHolding) ReadHolding(_ context.Context, start, qty uint16) ([]byte, error) {
	if f.hold == nil {
		return nil, errors.New("hold not stubbed")
	}
	return f.hold(start, qty)
}

func (f fakeHolding) ReadHoldingUnit(_ context.Context, unitID byte, start, qty uint16) ([]byte, error) {
	if f.unit == nil {
		return nil, errors.New("unit not stubbed")
	}
	return f.unit(unitID, start, qty)
}

func testCatalog(t *testing.T, keys []string) []registers.ResolvedEntry {
	t.Helper()
	cat, err := registers.Load("../../registers/huawei_smartlogger.yaml")
	if err != nil {
		t.Fatal(err)
	}
	all, err := cat.Resolve(0)
	if err != nil {
		t.Fatal(err)
	}
	got, err := registers.Subset(all, keys)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func zeros(qty uint16) []byte { return make([]byte, int(qty)*2) }

func TestPollSoftDropsExceptionChunkKeepsRest(t *testing.T) {
	entries := testCatalog(t, []string{"sl_alarm_7", "sl_alarm_8", "ess_active_power_adjustment_kw"})
	sess := fakeHolding{hold: func(start, qty uint16) ([]byte, error) {
		if start == 50006 {
			return nil, &modbus.Error{FunctionCode: 3, ExceptionCode: modbus.ExceptionCodeIllegalDataAddress}
		}
		out := zeros(qty)
		if start == 40381 { // INT32, gain 0.1: 100 kW = 1000
			binary.BigEndian.PutUint32(out, 1000)
		}
		return out, nil
	}}
	got, remaining, dropped, ioErr, err := pollSoft(context.Background(), sess, time.Second, entries, nil)
	if err != nil || ioErr != nil {
		t.Fatalf("err=%v ioErr=%v", err, ioErr)
	}
	if got["ess_active_power_adjustment_kw"] != 100 {
		t.Fatalf("readback = %v, want 100", got["ess_active_power_adjustment_kw"])
	}
	if _, ok := got["sl_alarm_7"]; ok {
		t.Fatal("alarm 7 must not survive an exception")
	}
	if !alarmWordsDropped(dropped) {
		t.Fatalf("dropped = %v, want sl_alarm_7/8", dropped)
	}
	if hasKey(remaining, "sl_alarm_7") || hasKey(remaining, "sl_alarm_8") {
		t.Fatal("refused words must leave the next cycle's plan")
	}
}

func TestPollSoftIOErrorStopsGroupKeepsEntries(t *testing.T) {
	// Two chunks (40378 and 40575…40577): the first times out, so the
	// second must not cost another request timeout this cycle.
	entries := testCatalog(t, []string{"pv_active_power_adjustment_kw", "grid_line_voltage_ab_v"})
	calls := 0
	sess := fakeHolding{hold: func(start, qty uint16) ([]byte, error) {
		calls++
		return nil, errors.New("i/o timeout")
	}}
	got, remaining, dropped, ioErr, err := pollSoft(context.Background(), sess, time.Second, entries, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ioErr == nil {
		t.Fatal("transport error must be reported")
	}
	if calls != 1 {
		t.Fatalf("requests = %d, want 1 (stop after the first transport error)", calls)
	}
	if len(got) != 0 || len(dropped) != 0 || len(remaining) != len(entries) {
		t.Fatalf("got=%v dropped=%v remaining=%d: keys stay planned, nothing decoded", got, dropped, len(remaining))
	}
}

func TestPollSoftMeterUsesUnitID(t *testing.T) {
	entries := testCatalog(t, []string{"meter_active_power_kw"})
	var saw byte
	sess := fakeHolding{unit: func(unitID byte, start, qty uint16) ([]byte, error) {
		saw = unitID
		out := zeros(qty)
		raw := int32(-150000) // INT32, gain 0.001: −150 kW
		binary.BigEndian.PutUint32(out, uint32(raw))
		return out, nil
	}}
	uid := byte(11)
	got, _, dropped, ioErr, err := pollSoft(context.Background(), sess, time.Second, entries, &uid)
	if err != nil || ioErr != nil || len(dropped) != 0 {
		t.Fatalf("err=%v ioErr=%v dropped=%v", err, ioErr, dropped)
	}
	if saw != 11 {
		t.Fatalf("unit id = %d, want 11", saw)
	}
	if v := got["meter_active_power_kw"]; v != -150 {
		t.Fatalf("meter = %v, want -150", v)
	}
}

func TestDeviceMetricKeyPrefixesDualSite(t *testing.T) {
	if got := DeviceMetricKey(RolePV, "grid_line_voltage_ab_v"); got != "pv_grid_line_voltage_ab_v" {
		t.Fatalf("pv line V = %s", got)
	}
	if got := DeviceMetricKey(RoleESS, "active_power_control_mode"); got != "ess_active_power_control_mode" {
		t.Fatalf("ess 40737 = %s", got)
	}
	if got := DeviceMetricKey(RolePV, "pv_rated_kw"); got != "pv_rated_kw" {
		t.Fatalf("unique keys must stay unprefixed, got %s", got)
	}
	if got := DeviceMetricKey(RoleAll, "grid_line_voltage_ab_v"); got != "grid_line_voltage_ab_v" {
		t.Fatalf("single topology must stay unprefixed, got %s", got)
	}
}

func TestMergeSoftStoresPrefixedKeys(t *testing.T) {
	entries := testCatalog(t, []string{"grid_line_voltage_ab_v"})
	sess := fakeHolding{hold: func(start, qty uint16) ([]byte, error) {
		out := zeros(qty)
		binary.BigEndian.PutUint16(out, 4000) // gain 0.1: 400 V
		return out, nil
	}}
	held := map[string]float64{}
	_, dropped, ioErr, err := mergeSoft(context.Background(), sess, Device{Role: RolePV}, entries, held, nil)
	if err != nil || ioErr != nil || len(dropped) != 0 {
		t.Fatalf("err=%v ioErr=%v dropped=%v", err, ioErr, dropped)
	}
	if held["pv_grid_line_voltage_ab_v"] != 400 {
		t.Fatalf("held = %v", held)
	}
}

func TestMergeSoftFailureClearsHeldValue(t *testing.T) {
	// A value must not outlive a failed read: stale readback or
	// voltage would read as live in the journal and health.
	entries := testCatalog(t, []string{"meter_phase_a_voltage_v"})
	held := map[string]float64{"meter_phase_a_voltage_v": 231}
	sess := fakeHolding{unit: func(byte, uint16, uint16) ([]byte, error) {
		return nil, errors.New("i/o timeout")
	}}
	uid := byte(11)
	_, _, ioErr, err := mergeSoft(context.Background(), sess, Device{Role: RolePV}, entries, held, &uid)
	if err != nil || ioErr == nil {
		t.Fatalf("err=%v ioErr=%v, want transport error", err, ioErr)
	}
	if _, ok := held["meter_phase_a_voltage_v"]; ok {
		t.Fatalf("held = %v, stale value must be removed", held)
	}
}

func TestSoftDueBacksOffAfterTransportError(t *testing.T) {
	log := slog.New(slog.NewTextHandler(nopWriter{}, nil))
	now := testTS
	if got := softDue(log, "slow", now, slowReadInterval, nil); !got.Equal(now.Add(slowReadInterval)) {
		t.Fatalf("ok: next = %v, want +1h", got.Sub(now))
	}
	if got := softDue(log, "slow", now, slowReadInterval, errors.New("timeout")); !got.Equal(now.Add(softReadBackoff)) {
		t.Fatalf("io error: next = %v, want +%v (not an hour)", got.Sub(now), softReadBackoff)
	}
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }
