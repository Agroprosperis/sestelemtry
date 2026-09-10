package edge

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ReplayOptions configures a historical run of the shadow engine:
// telemetry rows in, control decisions out. No Modbus, no black box,
// no uplink — the exact same Decide() the live loop uses.
type ReplayOptions struct {
	InputCSV     string
	OutputCSV    string
	ManifestFile string // optional manifest-lite JSON with a plan
}

// registerSuffix strips trailing register addresses from exported
// column names (e.g. "soc_percent_40515" → "soc_percent") so both the
// prod DB pivot export and the ems-spec archive CSVs replay directly.
var registerSuffix = regexp.MustCompile(`_[0-9]{5}$`)

// replay column → catalog metric_key aliases beyond suffix stripping.
var replayAliases = map[string]string{
	"pv_power_kw":   "active_pv_power_kw",
	"ess_power_kw":  "active_ess_power_kw",
	"grid_power_kw": "grid_connected_active_power_kw",
}

// RunReplay streams the CSV through the normalizer + shadow engine and
// writes one decision row per input row.
func RunReplay(ctx context.Context, cfg *Config, log *slog.Logger, opts ReplayOptions) error {
	var manifest *Manifest
	if opts.ManifestFile != "" {
		raw, err := os.ReadFile(opts.ManifestFile)
		if err != nil {
			return fmt.Errorf("replay manifest: %w", err)
		}
		var m Manifest
		if err := json.Unmarshal(raw, &m); err != nil {
			return fmt.Errorf("replay manifest: %w", err)
		}
		if err := m.ValidateForEdge(cfg.SiteID); err != nil {
			return fmt.Errorf("replay manifest: %w", err)
		}
		manifest = &m
	}

	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return err
	}

	in, err := os.Open(opts.InputCSV)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(opts.OutputCSV)
	if err != nil {
		return err
	}
	defer out.Close()

	r := csv.NewReader(in)
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil {
		return fmt.Errorf("replay: read header: %w", err)
	}
	timeCol, metricCols, err := mapReplayHeader(header)
	if err != nil {
		return err
	}

	w := csv.NewWriter(out)
	defer w.Flush()
	if err := w.Write([]string{
		"ts", "preset", "plan_source", "reason_code",
		"p_bess_virtual_kw", "would_write_40381", "would_write_40378",
		"p_bess_plan_kw", "soc_percent", "pv_power_kw", "load_power_kw",
		"grid_power_kw", "ess_power_actual_kw", "data_quality", "rationale",
		"clamps",
	}); err != nil {
		return err
	}

	rows, decisions := 0, 0
	reasons := map[string]int{}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("replay: row %d: %w", rows+2, err)
		}
		rows++

		ts, values, ok := parseReplayRow(rec, timeCol, metricCols, loc)
		if !ok {
			continue
		}
		tick := buildTickFromValues(cfg.SiteID, cfg.SmartLogger.Topology, cfg.EffectiveEssSign(), ts, values, QualityOK)
		d, _ := Decide(tick, manifest, cfg)
		decisions++
		reasons[d.ReasonCode]++

		if err := w.Write([]string{
			d.TS.Format(time.RFC3339),
			d.Preset,
			d.PlanSource,
			d.ReasonCode,
			fmtF(d.PBessVirtualKw),
			fmtF(d.PBessVirtualKw),
			fmtF(d.PPVLimitVirtualKw),
			fmtPtr(d.PBessPlanKw),
			fmtPtr(d.SocPercent),
			fmtPtr(d.PVPowerKw),
			fmtPtr(d.LoadPowerKw),
			fmtPtr(d.GridPowerKw),
			fmtPtr(d.ESSPowerKw),
			d.DataQuality,
			d.Rationale,
			strings.Join(d.Clamps, " | "),
		}); err != nil {
			return err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}

	log.Info("edge_replay_done",
		"input", opts.InputCSV, "output", opts.OutputCSV,
		"rows", rows, "decisions", decisions, "reasons", reasonSummary(reasons))
	return nil
}

// mapReplayHeader finds the time column and maps every other column to
// a catalog metric_key (register suffixes stripped, aliases applied).
func mapReplayHeader(header []string) (timeCol int, metricCols map[int]string, err error) {
	timeCol = -1
	metricCols = map[int]string{}
	for i, name := range header {
		n := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(name, "\ufeff")))
		if n == "time" || n == "ts" || n == "timestamp" {
			timeCol = i
			continue
		}
		key := registerSuffix.ReplaceAllString(n, "")
		if alias, ok := replayAliases[key]; ok {
			key = alias
		}
		metricCols[i] = key
	}
	if timeCol == -1 {
		return 0, nil, fmt.Errorf("replay: no time/ts column in header %v", header)
	}
	return timeCol, metricCols, nil
}

func parseReplayRow(rec []string, timeCol int, metricCols map[int]string, loc *time.Location) (time.Time, map[string]float64, bool) {
	if timeCol >= len(rec) {
		return time.Time{}, nil, false
	}
	ts, err := parseReplayTime(strings.TrimSpace(rec[timeCol]), loc)
	if err != nil {
		return time.Time{}, nil, false
	}
	values := map[string]float64{}
	for i, key := range metricCols {
		if i >= len(rec) {
			continue
		}
		s := strings.TrimSpace(rec[i])
		if s == "" {
			continue
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			continue
		}
		values[key] = v
	}
	if len(values) == 0 {
		return time.Time{}, nil, false
	}
	return ts, values, true
}

func parseReplayTime(s string, loc *time.Location) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	// Naive local timestamps (DB exports, archive CSVs) are read in the
	// site timezone.
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unparseable time %q", s)
}

func fmtF(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }

func fmtPtr(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', 3, 64)
}

func reasonSummary(reasons map[string]int) string {
	keys := make([]string, 0, len(reasons))
	for k := range reasons {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, reasons[k]))
	}
	return strings.Join(parts, " ")
}
