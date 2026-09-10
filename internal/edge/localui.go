package edge

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Local on-device console (spec ems_ui_edge_vs_cloud §2, mockup
// iot2050_console.html): status, diagnostics, manifest view and the
// emergency override. Serves on the site LAN without auth — the
// IOT2050 sits on the OT network; the full planner lives in the cloud.

//go:embed localui.html
var localUIPage []byte

func (s *Service) runLocalUI(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(localUIPage)
	})
	mux.HandleFunc("/api/status", s.uiStatus)
	mux.HandleFunc("/api/decisions", s.uiDecisions)
	mux.HandleFunc("/api/events", s.uiEvents)
	mux.HandleFunc("/api/manifest", s.uiManifest)
	mux.HandleFunc("/api/override", s.uiOverride)

	srv := &http.Server{
		Addr:              s.cfg.LocalUI.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shCtx)
	}()
	s.log.Info("edge_local_ui", "listen", s.cfg.LocalUI.Listen)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		s.log.Error("edge_local_ui", "err", err)
	}
}

func uiJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// uiStatus is the console's single polling endpoint: everything the
// «Стан» panel shows in one document.
func (s *Service) uiStatus(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	out := map[string]any{
		"site_id":  s.cfg.SiteID,
		"edge_id":  s.cfg.Edge.EdgeID,
		"version":  s.version,
		"mode":     string(s.cfg.Control.Mode),
		"preset":   s.cfg.Control.Preset,
		"topology": string(s.cfg.SmartLogger.Topology),
		"now":      now,
		"uptime_s": int64(time.Since(s.startedAt).Seconds()),
		"timezone": s.cfg.Timezone,
		"limits": map[string]any{
			"grid_import_kw":    s.cfg.Limits.Grid.ImportLimitKw,
			"grid_target_kw":    s.cfg.Limits.Grid.TargetImportKw,
			"pv_rated_kw":       s.cfg.Limits.PV.RatedKw,
			"bess_power_kw":     s.cfg.Limits.Bess.RatedPowerKw,
			"bess_capacity_kwh": s.cfg.Limits.Bess.RatedCapacityKwh,
			"soc_min_pct":       s.cfg.Limits.Bess.SocMinEconomicPct,
			"soc_max_pct":       s.cfg.Limits.Bess.SocMaxEconomicPct,
		},
	}

	if t := s.lastTick.Load(); t != nil {
		out["tick"] = t
	}
	if d := s.lastDecision.Load(); d != nil {
		out["decision"] = d.Record(s.cfg.SiteID)
	}
	out["health"] = s.buildHealth(now)
	if len(s.cfg.Diagnostics.Inverters.DeviceAddresses) > 0 {
		out["inverter_poll_s"] = int(s.cfg.Diagnostics.Inverters.PollInterval.Seconds())
	}

	m := s.manifest.Load()
	if m != nil {
		mi := map[string]any{
			"manifest_id": m.ManifestID,
			"mode":        string(m.Mode),
			"preset":      m.Preset,
			"valid_from":  m.ValidFrom,
			"valid_until": m.ValidUntil,
			"expired":     !m.ActiveAt(now),
			"intervals":   0,
		}
		if m.Plan != nil {
			mi["intervals"] = len(m.Plan.Intervals)
			mi["load_source"] = m.Plan.LoadSource
		}
		out["manifest"] = mi
	}

	devices := []map[string]any{}
	for _, d := range s.cfg.SmartLogger.Devices {
		dev := map[string]any{"role": string(d.Role), "host": d.Host, "port": d.Port}
		if v, ok := s.devPollOK.Load(d.Host); ok {
			last := time.Unix(v.(int64), 0).UTC()
			dev["last_ok"] = last
			dev["ok"] = now.Sub(last) < 3*s.cfg.SmartLogger.PollInterval+2*time.Second
		} else {
			dev["ok"] = false
		}
		devices = append(devices, dev)
	}
	out["devices"] = devices

	uplink := map[string]any{"enabled": s.cfg.Uplink.Enabled}
	if unix := s.lastUplinkOK.Load(); unix > 0 {
		uplink["last_ok"] = time.Unix(unix, 0).UTC()
	}
	out["uplink"] = uplink

	if pending, size, err := s.bb.Stats(r.Context()); err == nil {
		out["blackbox"] = map[string]any{
			"path":       s.bb.Path(),
			"size_bytes": size,
			"pending":    pending,
		}
	}

	if ov := s.override.Load(); ov.activeAt(now) {
		out["override"] = ov
	}
	uiJSON(w, out)
}

func (s *Service) uiDecisions(w http.ResponseWriter, r *http.Request) {
	rows, err := s.bb.RecentDecisions(r.Context(), uiLimit(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	uiJSON(w, map[string]any{"decisions": rows})
}

func (s *Service) uiEvents(w http.ResponseWriter, r *http.Request) {
	rows, err := s.bb.RecentEvents(r.Context(), uiLimit(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	uiJSON(w, map[string]any{"events": rows})
}

// uiManifest serves the raw cached manifest JSON (the «повний JSON
// (технічний)» block in the mockup).
func (s *Service) uiManifest(w http.ResponseWriter, r *http.Request) {
	raw, err := os.ReadFile(s.cfg.Manifest.CachePath)
	if err != nil {
		if m := s.manifest.Load(); m != nil {
			uiJSON(w, m)
			return
		}
		http.Error(w, "no manifest cached yet", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

// uiOverride sets or clears the local emergency override.
// POST {"mode":"fallback_safe"|"monitor"|"auto","ttl_minutes":60}
func (s *Service) uiOverride(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Mode       string `json:"mode"`
		TTLMinutes int    `json:"ttl_minutes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	now := time.Now().UTC()
	switch strings.TrimSpace(body.Mode) {
	case "auto", "":
		s.override.Store(nil)
		s.pushEvent(Event{
			TS: now, Severity: SevInfo, Code: EvOverrideCleared,
			Message: "local override cleared from the console — back to manifest",
		})
		uiJSON(w, map[string]any{"override": nil})
	case "fallback_safe", "monitor":
		ttl := body.TTLMinutes
		if ttl <= 0 {
			ttl = 60
		}
		if ttl > 24*60 {
			ttl = 24 * 60
		}
		ov := &overrideState{Mode: body.Mode, Until: now.Add(time.Duration(ttl) * time.Minute)}
		s.override.Store(ov)
		s.pushEvent(Event{
			TS: now, Severity: SevWarning, Code: EvOverrideSet,
			Message: "local override set from the console: " + ov.Mode,
			Context: map[string]any{"mode": ov.Mode, "until": ov.Until},
		})
		uiJSON(w, map[string]any{"override": ov})
	default:
		http.Error(w, "mode must be auto, fallback_safe or monitor", http.StatusBadRequest)
	}
}

// pushEvent hands an event to the core loop without blocking the HTTP
// goroutine (the channel is buffered; a full buffer just drops the
// console event — the override itself is already in effect).
func (s *Service) pushEvent(ev Event) {
	if s.events == nil {
		return
	}
	select {
	case s.events <- ev:
	default:
	}
}

func uiLimit(r *http.Request) int {
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return 50
}
