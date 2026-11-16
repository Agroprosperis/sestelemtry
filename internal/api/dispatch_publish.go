package api

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nesh/sestelemetry/internal/dispatch"
	"github.com/nesh/sestelemetry/internal/storage"
)

const (
	// An unchanged plan is republished only when its horizon date rolls
	// over, the same instant a manifest without grace would expire; the
	// grace must outlast one planner-loop interval plus the edge poll.
	dispatchManifestGraceHours = 1
)

// dispatchRunHour is one planned hour as stored with a publication.
type dispatchRunHour struct {
	TS        time.Time         `json:"ts"`
	LoadKw    float64           `json:"load_kw"`
	PvKw      float64           `json:"pv_kw"`
	EssKw     float64           `json:"ess_kw"`
	SocPct    float64           `json:"soc_pct"`
	ImportKw  float64           `json:"import_kw"`
	ExportKw  float64           `json:"export_kw"`
	Curtailed float64           `json:"curtailed_kw"`
	Command   *dispatch.Command `json:"command,omitempty"`
}

// publishDispatch republishes a site's plan: the latest confirmed
// version from the current hour, priced by the LP over the known
// horizon. Without known load the plan is empty and the edge runs
// self-consumption (no_plan_*).
func (h *Handlers) publishDispatch(ctx context.Context, siteID string) (EdgePublishResult, error) {
	e := h.edge
	env, err := h.loadDispatchEnv(ctx, siteID)
	if err != nil {
		return EdgePublishResult{SiteID: siteID}, err
	}
	m, ver, err := h.appliedModel(ctx, env)
	if err != nil {
		return EdgePublishResult{SiteID: siteID}, err
	}
	res, err := dispatch.Simulate(env.inputs, m)
	if err != nil {
		return EdgePublishResult{SiteID: siteID}, err
	}
	doc, run := buildDispatchManifest(env, m, res, ver)

	out := EdgePublishResult{
		SiteID: siteID, ManifestID: doc.ManifestID, Source: doc.Source,
		ValidUntil: doc.ValidUntil.Format(time.RFC3339), LoadSource: "operator",
	}
	if doc.Plan != nil {
		out.Intervals = len(doc.Plan.Intervals)
	} else {
		out.LoadSource = "none"
	}
	latestPayload, latestID, hasLatest, err := storage.LatestEdgeManifest(ctx, e.Pool, siteID)
	if err != nil {
		return out, err
	}
	if !hasLatest || latestID != doc.ManifestID {
		doc.ManifestSeq = nextManifestSeq(latestPayload, time.Now())
		payload, err := json.Marshal(doc)
		if err != nil {
			return out, err
		}
		if err := storage.UpsertEdgeManifest(ctx, e.Pool, siteID, doc.ManifestID, payload, doc.ValidFrom, doc.ValidUntil); err != nil {
			return out, err
		}
		out.Published = true
		e.Log.Info("dispatch_manifest_published", "site_id", siteID, "manifest_id", doc.ManifestID,
			"version", ver.Version, "intervals", out.Intervals)
	}
	hours, err := json.Marshal(run)
	if err != nil {
		return out, err
	}
	if err := storage.InsertDispatchRun(ctx, e.Pool, storage.DispatchRun{
		SiteID: siteID, StartHour: env.start, ManifestID: doc.ManifestID, Version: ver.Version, Hours: hours,
	}); err != nil {
		e.Log.Warn("dispatch_run_store", "site_id", siteID, "err", err)
	}
	return out, nil
}

// nextManifestSeq numbers a newly stored manifest: past the previous
// one's, and never behind the clock.
func nextManifestSeq(prevPayload []byte, now time.Time) int64 {
	seq := now.UnixMilli()
	var prev struct {
		ManifestSeq int64 `json:"manifest_seq"`
	}
	if len(prevPayload) > 0 && json.Unmarshal(prevPayload, &prev) == nil && prev.ManifestSeq >= seq {
		seq = prev.ManifestSeq + 1
	}
	return seq
}

// manualBlocks turns the model's hourly commands back into the
// operator's blocks: consecutive hours of one block (same id, else same
// intent) merge. AUTO hours carry nothing.
func manualBlocks(m dispatch.Model, start time.Time, ver storage.DispatchVersion) []edgeManualCommand {
	var out []edgeManualCommand
	var srcIDs []string // the desk's own block id of each entry of out
	same := func(i int, c *dispatch.Command) bool {
		if srcIDs[i] != "" || c.ID != "" {
			return srcIDs[i] == c.ID
		}
		b := out[i]
		return b.Type == string(c.Type) && b.Value == c.Value && b.Direction == c.Direction
	}
	for i, c := range m.Commands {
		if c == nil || c.Type == dispatch.CmdAuto || c.Type == "" {
			continue
		}
		from := start.Add(time.Duration(i) * time.Hour)
		until := from.Add(time.Hour)
		if n := len(out); n > 0 && out[n-1].Until.Equal(from) && same(n-1, c) {
			out[n-1].Until = until
			if c.Value > 0 {
				// A target block's goal sits on its last hour.
				out[n-1].Value = c.Value
			}
			continue
		}
		id := c.ID
		if id == "" {
			id = fmt.Sprintf("v%d-%s", ver.Version, from.UTC().Format("200601021504"))
		}
		out = append(out, edgeManualCommand{
			ID: id, Type: string(c.Type), Value: c.Value, Direction: c.Direction,
			From: from, Until: until, ConfirmedBy: ver.ConfirmedBy, ConfirmedAt: ver.ConfirmedAt,
		})
		srcIDs = append(srcIDs, c.ID)
	}
	return out
}

// pccExportCeiling is the edge's hard PCC export ceiling: the site's
// own from «Налаштування» (export regime × contract, capped by the
// technical PCC limit), lowered to the desk cap when the operator set
// one. Blocking export or clearing the desk cap stops battery sales
// (export_allowed) but does not lift the ceiling for PV. 0 = the site
// has no regime yet: the edge then refuses to turn writing on.
func pccExportCeiling(env *dispatchEnv, cfg dispatch.Constraints) float64 {
	if env.exportCeiling == nil {
		return 0
	}
	c := *env.exportCeiling
	if op := cfg.EffectiveExportCap(); op > 0 && op < c {
		c = op
	}
	return c
}

// buildDispatchManifest turns a simulated plan into manifest-lite (the
// edge follows plan.intervals[].ess_kw, a manual block first) and the
// run record. Pure.
func buildDispatchManifest(env *dispatchEnv, m dispatch.Model, res dispatch.Result, ver storage.DispatchVersion) (edgeManifestDoc, []dispatchRunHour) {
	version := ver.Version
	cfg := m.Cfg
	doc := edgeManifestDoc{
		SchemaVersion: "lite-1",
		SiteID:        env.siteID,
		IssuedAt:      env.now,
		ValidFrom:     env.now,
		ValidUntil:    env.start.Add((dispatchFutureHours + dispatchManifestGraceHours) * time.Hour),
		Mode:          "shadow",
		WriteEnabled:  false,
		Preset:        "economic_arbitrage",
		ExportAllowed: cfg.EssSale && !cfg.BlockExport && cfg.EffectiveExportCap() > 0,
		Source:        "dispatch",
		Note:          fmt.Sprintf("dispatch v%d", version),
	}
	doc.Limits.EssChargeMaxKw = env.site.ChargeKw
	doc.Limits.EssDischargeMaxKw = env.site.DischargeKw
	// import_limit_kw is the contract the edge health check uses;
	// target_import_kw is the charge clamp: the tighter of the
	// «Обмеження» target and the desk horizon cap.
	if env.importSet {
		doc.GridLimits.ImportLimitKw = env.site.ImportKw
		target := cfg.ImportCapKw
		if t := env.params.GridTargetKw; t > 0 && t < target {
			target = t
		}
		doc.GridLimits.TargetImportKw = target
	} else {
		doc.GridLimits.TargetImportKw = env.params.GridTargetKw
	}
	doc.GridLimits.PvRatedKw = env.params.PvRatedKw
	doc.GridLimits.ExportCapKw = pccExportCeiling(env, cfg)
	doc.SocPolicy.MinEconomicPct = cfg.ReservePct
	doc.SocPolicy.MaxEconomicPct = env.site.SocMaxPct
	doc.ManualCommands = manualBlocks(m, env.start, ver)

	var intervals []edgePlanInterval
	run := []dispatchRunHour{}
	for i := 0; i < res.KnownHours; i++ {
		hr := res.Hours[i]
		ts := env.start.Add(time.Duration(i) * time.Hour)
		load, pv := *m.Loads[i], env.inputs.PV[i]
		iv := edgePlanInterval{
			TS:           ts,
			EssKw:        round1(*hr.P),
			SocTargetPct: round1(*hr.Soc),
			Action:       dispatchAction(*hr.P, *hr.Export, load, pv),
		}
		if env.inputs.Rdn[i] != nil {
			iv.PriceUah = round3(*env.inputs.Rdn[i])
		}
		intervals = append(intervals, iv)
		run = append(run, dispatchRunHour{
			TS: ts, LoadKw: round1(load), PvKw: round1(pv), EssKw: round1(*hr.P), SocPct: round1(*hr.Soc),
			ImportKw: round1(*hr.Import), ExportKw: round1(*hr.Export), Curtailed: round1(*hr.Curtailed),
			Command: m.Commands[i],
		})
	}
	if len(intervals) > 0 {
		doc.Plan = &edgePlanDoc{Granularity: "1h", LoadSource: "operator", Intervals: intervals}
	}
	doc.ManifestID = edgeManifestID(env.siteID, doc.ValidUntil, doc)
	return doc, run
}

// dispatchAction labels a planned hour for the edge console.
func dispatchAction(p, exportKw, load, pv float64) string {
	switch {
	case p > 0.5:
		for _, part := range dispatch.SplitFlow(p, pv, load) {
			if part.Key == "export" && part.Power > 0.5 && exportKw > 0.05 {
				return "export"
			}
		}
		return "discharge"
	case p < -0.5:
		return "charge"
	default:
		return "hold"
	}
}
