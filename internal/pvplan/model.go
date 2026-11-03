package pvplan

import (
	"math"
	"sort"
	"time"
)

// The model is the one the n8n flow ran (Update SES Plan), so a station
// switched from the flow to this package keeps the same plan for the
// same weather.
const (
	noctC      = 45.0
	lossFactor = 0.90

	// ModelVersion tags plans computed from the live forecast.
	ModelVersion = "pv_v1_noct45_loss0.9"
	// HistoryModelVersion tags days rebuilt from the Previous Runs
	// archive, whose lead time is a flat 24 h instead of the live
	// run's 6–30 h.
	HistoryModelVersion = ModelVersion + "_hist"
)

// HourInput is one plane's weather for one hour. Start is the UTC
// start of the hour the irradiance averages over.
type HourInput struct {
	Start  time.Time
	TempC  float64
	GtiWm2 float64
}

// planeDcKw is the DC output of one plane: irradiance on the plane
// scaled by a cell-temperature correction (NOCT model) and a flat
// system loss.
func planeDcKw(o Orientation, gammaPctPerC float64, h HourInput) float64 {
	tCell := h.TempC + (noctC-20)/800*h.GtiWm2
	tempFactor := math.Max(0, 1+gammaPctPerC/100*(tCell-25))
	return math.Max(0, o.PdcKw()*(h.GtiWm2/1000)*tempFactor*lossFactor)
}

// stationAcKw clips the summed DC output to the station's nameplate and
// then to its inverter. The clip is per station, after the planes are
// summed: clipping each plane on its own would let a site's planes add
// up past its inverter.
func stationAcKw(st Station, rawDcKw float64) float64 {
	dc := math.Min(st.PdcTotalKwp(), rawDcKw)
	if st.PacLimitKw > 0 {
		dc = math.Min(st.PacLimitKw, dc)
	}
	return math.Round(dc*1000) / 1000
}

// PlanHours turns per-plane weather into station kWh per UTC hour
// start; series[i] is the weather of st.EnabledOrientations()[i]. Each
// hour lasts an hour, so its kWh equals its average kW. An hour that
// any plane has no weather for is left out rather than counted as dark.
func PlanHours(st Station, series [][]HourInput) map[time.Time]float64 {
	planes := st.EnabledOrientations()
	if len(planes) == 0 || len(series) != len(planes) {
		return nil
	}
	byPlane := make([]map[time.Time]HourInput, len(planes))
	for i, s := range series {
		m := make(map[time.Time]HourInput, len(s))
		for _, h := range s {
			m[h.Start.UTC()] = h
		}
		byPlane[i] = m
	}
	out := make(map[time.Time]float64, len(byPlane[0]))
	for start := range byPlane[0] {
		raw := 0.0
		complete := true
		for i, o := range planes {
			h, ok := byPlane[i][start]
			if !ok {
				complete = false
				break
			}
			raw += planeDcKw(o, st.GammaPctPerC, h)
		}
		if complete {
			out[start] = stationAcKw(st, raw)
		}
	}
	return out
}

// PlanDay is one civil day of a station's plan.
type PlanDay struct {
	Day   string          // YYYY-MM-DD in the station's zone
	Hours map[int]float64 // local hour start 0..23 → kWh
	Total float64
}

// GroupDays splits hourly kWh into civil days in loc and keeps only
// days that have every hour: a day missing its afternoon would read as
// a dull day rather than as an unknown one. On the autumn DST day the
// repeated local hour keeps its later value.
func GroupDays(hours map[time.Time]float64, loc *time.Location) []PlanDay {
	starts := make([]time.Time, 0, len(hours))
	for t := range hours {
		starts = append(starts, t)
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i].Before(starts[j]) })

	type acc struct {
		day   PlanDay
		count int
	}
	byDay := map[string]*acc{}
	var order []string
	for _, t := range starts {
		local := t.In(loc)
		key := local.Format("2006-01-02")
		a, ok := byDay[key]
		if !ok {
			a = &acc{day: PlanDay{Day: key, Hours: map[int]float64{}}}
			byDay[key] = a
			order = append(order, key)
		}
		a.day.Hours[local.Hour()] = hours[t]
		a.count++
	}
	out := make([]PlanDay, 0, len(order))
	for _, key := range order {
		a := byDay[key]
		if a.count != hoursInDay(key, loc) {
			continue
		}
		for _, v := range a.day.Hours {
			a.day.Total += v
		}
		a.day.Total = math.Round(a.day.Total*1000) / 1000
		out = append(out, a.day)
	}
	return out
}

// hoursInDay is 23, 24 or 25 depending on DST.
func hoursInDay(day string, loc *time.Location) int {
	start, err := time.ParseInLocation("2006-01-02", day, loc)
	if err != nil {
		return 24
	}
	end := time.Date(start.Year(), start.Month(), start.Day()+1, 0, 0, 0, 0, loc)
	return int(end.Sub(start) / time.Hour)
}
