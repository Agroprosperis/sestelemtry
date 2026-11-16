package energyflow

import (
	"sort"
	"time"
)

// GroupSite is one site of a group whose sites share one generator
// (ems-spec generator handoff §6): its raw samples, its own supply
// timeline and its allocation options.
type GroupSite struct {
	Rows     []RawSample
	Timeline *GenTimeline
	Opts     Options
}

// GroupWindow is one allocation window of such a group.
type GroupWindow struct {
	End time.Time
	// Mode is the group's: generator while the generator feeds a site
	// without the grid; else island or blackout while a site is without
	// it; else what the sites report (unknown, transition, grid).
	Mode SupplyMode
	// Partial: a site stayed on the grid while another was off it.
	Partial bool
	// Result sums the window's allocation, zero when no site has an
	// interval ending in it: the sites without the grid together with
	// the generator's energy, every other site on its own without it.
	Result GenResult
}

type groupInterval struct {
	prev, curr Sample
}

// IterateGroupWindows walks a group's sites window by window, over every
// window a site's timeline covers. The generator's energy comes once,
// from gen (the timeline of one site, all of them reading one meter).
// The sites without the grid in a window are allocated as one site,
// their deltas summed with the generator's energy once: one generator
// behind several grid inputs cannot be split between them, so it is
// counted on their sum. Energy of windows no allocation could take is
// carried to the next window that has one.
func IterateGroupWindows(sites []GroupSite, gen *GenTimeline, cb func(GroupWindow)) {
	intervals := make([]map[int64]groupInterval, len(sites))
	ends := map[int64]time.Time{}
	for i, s := range sites {
		intervals[i] = map[int64]groupInterval{}
		if s.Timeline == nil {
			continue
		}
		for k := range s.Timeline.windows {
			ends[k] = time.Unix(0, k).UTC()
		}
		walkWindows(s.Rows, walkOptions(s.Opts), nil, func(_, end time.Time, prev, curr Sample) {
			if _, ok := s.Timeline.windows[end.UnixNano()]; ok {
				intervals[i][end.UnixNano()] = groupInterval{prev: prev, curr: curr}
			}
		})
	}
	keys := make([]int64, 0, len(ends))
	for k := range ends {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool { return keys[a] < keys[b] })

	var pending float64
	var last time.Time
	for _, k := range keys {
		end := ends[k]
		after := last
		if after.IsZero() && gen != nil {
			after = end.Add(-gen.window)
		}
		pending += gen.GenBetween(after, end)
		last = end

		modes := make([]SupplyMode, len(sites))
		var off, on []int
		for i, s := range sites {
			if s.Timeline == nil {
				continue
			}
			w, ok := s.Timeline.windows[k]
			if !ok {
				continue
			}
			modes[i] = w.Mode
			if w.Mode.Offgrid() {
				off = append(off, i)
			} else {
				on = append(on, i)
			}
		}
		gw := GroupWindow{End: end, Mode: groupMode(modes, off, on), Partial: len(off) > 0 && len(on) > 0}

		var offAlloc, onAlloc []int
		for _, i := range off {
			if _, ok := intervals[i][k]; ok {
				offAlloc = append(offAlloc, i)
			}
		}
		for _, i := range on {
			if _, ok := intervals[i][k]; ok {
				onAlloc = append(onAlloc, i)
			}
		}
		if len(offAlloc)+len(onAlloc) == 0 {
			cb(gw)
			continue
		}
		g := pending
		pending = 0
		// The generator feeds the sites without the grid while it runs
		// for one of them; else a site switching over takes it.
		gOff, gOn := 0.0, -1
		switch {
		case gw.Mode == SupplyGenerator && len(offAlloc) > 0:
			gOff = g
		case g > 0:
			for _, i := range onAlloc {
				if modes[i] == SupplyTransition {
					gOn = i
					break
				}
			}
		}

		res := GenResult{Mode: gw.Mode}
		if len(offAlloc) > 0 {
			var sum genDeltas
			n := 0
			for _, i := range offAlloc {
				iv := intervals[i][k]
				d, reason := genIntervalDeltas(iv.prev, iv.curr, true, walkOptions(sites[i].Opts))
				if reason != "" {
					continue
				}
				sum.add(d)
				n++
			}
			if n > 0 {
				res.add(allocateGenDeltas(sum, GenWindow{Mode: gw.Mode, GenKwh: gOff}))
			}
		}
		for _, i := range onAlloc {
			iv := intervals[i][k]
			gi := 0.0
			if i == gOn {
				gi = g
			}
			r := AllocateGen(iv.prev, iv.curr, GenWindow{Mode: modes[i], GenKwh: gi}, walkOptions(sites[i].Opts))
			if !r.Skipped {
				res.add(r)
			}
		}
		gw.Result = res
		cb(gw)
	}
}

// groupMode is a window's mode for the group as a whole.
func groupMode(modes []SupplyMode, off, on []int) SupplyMode {
	has := func(set []int, m SupplyMode) bool {
		for _, i := range set {
			if modes[i] == m {
				return true
			}
		}
		return false
	}
	switch {
	case has(off, SupplyGenerator):
		return SupplyGenerator
	case has(off, SupplyIsland):
		return SupplyIsland
	case len(off) > 0:
		return SupplyBlackout
	case has(on, SupplyUnknown):
		return SupplyUnknown
	case has(on, SupplyTransition):
		return SupplyTransition
	case len(on) > 0:
		return SupplyGrid
	}
	return SupplyNone
}

// add sums another allocation of the same window in.
func (r *GenResult) add(o GenResult) {
	r.GenKwh += o.GenKwh
	r.PVToESSKwh += o.PVToESSKwh
	r.GenToESSKwh += o.GenToESSKwh
	r.GridToESSKwh += o.GridToESSKwh
	r.ESSToLoadKwh += o.ESSToLoadKwh
	r.ESSToGridKwh += o.ESSToGridKwh
	r.GenToLoadKwh += o.GenToLoadKwh
	r.PVToLoadKwh += o.PVToLoadKwh
	r.LoadKwh += o.LoadKwh
	r.EssChargedKwh += o.EssChargedKwh
	r.EssDischargedKwh += o.EssDischargedKwh
	r.LoadOffgridKwh += o.LoadOffgridKwh
	r.PVToLoadOffgridKwh += o.PVToLoadOffgridKwh
	r.ESSToLoadOffgridKwh += o.ESSToLoadOffgridKwh
	r.PVToESSOffgridKwh += o.PVToESSOffgridKwh
	r.PVToLoadIslandKwh += o.PVToLoadIslandKwh
	r.ESSToLoadIslandKwh += o.ESSToLoadIslandKwh
	r.OffgridImportKwh += o.OffgridImportKwh
	r.OffgridExportKwh += o.OffgridExportKwh
}
