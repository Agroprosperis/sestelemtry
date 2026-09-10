package edge

import (
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"
)

// healthTestService builds a Service with just enough state for
// buildHealth: config, a fresh reading for the single mock device, and
// optional tick/decision/fleet.
func healthTestService(cfg *Config, tick *Tick, dec *Decision) *Service {
	s := &Service{
		cfg:              cfg,
		log:              slog.Default(),
		startedAt:        time.Now(),
		eventLastWritten: map[string]time.Time{},
	}
	s.devPollOK.Store("mock", testTS.Unix())
	if tick != nil {
		s.lastTick.Store(tick)
	}
	if dec != nil {
		s.lastDecision.Store(dec)
	}
	return s
}

func findCheck(t *testing.T, h *HealthSnapshot, id string) *HealthCheck {
	t.Helper()
	for i := range h.Checks {
		if h.Checks[i].ID == id {
			return &h.Checks[i]
		}
	}
	return nil
}

func TestHealthBessShutdownFailsOK(t *testing.T) {
	tick := testTick(map[string]float64{
		"active_pv_power_kw": 0, "load_power_kw": 300, "soc_percent": 60,
		"active_ess_power_kw": 0, "pcs_shutdown": 1, "pcs_in_operation": 0,
	}, QualityOK)
	s := healthTestService(testCfg(), &tick, nil)
	h := s.buildHealth(testTS)

	if h.Bess == nil || h.Bess.Class != "shutdown" {
		t.Fatalf("bess class = %+v, want shutdown", h.Bess)
	}
	if c := findCheck(t, h, "pcs"); c == nil || c.Severity != CheckAlarm {
		t.Fatalf("pcs check = %+v, want alarm", c)
	}
	if c := findCheck(t, h, "bess"); c == nil || c.Severity != CheckAlarm {
		t.Fatalf("bess check = %+v, want alarm", c)
	}
	if h.OK {
		t.Fatal("root ok must be false on pcs shutdown")
	}
}

func TestHealthPcsZeroZeroIsWarningNotShutdown(t *testing.T) {
	// 40539 = 0 при 40540 = 0 → warning, НЕ shutdown (spec §4: ризик
	// порожнього регістра).
	tick := testTick(map[string]float64{
		"active_pv_power_kw": 100, "load_power_kw": 100, "soc_percent": 60,
		"active_ess_power_kw": 0.5, "pcs_shutdown": 0, "pcs_in_operation": 0,
	}, QualityOK)
	s := healthTestService(testCfg(), &tick, nil)
	h := s.buildHealth(testTS)

	c := findCheck(t, h, "pcs")
	if c == nil || c.Severity != CheckWarning {
		t.Fatalf("pcs check = %+v, want warning", c)
	}
	if h.Bess == nil || h.Bess.Class == "shutdown" {
		t.Fatalf("bess class = %v — 40539=0 must not read as shutdown", h.Bess)
	}
	if h.Bess.Class != "hold" {
		t.Fatalf("bess class = %s, want hold (|P| ≤ 2)", h.Bess.Class)
	}
}

func TestHealthBessInventoryMismatchWarns(t *testing.T) {
	cfg := testCfg()
	cfg.Limits.Bess.PassportKw = 864
	cfg.Limits.Bess.PassportKwh = 1720
	cfg.Limits.Bess.PassportEssCount = 8
	tick := testTick(map[string]float64{
		"active_pv_power_kw": 100, "load_power_kw": 100, "soc_percent": 60,
		"active_ess_power_kw": 0, "pcs_shutdown": 0, "pcs_in_operation": 1,
		"ess_rated_kw": 800, "ess_rated_kwh": 1720, "ess_count": 6,
	}, QualityOK)
	s := healthTestService(cfg, &tick, nil)
	h := s.buildHealth(testTS)

	c := findCheck(t, h, "bess_inventory")
	if c == nil || c.Severity != CheckWarning || c.OK {
		t.Fatalf("bess_inventory = %+v, want warning", c)
	}

	// Matching registers → ok.
	tick2 := testTick(map[string]float64{
		"active_pv_power_kw": 100, "load_power_kw": 100, "soc_percent": 60,
		"active_ess_power_kw": 0, "pcs_shutdown": 0, "pcs_in_operation": 1,
		"ess_rated_kw": 864, "ess_rated_kwh": 1720, "ess_count": 8,
	}, QualityOK)
	s2 := healthTestService(cfg, &tick2, nil)
	if c := findCheck(t, s2.buildHealth(testTS), "bess_inventory"); c == nil || c.Severity != CheckOK {
		t.Fatalf("matching inventory = %+v, want ok", c)
	}
}

func TestHealthNoPassportSkipsInventoryCheck(t *testing.T) {
	tick := testTick(map[string]float64{
		"active_pv_power_kw": 100, "load_power_kw": 100, "soc_percent": 60,
		"ess_rated_kw": 800,
	}, QualityOK)
	s := healthTestService(testCfg(), &tick, nil)
	if c := findCheck(t, s.buildHealth(testTS), "bess_inventory"); c != nil {
		t.Fatalf("bess_inventory present without passport: %+v", c)
	}
}

func standbyFleet(n int, ts time.Time) *inverterFleet {
	rows := make([]InverterSnapshot, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, InverterSnapshot{
			DeviceAddress: 12 + i, RegisterBase: inverterRegisterBase(12 + i),
			Class: InvStandby, StatusRaw: "0x0700",
			StatusLabel: "standby (немає опромінення)",
			PollOK:      true, TS: ts,
		})
	}
	return &inverterFleet{TS: ts, Inverters: rows}
}

func TestHealthNightStandbyDoesNotFailOK(t *testing.T) {
	cfg := testCfg()
	cfg.Diagnostics.Inverters = InverterDiagnostics{
		DeviceAddresses: []int{12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23},
		PollInterval:    30 * time.Second,
	}
	// Night: PV 0, all twelve inverters in standby, PCS alive, alarm
	// words polled and zero.
	tick := testTick(map[string]float64{
		"active_pv_power_kw": 0, "load_power_kw": 80, "soc_percent": 60,
		"active_ess_power_kw": 0, "pcs_shutdown": 0, "pcs_in_operation": 1,
		"sl_alarm_1": 0, "sl_alarm_2": 0, "sl_alarm_3": 0,
		"sl_alarm_4": 0, "sl_alarm_5": 0, "sl_alarm_6": 0,
	}, QualityOK)
	s := healthTestService(cfg, &tick, nil)
	s.lastInverters.Store(standbyFleet(12, testTS))
	h := s.buildHealth(testTS)

	if len(h.Inverters) != 12 {
		t.Fatalf("inverters length = %d, want 12", len(h.Inverters))
	}
	c := findCheck(t, h, "inverters")
	if c == nil || c.Severity != CheckOK {
		t.Fatalf("inverters check = %+v — нічний standby не аварія (§10.6)", c)
	}
	if !h.OK {
		var bad []string
		for _, c := range h.Checks {
			if !c.OK {
				bad = append(bad, c.ID+":"+c.Severity)
			}
		}
		t.Fatalf("root ok = false at night standby; failing checks: %v", bad)
	}
	if h.Alarms == nil || len(h.Alarms.Words) != 6 || h.Alarms.Words[0] != "0x0" {
		t.Fatalf("alarms = %+v, want six 0x0 words", h.Alarms)
	}
}

func TestHealthFleetFaultAndUnreachableAlarm(t *testing.T) {
	cfg := testCfg()
	cfg.Diagnostics.Inverters = InverterDiagnostics{
		DeviceAddresses: []int{12, 13, 14}, PollInterval: 30 * time.Second,
	}
	tick := testTick(map[string]float64{
		"active_pv_power_kw": 200, "load_power_kw": 100, "soc_percent": 60,
		"pcs_shutdown": 0, "pcs_in_operation": 1,
	}, QualityOK)
	s := healthTestService(cfg, &tick, nil)
	errMsg := "dial tcp: timeout"
	s.lastInverters.Store(&inverterFleet{TS: testTS, Inverters: []InverterSnapshot{
		{DeviceAddress: 12, Class: InvOnGrid, PollOK: true, TS: testTS},
		{DeviceAddress: 13, Class: InvFault, PollOK: true, TS: testTS},
		{DeviceAddress: 14, Class: InvUnreachable, PollError: &errMsg, TS: testTS},
	}})
	h := s.buildHealth(testTS)

	// Length stays = configured addresses even with an unreachable row.
	if len(h.Inverters) != 3 {
		t.Fatalf("inverters length = %d, want 3", len(h.Inverters))
	}
	c := findCheck(t, h, "inverters")
	if c == nil || c.Severity != CheckAlarm {
		t.Fatalf("inverters check = %+v, want alarm on fault/unreachable", c)
	}
	if h.OK {
		t.Fatal("root ok must fail on fleet fault")
	}
}

func TestHealthEmptyAddressesHideInverters(t *testing.T) {
	tick := testTick(map[string]float64{
		"active_pv_power_kw": 100, "load_power_kw": 100, "soc_percent": 60,
	}, QualityOK)
	s := healthTestService(testCfg(), &tick, nil)
	h := s.buildHealth(testTS)

	if h.Inverters != nil {
		t.Fatalf("inverters = %v, want absent when poll disabled", h.Inverters)
	}
	if c := findCheck(t, h, "inverters"); c != nil {
		t.Fatalf("inverters check present without addresses: %+v", c)
	}
	raw, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	var asMap map[string]any
	_ = json.Unmarshal(raw, &asMap)
	if _, ok := asMap["inverters"]; ok {
		t.Fatalf("snapshot JSON must omit the inverters key: %s", raw)
	}
}

func TestHealthSLAlarmWords(t *testing.T) {
	tick := testTick(map[string]float64{
		"active_pv_power_kw": 100, "load_power_kw": 100, "soc_percent": 60,
		"sl_alarm_1": 0, "sl_alarm_2": 0, "sl_alarm_3": 16,
		"sl_alarm_4": 0, "sl_alarm_5": 0, "sl_alarm_6": 0,
	}, QualityOK)
	s := healthTestService(testCfg(), &tick, nil)
	h := s.buildHealth(testTS)

	if h.Alarms == nil || h.Alarms.Words[2] != "0x0010" {
		t.Fatalf("alarms = %+v, want word 3 = 0x0010", h.Alarms)
	}
	c := findCheck(t, h, "sl_alarms")
	if c == nil || c.Severity != CheckAlarm {
		t.Fatalf("sl_alarms check = %+v, want alarm", c)
	}
	if h.OK {
		t.Fatal("root ok must fail on an SL alarm word")
	}
}

func TestHealthPlanVsShadowWarnsWithClamps(t *testing.T) {
	tick := testTick(map[string]float64{
		"active_pv_power_kw": 0, "load_power_kw": 100, "soc_percent": 60,
	}, QualityOK)
	plan := 200.0
	dec := &Decision{
		TS: testTS, PBessPlanKw: &plan, PBessVirtualKw: 100,
		Clamps: []string{"без експорту: розряд обрізано до дефіциту"},
	}
	s := healthTestService(testCfg(), &tick, dec)
	h := s.buildHealth(testTS)
	c := findCheck(t, h, "plan_vs_shadow")
	if c == nil || c.Severity != CheckWarning || c.Detail == "" {
		t.Fatalf("plan_vs_shadow = %+v, want warning with clamp detail", c)
	}
	// shadow_vs_fact stays info regardless (керує Encombi).
	if c := findCheck(t, h, "shadow_vs_fact"); c != nil && c.Severity != CheckInfo {
		t.Fatalf("shadow_vs_fact = %+v, want info", c)
	}
}

func TestLocalUIStatusIncludesHealth(t *testing.T) {
	s := localUITestService(t)
	rec := httptest.NewRecorder()
	s.uiStatus(rec, httptest.NewRequest("GET", "/api/status", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Health *HealthSnapshot `json:"health"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Health == nil || len(body.Health.Checks) == 0 {
		t.Fatalf("health missing from /api/status: %s", rec.Body.String())
	}
}

func TestHeartbeatCarriesHealth(t *testing.T) {
	// The Heartbeat document must serialize the snapshot under `health`
	// (cloud stores it verbatim next to the heartbeat row).
	s := healthTestService(testCfg(), nil, nil)
	raw, err := json.Marshal(s.buildHealth(testTS))
	if err != nil {
		t.Fatal(err)
	}
	hb := Heartbeat{SiteID: "ab", EdgeID: "iot2050-test", Status: "online", Health: raw}
	out, err := json.Marshal(hb)
	if err != nil {
		t.Fatal(err)
	}
	var back struct {
		Health *HealthSnapshot `json:"health"`
	}
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	if back.Health == nil || len(back.Health.Checks) == 0 {
		t.Fatalf("heartbeat JSON lost the health snapshot: %s", out)
	}
}
