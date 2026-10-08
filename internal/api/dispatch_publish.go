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
	doc, run := buildDispatchManifest(env, m, res, ver.Version)

	out := EdgePublishResult{
		SiteID: siteID, ManifestID: doc.ManifestID, Source: doc.Source,
		ValidUntil: doc.ValidUntil.Format(time.RFC3339), LoadSource: "operator",
	}
	if doc.Plan != nil {
		out.Intervals = len(doc.Plan.Intervals)
	} else {
		out.LoadSource = "none"
	}
	_, latestID, hasLatest, err := storage.LatestEdgeManifest(ctx, e.Pool, siteID)
	if err != nil {
		return out, err
	}
	if !hasLatest || latestID != doc.ManifestID {
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

// buildDispatchManifest turns a simulated plan into manifest-lite (the
// edge follows plan.intervals[].ess_kw) and the run record. Pure.
func buildDispatchManifest(env *dispatchEnv, m dispatch.Model, res dispatch.Result, version int) (edgeManifestDoc, []dispatchRunHour) {
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
	doc.SocPolicy.MinEconomicPct = cfg.ReservePct
	doc.SocPolicy.MaxEconomicPct = env.site.SocMaxPct

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
