// Package counterstitch runs the counter stitcher against the database:
// it scans each lifetime counter for discontinuities since the last
// pass, stores the corrections every counter delta query applies, and
// records the episodes behind day flags and alerts.
package counterstitch

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nesh/sestelemetry/internal/counters"
	"github.com/nesh/sestelemetry/internal/storage"
)

// settleDelay keeps a pass off the samples still being written, so a
// step is never judged on half of its second.
const settleDelay = 2 * time.Minute

// chunk bounds one candidate scan. A day of 1 Hz samples is ~86k rows
// per counter, which the window function sorts in memory.
const chunk = 24 * time.Hour

// frozenRules lists the counters whose standing still is a fault: an
// elevator always draws something from the grid and PV always yields
// something over two days. Export and the ESS counters legitimately sit
// idle for weeks.
var frozenRules = []struct {
	metric  string
	minDays int
}{
	{"accumulated_pv_energy_yield_kwh", 2},
	{"accumulated_electricity_purchased_kwh", 2},
}

// frozenMinSamples is how many readings a day needs before its standing
// still means anything (a 5-minute archive day holds 288).
const frozenMinSamples = 200

// frozenLookbackDays is how far back each pass re-reads day spans: long
// enough to see a frozen stretch end, short enough to stay cheap.
const frozenLookbackDays = 60

// Runner stitches the counters of one database.
type Runner struct {
	Pool *pgxpool.Pool
	Log  *slog.Logger
	// HasCAGG enables frozen-counter detection, which reads the daily
	// continuous aggregate.
	HasCAGG bool
	// Loc is the civil-day zone of the daily aggregate.
	Loc *time.Location
	// Now is the clock; time.Now when nil.
	Now func() time.Time
	// OffgridBetween reports the site off the grid inside [from, to];
	// storage.OffgridBetween when nil.
	OffgridBetween func(ctx context.Context, org string, from, to time.Time) (bool, error)
}

// The windows around a dropout that tell an outage explains it
// (counters.KindOffgrid): the generator controller sees the switchover
// within seconds of the fall, and a counter back within minutes of the
// grid came back with it. offgridRecheck is how far back stored dropouts
// are looked at again, for readings that arrived after the pass.
const (
	offgridBeforeFall   = 30 * time.Second
	offgridAfterFall    = 2 * time.Minute
	offgridBeforeReturn = 10 * time.Minute
	offgridRecheck      = 7 * 24 * time.Hour
)

func (r *Runner) offgridBetween(ctx context.Context, org string, from, to time.Time) (bool, error) {
	if r.OffgridBetween != nil {
		return r.OffgridBetween(ctx, org, from, to)
	}
	return storage.OffgridBetween(ctx, r.Pool, org, from, to)
}

// outageDropout reports a dropout of the site that its outage explains.
func (r *Runner) outageDropout(ctx context.Context, org string, start, end time.Time) (bool, error) {
	off, err := r.offgridBetween(ctx, org, start.Add(-offgridBeforeFall), start.Add(offgridAfterFall))
	if err != nil || off || end.IsZero() {
		return off, err
	}
	return r.offgridBetween(ctx, org, end.Add(-offgridBeforeReturn), end)
}

// markOffgrid reclassifies the dropouts among episodes that the site's
// outage explains.
func (r *Runner) markOffgrid(ctx context.Context, org string, episodes []counters.Episode) error {
	for i, e := range episodes {
		if e.Kind != counters.KindDropout {
			continue
		}
		off, err := r.outageDropout(ctx, org, e.Start, e.End)
		if err != nil {
			return err
		}
		if off {
			episodes[i].Kind = counters.KindOffgrid
		}
	}
	return nil
}

// recheckOffgrid reclassifies the recent stored dropouts that the site's
// outage explains, and returns the days whose flags that changes.
func (r *Runner) recheckOffgrid(ctx context.Context, org string, until time.Time) ([]Span, error) {
	stored, err := storage.CounterEpisodesSince(ctx, r.Pool, org, string(counters.KindDropout), until.Add(-offgridRecheck))
	if err != nil {
		return nil, err
	}
	var write []storage.CounterEpisode
	var changed []Span
	for _, e := range stored {
		var end time.Time
		if e.EndedAt != nil {
			end = *e.EndedAt
		}
		off, err := r.outageDropout(ctx, org, e.StartedAt, end)
		if err != nil {
			return nil, err
		}
		if !off {
			continue
		}
		e.Kind = string(counters.KindOffgrid)
		write = append(write, e)
		to := until
		if !end.IsZero() {
			to = end
		}
		changed = append(changed, Span{From: e.StartedAt, To: to, FlagsOnly: true})
	}
	if err := storage.UpsertCounterEpisodes(ctx, r.Pool, org, write); err != nil {
		return nil, err
	}
	return changed, nil
}

// dropoutSettle is how long a counter has to go without falling again
// before its open dropouts count as resets (counters.KindReset).
// resetMoveKwh is how far it has to have metered past its last fall.
const (
	dropoutSettle = 14 * 24 * time.Hour
	resetMoveKwh  = 1.0
)

// settleDropouts reclassifies as resets the open dropouts of every
// counter that has not fallen for dropoutSettle and kept metering since,
// and returns the days whose flags that changes: they flagged every day
// after their start.
func (r *Runner) settleDropouts(ctx context.Context, org string, until time.Time) ([]Span, error) {
	open, err := storage.OpenCounterEpisodes(ctx, r.Pool, org, string(counters.KindDropout))
	if err != nil {
		return nil, err
	}
	resets, err := resetsOf(open, until, func(metric string, since time.Time) (float64, bool, error) {
		return storage.CounterMaxSince(ctx, r.Pool, org, metric, since)
	})
	if err != nil {
		return nil, err
	}
	changed := make([]Span, 0, len(resets))
	for _, e := range resets {
		changed = append(changed, Span{From: e.StartedAt, To: until, FlagsOnly: true})
	}
	if err := storage.UpsertCounterEpisodes(ctx, r.Pool, org, resets); err != nil {
		return nil, err
	}
	return changed, nil
}

// resetsOf picks the open dropouts (oldest first) that count as resets:
// those of a counter whose newest fall is dropoutSettle old and that has
// metered resetMoveKwh past it since. They come back closed at their
// start, as KindReset.
func resetsOf(open []storage.CounterEpisode, until time.Time, maxSince func(metric string, since time.Time) (float64, bool, error)) ([]storage.CounterEpisode, error) {
	newest := map[string]storage.CounterEpisode{}
	for _, e := range open {
		if n, ok := newest[e.MetricKey]; !ok || e.StartedAt.After(n.StartedAt) {
			newest[e.MetricKey] = e
		}
	}
	settled := map[string]bool{}
	for metric, last := range newest {
		if until.Sub(last.StartedAt) < dropoutSettle {
			continue
		}
		top, ok, err := maxSince(metric, last.StartedAt.Add(time.Hour))
		if err != nil {
			return nil, err
		}
		settled[metric] = ok && top-last.ValueAfter >= resetMoveKwh
	}
	var out []storage.CounterEpisode
	for _, e := range open {
		if !settled[e.MetricKey] {
			continue
		}
		e.Kind = string(counters.KindReset)
		start := e.StartedAt
		e.EndedAt = &start
		out = append(out, e)
	}
	return out, nil
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Span is a half-open stretch [From, To) whose stitched readings or day
// flags a pass changed; From == To marks the instant of a single step.
type Span struct {
	From time.Time
	To   time.Time
	// FlagsOnly: only the days' counter flags changed, not their readings
	// (an episode's kind or end), so the days need no recompute.
	FlagsOnly bool
}

// Sync brings the organization's corrections and episodes up to date
// and returns the spans it changed, so the caller knows which economics
// days to recompute. An offset that holds over a whole segment leaves
// the deltas inside it alone; what changes is the step between two
// segments, a gap's ramp, and the days an episode flags.
func (r *Runner) Sync(ctx context.Context, org string) ([]Span, error) {
	until := r.now().Add(-settleDelay).UTC().Truncate(time.Second)
	rebuild, hasRebuild, err := storage.PendingCounterRebuild(ctx, r.Pool, org)
	if err != nil {
		return nil, err
	}
	states, err := storage.LoadCounterStitchStates(ctx, r.Pool, org)
	if err != nil {
		return nil, err
	}

	var changed []Span
	for _, metric := range counters.Metrics {
		prev, ok := states[metric]
		replace := hasRebuild || !ok
		var st counters.State
		from := prev.ProcessedUntil
		if replace {
			first, has, err := storage.FirstSampleTime(ctx, r.Pool, org, metric)
			if err != nil {
				return nil, err
			}
			if !has {
				continue
			}
			from = first
		} else if err := json.Unmarshal(prev.State, &st); err != nil {
			return nil, fmt.Errorf("counter state %s/%s: %w", org, metric, err)
		}
		if !from.Before(until) {
			continue
		}
		spans, err := r.stitch(ctx, org, metric, st, from, until, replace)
		if err != nil {
			return nil, err
		}
		// A rebuild redoes history the data before ChangedFrom already
		// had, so only the requested bound counts for it.
		if !hasRebuild {
			changed = append(changed, spans...)
		}
	}

	spans, err := r.recheckOffgrid(ctx, org, until)
	if err != nil {
		return nil, err
	}
	changed = append(changed, spans...)
	spans, err = r.settleDropouts(ctx, org, until)
	if err != nil {
		return nil, err
	}
	changed = append(changed, spans...)

	if r.HasCAGG {
		spans, err := r.syncFrozen(ctx, org, until, hasRebuild || len(states) == 0)
		if err != nil {
			return nil, err
		}
		changed = append(changed, spans...)
	}

	if hasRebuild {
		changed = append(changed, Span{From: rebuild.ChangedFrom, To: until})
		if err := storage.ClearCounterRebuild(ctx, r.Pool, org, rebuild); err != nil {
			return nil, err
		}
	}
	return changed, nil
}

// stitch runs one counter over [from, until) day by day and stores the
// pass. It returns the spans the pass changed.
func (r *Runner) stitch(ctx context.Context, org, metric string, st counters.State, from, until time.Time, replace bool) ([]Span, error) {
	var segments []counters.Segment
	var episodes []counters.Episode
	for at := from; at.Before(until); {
		next := at.Add(chunk)
		if next.After(until) {
			next = until
		}
		rows, err := storage.CounterStepCandidates(ctx, r.Pool, org, metric, at, next,
			counters.DropTolKwh, counters.CandidateFloorKwh, counters.MaxPowerKw, counters.SlackKwh, counters.CoarseUpdate)
		if err != nil {
			return nil, err
		}
		if len(rows) > 0 {
			steps := make([]counters.Step, len(rows))
			for i, row := range rows {
				steps[i] = counters.Step{PrevTime: row.PrevTime, Prev: row.Prev, Time: row.Time, Value: row.Value, Settled: row.Settled}
			}
			res := counters.Run(st, steps)
			st = res.State
			segments = mergeSegments(segments, res.Segments)
			episodes = mergeEpisodes(episodes, res.Episodes)
		}
		at = next
	}

	if err := r.markOffgrid(ctx, org, episodes); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return nil, err
	}
	state := storage.CounterStitchState{MetricKey: metric, ProcessedUntil: until, State: raw}
	if err := storage.SaveCounterStitch(ctx, r.Pool, org, state,
		toStoredSegments(metric, segments), toStoredEpisodes(org, metric, episodes), replace); err != nil {
		return nil, err
	}

	if len(episodes) > 0 && r.Log != nil {
		r.Log.Info("counter_stitch",
			"organization_id", org,
			"metric_key", metric,
			"from", from.Format(time.RFC3339),
			"until", until.Format(time.RFC3339),
			"segments", len(segments),
			"episodes", len(episodes),
			"open_falls_kwh", st.Offset()-st.Base,
		)
	}
	return changeSpans(segments, episodes, from, until), nil
}

// changeSpans lists what a pass over [from, until) changed. A segment
// adds the step at its start, or across its whole length when it closed
// on a ramp; one carried over from an earlier pass and closed flat only
// adds the step at its end. An episode flags every day it covers; one
// carried over and closed in this pass only stops flagging the days
// after its end.
func changeSpans(segments []counters.Segment, episodes []counters.Episode, from, until time.Time) []Span {
	var out []Span
	for _, g := range segments {
		switch {
		case g.Start.Before(from) && !g.End.IsZero() && g.EndOffset == g.StartOffset:
			out = append(out, Span{From: g.End, To: g.End})
		case !g.End.IsZero() && g.EndOffset != g.StartOffset:
			out = append(out, Span{From: g.Start, To: g.End})
		default:
			out = append(out, Span{From: g.Start, To: g.Start})
		}
	}
	for _, e := range episodes {
		switch {
		case e.Start.Before(from) && !e.End.IsZero():
			out = append(out, Span{From: e.End, To: until})
		case e.End.IsZero():
			out = append(out, Span{From: e.Start, To: until})
		default:
			out = append(out, Span{From: e.Start, To: e.End})
		}
	}
	return out
}

// syncFrozen finds counters standing still for days and stores the
// stretches that are new or changed. Stretches inside a dropout are the
// dropout's stale reading, already flagged as such, and are left out.
func (r *Runner) syncFrozen(ctx context.Context, org string, until time.Time, full bool) ([]Span, error) {
	loc := r.Loc
	if loc == nil {
		loc = time.UTC
	}
	today := civilDay(until, loc)
	var changed []Span
	for _, rule := range frozenRules {
		from := today.AddDate(0, 0, -frozenLookbackDays)
		if full {
			first, has, err := storage.FirstSampleTime(ctx, r.Pool, org, rule.metric)
			if err != nil {
				return nil, err
			}
			if !has {
				continue
			}
			from = civilDay(first, loc)
		}
		to := today.AddDate(0, 0, 1)
		rows, err := storage.CounterDaySpans(ctx, r.Pool, org, rule.metric, from, to)
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			continue
		}
		spans := make([]counters.DaySpan, len(rows))
		for i, d := range rows {
			spans[i] = counters.DaySpan{Day: d.Day.In(loc), First: d.First, Last: d.Last, Samples: d.Samples}
		}
		openEnd := spans[len(spans)-1].Day.Equal(today)
		found := counters.FrozenEpisodes(spans, rule.minDays, frozenMinSamples, openEnd)
		stored, err := storage.CounterEpisodesOfKind(ctx, r.Pool, org, rule.metric, string(counters.KindFrozen), from, to)
		if err != nil {
			return nil, err
		}
		write := staleFrozen(stored, found, spans)
		for _, ep := range write {
			changed = append(changed, Span{From: ep.End, To: until, FlagsOnly: true})
		}
		if len(found) > 0 {
			dropouts, err := storage.CounterEpisodesOfKind(ctx, r.Pool, org, rule.metric, string(counters.KindDropout), time.Time{}, to)
			if err != nil {
				return nil, err
			}
			outages, err := storage.CounterEpisodesOfKind(ctx, r.Pool, org, rule.metric, string(counters.KindOffgrid), time.Time{}, to)
			if err != nil {
				return nil, err
			}
			dropouts = append(dropouts, outages...)
			for _, ep := range found {
				if insideDropout(ep, dropouts, until) || unchanged(ep, stored) {
					continue
				}
				write = append(write, ep)
				end := ep.End
				if end.IsZero() {
					end = until
				}
				changed = append(changed, Span{From: ep.Start, To: end, FlagsOnly: true})
			}
		}
		if err := storage.UpsertCounterEpisodes(ctx, r.Pool, org, toStoredEpisodes(org, rule.metric, write)); err != nil {
			return nil, err
		}
	}
	return changed, nil
}

// staleFrozen closes the stored open frozen stretches the pass no longer
// finds. A stretch reaching into a partly-read today is stored open; once
// the counter moves that day, the stretch is shorter than the rule and
// is not found again, so it is closed where the counter stood still.
func staleFrozen(stored []storage.CounterEpisode, found []counters.Episode, days []counters.DaySpan) []counters.Episode {
	var out []counters.Episode
	for _, s := range stored {
		if s.EndedAt != nil || slices.ContainsFunc(found, func(f counters.Episode) bool { return f.Start.Equal(s.StartedAt) }) {
			continue
		}
		out = append(out, counters.Episode{
			Kind:   counters.KindFrozen,
			Start:  s.StartedAt,
			End:    counters.StillUntil(days, s.StartedAt, frozenMinSamples),
			Before: s.ValueBefore,
			After:  s.ValueAfter,
		})
	}
	return out
}

// insideDropout reports whether a frozen stretch overlaps a dropout of
// the same counter.
func insideDropout(ep counters.Episode, dropouts []storage.CounterEpisode, until time.Time) bool {
	epEnd := ep.End
	if epEnd.IsZero() {
		epEnd = until
	}
	for _, d := range dropouts {
		dEnd := until
		if d.EndedAt != nil {
			dEnd = *d.EndedAt
		}
		if d.StartedAt.Before(epEnd) && dEnd.After(ep.Start) {
			return true
		}
	}
	return false
}

func unchanged(ep counters.Episode, stored []storage.CounterEpisode) bool {
	for _, s := range stored {
		if !s.StartedAt.Equal(ep.Start) {
			continue
		}
		if s.EndedAt == nil {
			return ep.End.IsZero()
		}
		return !ep.End.IsZero() && s.EndedAt.Equal(ep.End)
	}
	return false
}

func civilDay(t time.Time, loc *time.Location) time.Time {
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc)
}

// mergeSegments folds a later pass's segments into the list, replacing
// entries with the same start (the open segment getting closed).
func mergeSegments(list, more []counters.Segment) []counters.Segment {
	for _, g := range more {
		replaced := false
		for i := range list {
			if list[i].Start.Equal(g.Start) {
				list[i] = g
				replaced = true
				break
			}
		}
		if !replaced {
			list = append(list, g)
		}
	}
	return list
}

func mergeEpisodes(list, more []counters.Episode) []counters.Episode {
	for _, e := range more {
		replaced := false
		for i := range list {
			if list[i].Start.Equal(e.Start) {
				list[i] = e
				replaced = true
				break
			}
		}
		if !replaced {
			list = append(list, e)
		}
	}
	return list
}

func toStoredSegments(metric string, segs []counters.Segment) []storage.CounterSegment {
	out := make([]storage.CounterSegment, 0, len(segs))
	for _, g := range segs {
		row := storage.CounterSegment{
			MetricKey:   metric,
			StartsAt:    g.Start,
			OffsetStart: g.StartOffset,
			OffsetEnd:   g.StartOffset,
		}
		if !g.End.IsZero() {
			end := g.End
			row.EndsAt = &end
			row.OffsetEnd = g.EndOffset
		}
		out = append(out, row)
	}
	return out
}

func toStoredEpisodes(org, metric string, eps []counters.Episode) []storage.CounterEpisode {
	out := make([]storage.CounterEpisode, 0, len(eps))
	for _, e := range eps {
		row := storage.CounterEpisode{
			OrganizationID: org,
			MetricKey:      metric,
			StartedAt:      e.Start,
			Kind:           string(e.Kind),
			ValueBefore:    e.Before,
			ValueAfter:     e.After,
		}
		if !e.End.IsZero() {
			end := e.End
			row.EndedAt = &end
			dropout := e.Kind == counters.KindDropout || e.Kind == counters.KindOffgrid
			if dropout || e.Kind == counters.KindSpike {
				returned := e.Returned
				row.ValueReturned = &returned
			}
			if dropout {
				gap := e.GapKwh
				row.GapKwh = &gap
			}
		}
		out = append(out, row)
	}
	return out
}
