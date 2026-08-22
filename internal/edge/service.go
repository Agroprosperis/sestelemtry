package edge

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nesh/sestelemetry/internal/registers"
)

const EvManifestRejected = "MANIFEST_REJECTED"

// Local-console override events.
const (
	EvOverrideSet     = "OVERRIDE_SET"
	EvOverrideCleared = "OVERRIDE_CLEARED"
)

// uplinkBacklogThreshold triggers the UPLINK_BACKLOG event (spec §8).
const uplinkBacklogThreshold = 10000

// Service wires the edge modules together: pollers → normalizer →
// black box → shadow engine, plus the uplink/heartbeat/manifest side
// loops. The core loop is single-threaded: readings, events and ticks
// are serialized through one select, so engine state and event
// deduplication need no locking.
type Service struct {
	cfg     *Config
	log     *slog.Logger
	version string

	bb     *Blackbox
	norm   *Normalizer
	client *UplinkClient

	manifest   atomic.Pointer[Manifest]
	lastPollOK atomic.Int64 // unix seconds of the last successful poll

	// Local-console state (read by the HTTP goroutine, written by the
	// core loop / uplink — hence atomics and sync.Map).
	startedAt    time.Time
	lastTick     atomic.Pointer[Tick]
	lastDecision atomic.Pointer[Decision]
	lastUplinkOK atomic.Int64 // unix seconds of the last accepted batch
	devPollOK    sync.Map     // host → unix seconds of its last reading
	override     atomic.Pointer[overrideState]
	events       chan Event // core-loop event channel (console sends too)

	// Core-loop state (no locks: touched only from the core loop).
	lastManifestID   string
	manifestExpired  bool
	eventLastWritten map[string]time.Time
}

// overrideState is the local emergency override (spec ems_ui_edge_vs
// _cloud §2: локальний UI = стан + логи + emergency override). While
// active the shadow engine ignores the manifest: "fallback_safe" runs
// the self_consumption_safe preset, "monitor" suspends decisions
// entirely. Expires automatically at Until.
type overrideState struct {
	Mode  string    `json:"mode"` // fallback_safe | monitor
	Until time.Time `json:"until"`
}

func (o *overrideState) activeAt(now time.Time) bool {
	return o != nil && o.Mode != "" && now.Before(o.Until)
}

// Run starts the edge service and blocks until ctx is cancelled.
func Run(ctx context.Context, cfg *Config, log *slog.Logger, version string) error {
	entriesByDevice, err := resolveDeviceEntries(cfg)
	if err != nil {
		return err
	}

	bb, err := OpenBlackbox(cfg.Blackbox, cfg.SiteID)
	if err != nil {
		return err
	}
	defer bb.Close()

	s := &Service{
		cfg:              cfg,
		log:              log,
		version:          version,
		bb:               bb,
		norm:             NewNormalizer(cfg),
		startedAt:        time.Now(),
		eventLastWritten: map[string]time.Time{},
	}
	if cfg.Uplink.Enabled {
		s.client = NewUplinkClient(cfg.Uplink)
	}

	// A cached manifest survives reboots and cloud outages: the engine
	// keeps following it until valid_until, then falls back.
	if m, err := loadManifestCache(cfg.Manifest.CachePath, cfg.SiteID); err != nil {
		log.Warn("edge_manifest_cache", "err", err)
	} else if m != nil {
		s.manifest.Store(m)
		log.Info("edge_manifest_cache_loaded", "manifest_id", m.ManifestID, "valid_until", m.ValidUntil)
	}

	readings := make(chan reading, 16)
	events := make(chan Event, 128)
	s.events = events

	var wg sync.WaitGroup
	for _, dev := range cfg.SmartLogger.Devices {
		dev := dev
		wg.Add(1)
		go func() {
			defer wg.Done()
			runDevicePoller(ctx, log, dev, cfg.SmartLogger.PollInterval, entriesByDevice[dev.Role], readings, events)
		}()
	}
	if s.client != nil {
		wg.Add(1)
		go func() { defer wg.Done(); s.runUplink(ctx, events) }()
		wg.Add(1)
		go func() { defer wg.Done(); s.runHeartbeat(ctx) }()
		wg.Add(1)
		go func() { defer wg.Done(); s.runManifestFetcher(ctx, events) }()
	}
	wg.Add(1)
	go func() { defer wg.Done(); s.runMaintenance(ctx) }()
	if cfg.LocalUI.On() {
		wg.Add(1)
		go func() { defer wg.Done(); s.runLocalUI(ctx) }()
	}

	log.Info("edge_start",
		"site_id", cfg.SiteID, "edge_id", cfg.Edge.EdgeID,
		"topology", string(cfg.SmartLogger.Topology),
		"mode", string(cfg.Control.Mode), "preset", cfg.Control.Preset,
		"uplink", cfg.Uplink.Enabled, "version", version)

	t := time.NewTicker(cfg.Control.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			log.Info("edge_stop")
			return nil
		case r := <-readings:
			s.norm.Observe(r)
			s.lastPollOK.Store(r.at.Unix())
			s.devPollOK.Store(r.host, r.at.Unix())
		case ev := <-events:
			s.writeEventDeduped(ctx, ev)
		case now := <-t.C:
			s.onTick(ctx, now.UTC())
		}
	}
}

// resolveDeviceEntries loads the register catalog and computes each
// device's whitelist (explicit metric_keys or the role default).
func resolveDeviceEntries(cfg *Config) (map[DeviceRole][]registers.ResolvedEntry, error) {
	cat, err := registers.Load(cfg.RegisterCatalog)
	if err != nil {
		return nil, fmt.Errorf("edge: register catalog: %w", err)
	}
	resolved, err := cat.Resolve(0)
	if err != nil {
		return nil, err
	}
	out := map[DeviceRole][]registers.ResolvedEntry{}
	for _, dev := range cfg.SmartLogger.Devices {
		keys := dev.MetricKeys
		if len(keys) == 0 {
			keys = DefaultMetricKeys(dev.Role)
		}
		entries, err := registers.Subset(resolved, keys)
		if err != nil {
			return nil, fmt.Errorf("edge: device %s (%s): %w", dev.Host, dev.Role, err)
		}
		out[dev.Role] = entries
	}
	return out, nil
}

// onTick is the 1 s heart of the edge: normalize → black box → shadow.
func (s *Service) onTick(ctx context.Context, now time.Time) {
	tick := s.norm.BuildTick(now)
	s.lastTick.Store(&tick)
	if err := s.bb.WriteTick(ctx, tick); err != nil {
		s.log.Error("edge_blackbox_tick", "err", err)
	}

	cur := s.manifest.Load()
	if cur != nil && cur.ManifestID != s.lastManifestID {
		s.lastManifestID = cur.ManifestID
		s.manifestExpired = false
		s.writeEventDeduped(ctx, Event{
			TS: now, Severity: SevInfo, Code: EvManifestApplied,
			Message: "manifest applied: " + cur.ManifestID,
			Context: map[string]any{"manifest_id": cur.ManifestID, "valid_until": cur.ValidUntil},
		})
	}
	if cur != nil && !cur.ActiveAt(now) && !s.manifestExpired {
		s.manifestExpired = true
		s.writeEventDeduped(ctx, Event{
			TS: now, Severity: SevWarning, Code: EvManifestExpired,
			Message: fmt.Sprintf("manifest %s expired — falling back to %s", cur.ManifestID, FallbackPreset),
			Context: map[string]any{"manifest_id": cur.ManifestID},
		})
	}

	// Local emergency override (set from the on-device console). It
	// expires on its own; the transition back to the manifest is logged
	// once.
	ov := s.override.Load()
	if ov != nil && !ov.activeAt(now) {
		s.override.Store(nil)
		ov = nil
		s.writeEventDeduped(ctx, Event{
			TS: now, Severity: SevInfo, Code: EvOverrideCleared,
			Message: "local override expired — back to manifest",
		})
	}

	if s.cfg.Control.Mode != ModeShadow {
		return
	}
	if ov.activeAt(now) && ov.Mode == "monitor" {
		return // monitor override: telemetry only, no decisions
	}
	effective := cur
	if ov.activeAt(now) && ov.Mode == "fallback_safe" {
		// A synthetic expired manifest routes resolveParams onto the
		// standard fallback path: self_consumption_safe with site
		// limits intact — exactly the emergency behaviour.
		effective = &Manifest{
			ManifestID: "local-override",
			SiteID:     s.cfg.SiteID,
			Mode:       ModeShadow,
			ValidUntil: now.Add(-time.Second),
		}
	}
	decision, evs := Decide(tick, effective, s.cfg)
	if ov.activeAt(now) {
		decision.PlanSource = "override"
	}
	s.lastDecision.Store(&decision)
	if err := s.bb.WriteDecision(ctx, decision); err != nil {
		s.log.Error("edge_blackbox_decision", "err", err)
	}
	for _, ev := range evs {
		s.writeEventDeduped(ctx, ev)
	}
}

// writeEventDeduped drops repeats of the same (code, message) within
// 5 minutes so a persistent condition (e.g. SOC at the floor for an
// hour) yields one event, not 3600.
func (s *Service) writeEventDeduped(ctx context.Context, ev Event) {
	key := ev.Code + "|" + ev.Message
	if last, ok := s.eventLastWritten[key]; ok && ev.TS.Sub(last) < 5*time.Minute {
		return
	}
	s.eventLastWritten[key] = ev.TS
	if err := s.bb.WriteEvent(ctx, ev); err != nil {
		s.log.Error("edge_blackbox_event", "err", err)
	}
	s.log.Info("edge_event", "code", ev.Code, "severity", ev.Severity, "msg", ev.Message)
}

// runUplink ships pending black-box rows to the cloud on the batch
// interval, honouring the spec priority events → control_decisions →
// telemetry and the [10,30,60,300]s retry ladder.
func (s *Service) runUplink(ctx context.Context, events chan<- Event) {
	t := time.NewTicker(s.cfg.Uplink.BatchInterval)
	defer t.Stop()
	failures := 0
	var blockedUntil time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if time.Now().Before(blockedUntil) {
			continue
		}
		sentAny, err := s.sendOneBatch(ctx)
		if err != nil {
			failures++
			blockedUntil = time.Now().Add(backoffFor(failures))
			s.log.Warn("edge_uplink", "err", err, "failures", failures)
			if failures == pollFailThreshold {
				emitEvent(ctx, events, Event{
					TS: time.Now().UTC(), Severity: SevWarning, Code: EvUplinkOffline,
					Message: "uplink failing: " + err.Error(),
				})
			}
			continue
		}
		failures = 0
		blockedUntil = time.Time{}
		if !sentAny {
			continue
		}
		if pending, err := s.bb.PendingCount(ctx); err == nil && pending > uplinkBacklogThreshold {
			emitEvent(ctx, events, Event{
				TS: time.Now().UTC(), Severity: SevInfo, Code: EvUplinkBacklog,
				Message: fmt.Sprintf("uplink backlog: %d pending rows", pending),
			})
		}
	}
}

// sendOneBatch builds and sends a single batch. Returns whether
// anything was pending.
func (s *Service) sendOneBatch(ctx context.Context) (bool, error) {
	capacity := s.cfg.Uplink.BatchMaxRecords

	evs, err := s.bb.PendingEvents(ctx, capacity)
	if err != nil {
		return false, err
	}
	capacity -= len(evs.IDs)

	var decs Pending
	if capacity > 0 {
		if decs, err = s.bb.PendingDecisions(ctx, capacity); err != nil {
			return false, err
		}
		capacity -= len(decs.IDs)
	}

	var ticks Pending
	if capacity > 0 {
		if ticks, err = s.bb.PendingTicks(ctx, capacity); err != nil {
			return false, err
		}
	}

	total := len(evs.IDs) + len(decs.IDs) + len(ticks.IDs)
	if total == 0 {
		return false, nil
	}

	req := BatchRequest{
		BatchID:        newBatchID(),
		SiteID:         s.cfg.SiteID,
		EdgeID:         s.cfg.Edge.EdgeID,
		SentAt:         time.Now().UTC(),
		Records:        ticks.Docs,
		ControlRecords: decs.Docs,
		Events:         evs.Docs,
	}
	if _, err := s.client.SendBatch(ctx, req); err != nil {
		return true, err
	}

	if err := s.bb.MarkUploaded(ctx, "events", evs.IDs); err != nil {
		return true, err
	}
	if err := s.bb.MarkUploaded(ctx, "control_decisions", decs.IDs); err != nil {
		return true, err
	}
	if err := s.bb.MarkUploaded(ctx, "telemetry_raw", ticks.IDs); err != nil {
		return true, err
	}
	s.lastUplinkOK.Store(time.Now().Unix())
	s.log.Info("edge_uplink_ok", "events", len(evs.IDs), "decisions", len(decs.IDs), "ticks", len(ticks.IDs))
	return true, nil
}

func (s *Service) runHeartbeat(ctx context.Context) {
	t := time.NewTicker(s.cfg.Uplink.HeartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		pending, err := s.bb.PendingCount(ctx)
		if err != nil {
			s.log.Error("edge_heartbeat_pending", "err", err)
			continue
		}
		hb := Heartbeat{
			SiteID:          s.cfg.SiteID,
			EdgeID:          s.cfg.Edge.EdgeID,
			Status:          "online",
			BufferPending:   pending,
			FirmwareVersion: s.version,
		}
		if unix := s.lastPollOK.Load(); unix > 0 {
			ts := time.Unix(unix, 0).UTC()
			hb.LastSLPollOK = &ts
		}
		if err := s.client.SendHeartbeat(ctx, hb); err != nil {
			s.log.Warn("edge_heartbeat", "err", err)
		}
	}
}

// runManifestFetcher polls the cloud for a newer manifest, validates
// the MVP gate and atomically persists + swaps it on change.
func (s *Service) runManifestFetcher(ctx context.Context, events chan<- Event) {
	t := time.NewTicker(s.cfg.Manifest.PollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cur := s.manifest.Load()
		curID := ""
		if cur != nil {
			curID = cur.ManifestID
		}
		m, err := s.client.FetchManifest(ctx, s.cfg.SiteID, curID)
		if err != nil {
			s.log.Warn("edge_manifest_fetch", "err", err)
			continue
		}
		if m == nil || m.ManifestID == curID {
			continue
		}
		if err := m.ValidateForEdge(s.cfg.SiteID); err != nil {
			emitEvent(ctx, events, Event{
				TS: time.Now().UTC(), Severity: SevWarning, Code: EvManifestRejected,
				Message: err.Error(),
				Context: map[string]any{"manifest_id": m.ManifestID},
			})
			continue
		}
		if err := saveManifestCache(s.cfg.Manifest.CachePath, m); err != nil {
			s.log.Error("edge_manifest_cache_write", "err", err)
		}
		s.manifest.Store(m)
		s.log.Info("edge_manifest_new", "manifest_id", m.ManifestID, "valid_until", m.ValidUntil)
	}
}

func (s *Service) runMaintenance(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if err := s.bb.Maintain(ctx, now.UTC()); err != nil {
				s.log.Error("edge_blackbox_maintain", "err", err)
			}
		}
	}
}

func loadManifestCache(path, siteID string) (*Manifest, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("manifest cache corrupt: %w", err)
	}
	if err := m.ValidateForEdge(siteID); err != nil {
		return nil, err
	}
	return &m, nil
}

// saveManifestCache writes atomically (tmp + rename) so a power cut
// mid-write never leaves a truncated active manifest.
func saveManifestCache(path string, m *Manifest) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
