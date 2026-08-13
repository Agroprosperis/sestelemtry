package askoe

import (
	"sort"
	"time"

	"github.com/nesh/sestelemetry/internal/storage"
)

const (
	SourceLabel = "source"
	SourceValue = "askoe"

	pvMetric        = "accumulated_pv_energy_yield_kwh"
	importMetric    = "accumulated_electricity_purchased_kwh"
	exportMetric    = "accumulated_electricity_sold_kwh"
	loadMetric      = "accumulated_power_consumption_kwh"
	chargeMetric    = "total_energy_charged_kwh"
	dischargeMetric = "total_energy_discharged_kwh"
)

// OrgHosts mirrors the FusionSolar stamp so dual-SmartLogger sites
// (Zhmerynka) keep PV/grid on the site logger and ESS zeros on the ESS
// logger — otherwise the energy-flow allocator drops the day.
type OrgHosts struct {
	Default     string
	ByMetricKey map[string]string
}

func (h OrgHosts) hostFor(metric string) string {
	if h.ByMetricKey != nil {
		if v, ok := h.ByMetricKey[metric]; ok && v != "" {
			return v
		}
	}
	return h.Default
}

// ImportableMetricKeys is the set the idempotent delete rewrites.
func ImportableMetricKeys() []string {
	return []string{pvMetric, importMetric, exportMetric, loadMetric, chargeMetric, dischargeMetric}
}

const stepsPerHour = 12

// BuildDaySamples turns one complete ASKOE day into 5-minute lifetime
// counters. The hour's kWh is spread across 12 equal steps so the
// dashboard's delta×12 power reconstruction reads as the hour's average
// kW instead of a 12× spike in a single 5-minute bucket.
//
// `seed` is the running cumulative at local midnight (end of the previous
// imported day). ESS charge/discharge stay flat at the seed — ASKOE has
// no battery meters, but the allocator requires the pair.
func BuildDaySamples(orgID string, hosts OrgHosts, loc *time.Location, day civilDay, grid HourGrid, seed Counters) []storage.Sample {
	imp := grid.Import[day]
	exp := grid.Export[day]
	pv := grid.PV[day]
	midnight := day.Time(loc)
	dayEnd := midnight.AddDate(0, 0, 1)
	out := make([]storage.Sample, 0, 24*stepsPerHour*6)
	c := seed
	for h := 0; h < 24; h++ {
		dPV := pv[h] / stepsPerHour
		dImp := imp[h] / stepsPerHour
		dExp := exp[h] / stepsPerHour
		dLoad := dPV + dImp - dExp
		for s := 1; s <= stepsPerHour; s++ {
			c.PV += dPV
			c.Import += dImp
			c.Export += dExp
			c.Load += dLoad
			if c.Load < 0 {
				c.Load = 0
			}
			t := midnight.Add(time.Duration(h)*time.Hour + time.Duration(s)*5*time.Minute)
			// Keep every sample inside [midnight, next midnight) so a
			// day's closing 24:00 is not written onto the next civil
			// day (which may already hold FusionSolar / live data).
			// Hour 23 step 12 would be 24:00; step 11 is already 23:55,
			// so replace that slot instead of inserting a duplicate.
			if !t.Before(dayEnd) {
				t = dayEnd.Add(-5 * time.Minute)
				if n := len(samplesAt(orgID, hosts, t, c)); len(out) >= n {
					out = out[:len(out)-n]
				}
			}
			out = append(out, samplesAt(orgID, hosts, t, c)...)
		}
	}
	return out
}

// Counters is the running lifetime snapshot handed from one imported
// day to the next so deltas stay continuous across midnight.
type Counters struct {
	PV, Import, Export, Load float64
}

func EndCounters(samples []storage.Sample) Counters {
	var c Counters
	for _, s := range samples {
		switch s.MetricKey {
		case pvMetric:
			c.PV = s.Value
		case importMetric:
			c.Import = s.Value
		case exportMetric:
			c.Export = s.Value
		case loadMetric:
			c.Load = s.Value
		}
	}
	return c
}

func samplesAt(orgID string, hosts OrgHosts, t time.Time, c Counters) []storage.Sample {
	vals := []struct {
		key string
		v   float64
	}{
		{pvMetric, c.PV},
		{importMetric, c.Import},
		{exportMetric, c.Export},
		{loadMetric, c.Load},
		{chargeMetric, 0},
		{dischargeMetric, 0},
	}
	out := make([]storage.Sample, 0, len(vals))
	for _, row := range vals {
		l := map[string]string{SourceLabel: SourceValue}
		if host := hosts.hostFor(row.key); host != "" {
			l["device_host"] = host
		}
		out = append(out, storage.Sample{
			Time:           t,
			OrganizationID: orgID,
			MetricKey:      row.key,
			Value:          row.v,
			Labels:         l,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MetricKey < out[j].MetricKey })
	return out
}

// SeedAtMidnight writes the opening snapshot at local midnight so the
// first 5-minute delta of the first imported day has a prev sample.
func SeedAtMidnight(orgID string, hosts OrgHosts, midnight time.Time, seed Counters) []storage.Sample {
	return samplesAt(orgID, hosts, midnight, seed)
}
