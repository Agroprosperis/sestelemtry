package edge

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Local on-device console (spec ems_ui_edge_vs_cloud §2, mockup
// iot2050_console.html): status, diagnostics, manifest view and the
// emergency override. Serves on the site LAN behind the device login
// (consoleauth.go); the full planner lives in the cloud.

//go:embed localui.html
var localUIPage []byte

func (s *Service) runLocalUI(ctx context.Context) {
	srv := &http.Server{
		Addr:              s.cfg.LocalUI.Listen,
		Handler:           s.localUIHandler(),
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

func (s *Service) localUIHandler() http.Handler {
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
	mux.HandleFunc("/api/write", s.uiWrite)
	mux.HandleFunc("/api/command", s.uiCommand)
	return s.withConsoleAuth(mux)
}

func uiJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// uiStatus is the console's single polling endpoint: everything the
// «Стан» panel shows in one document.
func (s *Service) uiStatus(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	// The console shows the limits the engine actually applies (§4.1):
	// the device YAML is only the pre-manifest fallback, never the live
	// figure.
	params := resolveParams(now, s.manifest.Load(), s.cfg)
	out := map[string]any{
		"site_id":       s.cfg.SiteID,
		"edge_id":       s.cfg.Edge.EdgeID,
		"version":       s.version,
		"mode":          string(s.cfg.Control.Mode),
		"preset":        s.cfg.Control.Preset,
		"topology":      string(s.cfg.SmartLogger.Topology),
		"now":           now,
		"uptime_s":      int64(time.Since(s.startedAt).Seconds()),
		"timezone":      s.cfg.Timezone,
		"write_enabled": s.gate.Enabled(),
		"write":         s.writeHealth(),
		"limits": map[string]any{
			"source":                params.planSource,
			"grid_import_kw":        s.effectiveImportLimitKw(now),
			"grid_target_kw":        params.targetImportKw,
			"pv_rated_kw":           params.pvRatedKw,
			"bess_charge_max_kw":    params.chargeMaxKw,
			"bess_discharge_max_kw": params.dischargeMaxKw,
			"soc_min_pct":           params.socMinPct,
			"soc_max_pct":           params.socMaxPct,
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

// uiWrite is the write flag on the console.
//
//	GET  → state, preconditions
//	POST {"action":"enable","confirm":"<site_id>"} — turn writing on
//	     {"action":"stop"}    — battery 0 now, keep writing safe values
//	     {"action":"disable"} — battery 0 and PV at its rating, read back, off
func (s *Service) uiWrite(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		uiJSON(w, map[string]any{
			"write":         s.writeHealth(),
			"preconditions": s.writePreconditions(time.Now().UTC()),
		})
	case http.MethodPost:
		var body struct {
			Action  string `json:"action"`
			Confirm string `json:"confirm"`
			Pin     string `json:"pin"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		action := strings.TrimSpace(body.Action)
		by := consoleBy(r)
		pinOK := false
		if action == "enable" {
			now := time.Now().UTC()
			locked, err := s.pin.check(body.Pin, now)
			if err != nil {
				if errors.Is(err, errPinWrong) {
					sev := SevWarning
					if locked {
						sev = SevAlarm
					}
					s.pushEvent(Event{TS: now, Severity: sev, Code: EvWriteDenied,
						Message: "увімкнення запису відхилено: " + err.Error() + " (" + by + ")"})
				}
				uiWriteConflict(w, writeReply{Error: err.Error()})
				return
			}
			pinOK = true
		}
		reply := s.requestWrite(r.Context(), action, by, body.Confirm, pinOK)
		if !reply.OK {
			uiWriteConflict(w, reply)
			return
		}
		uiJSON(w, reply)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// uiCommand sends one start/stop button (POST {"command": "ess_start"}).
// Only with writing on; the answer and the confirmation come through
// GET /api/write.
func (s *Service) uiCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Command string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	reply := s.requestCommand(r.Context(), strings.TrimSpace(body.Command), consoleBy(r))
	if !reply.OK {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(reply)
		return
	}
	uiJSON(w, reply)
}

func uiWriteConflict(w http.ResponseWriter, reply writeReply) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(reply)
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
