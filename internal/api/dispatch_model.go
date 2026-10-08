package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/nesh/sestelemetry/internal/dispatch"
	"github.com/nesh/sestelemetry/internal/storage"
)

// dispatchStoredModel is a confirmed plan keyed by UTC hour.
type dispatchStoredModel struct {
	Loads  []dispatchStoredLoad  `json:"loads"`
	Blocks []dispatchStoredBlock `json:"blocks"`
	Cfg    dispatch.Constraints  `json:"cfg"`
}

type dispatchStoredLoad struct {
	TS time.Time `json:"ts"`
	Kw float64   `json:"kw"`
}

type dispatchStoredBlock struct {
	ID        string               `json:"id"`
	From      time.Time            `json:"from"`
	Until     time.Time            `json:"until"`
	Type      dispatch.CommandType `json:"type"`
	Value     float64              `json:"value"`
	Direction string               `json:"direction,omitempty"`
}

// storedFromModel keys an aligned model by UTC hour; consecutive hours
// of one block (same ID and command) become one stored block.
func storedFromModel(m dispatch.Model, start time.Time) dispatchStoredModel {
	sm := dispatchStoredModel{Loads: []dispatchStoredLoad{}, Blocks: []dispatchStoredBlock{}, Cfg: m.Cfg}
	for i, v := range m.Loads {
		if v != nil {
			sm.Loads = append(sm.Loads, dispatchStoredLoad{TS: start.Add(time.Duration(i) * time.Hour), Kw: *v})
		}
	}
	for i := 0; i < len(m.Commands); i++ {
		c := m.Commands[i]
		if c == nil || c.Type == dispatch.CmdAuto {
			continue
		}
		j := i + 1
		for j < len(m.Commands) && m.Commands[j] != nil && *m.Commands[j] == *c {
			j++
		}
		sm.Blocks = append(sm.Blocks, dispatchStoredBlock{
			ID: c.ID, From: start.Add(time.Duration(i) * time.Hour), Until: start.Add(time.Duration(j) * time.Hour),
			Type: c.Type, Value: c.Value, Direction: c.Direction,
		})
		i = j - 1
	}
	return sm
}

// modelFromStored aligns a stored plan to `start` over n hours. Hours
// already gone are dropped; a block in progress keeps its remainder.
func modelFromStored(sm dispatchStoredModel, start time.Time, n int) dispatch.Model {
	m := dispatch.Model{Loads: make([]*float64, n), Commands: make([]*dispatch.Command, n), Cfg: sm.Cfg}
	index := func(ts time.Time) int { return int(math.Round(ts.Sub(start).Hours())) }
	for _, l := range sm.Loads {
		if i := index(l.TS); i >= 0 && i < n {
			v := l.Kw
			m.Loads[i] = &v
		}
	}
	for _, b := range sm.Blocks {
		cmd := dispatch.Command{Type: b.Type, Value: b.Value, Direction: b.Direction, ID: b.ID}
		for i := index(b.From); i < index(b.Until); i++ {
			if i >= 0 && i < n {
				c := cmd
				m.Commands[i] = &c
			}
		}
	}
	return m
}

// sanitizeConstraints fits a stored or defaulted cfg into the current
// envelope (limits may have changed in «Обмеження» since it was saved).
func sanitizeConstraints(env *dispatchEnv, cfg dispatch.Constraints) dispatch.Constraints {
	site := env.site
	cfg.ReservePct = math.Min(math.Max(cfg.ReservePct, site.SocMinPct), site.SocMaxPct)
	if cfg.ImportCapKw <= 0 || cfg.ImportCapKw > site.ImportKw {
		cfg.ImportCapKw = site.ImportKw
	}
	// A cleared cap is the operator's «експорт вимкнено», not «take the
	// ceiling»; only a value above the ceiling is cut down.
	switch {
	case env.exportCeiling == nil:
		cfg.ExportCapKw = nil
	case cfg.ExportCapKw != nil && *cfg.ExportCapKw > *env.exportCeiling:
		cfg.ExportCapKw = copyKw(env.exportCeiling)
	}
	if cfg.ExportCapKw == nil {
		cfg.EssSale = false
	}
	return cfg
}

// appliedModel is the latest confirmed plan aligned to env.start, or an
// empty AUTO model with the default limits.
func (h *Handlers) appliedModel(ctx context.Context, env *dispatchEnv) (dispatch.Model, storage.DispatchVersion, error) {
	ver, has, err := storage.LatestDispatchVersion(ctx, h.edge.Pool, env.siteID)
	if err != nil {
		return dispatch.Model{}, ver, err
	}
	sm := dispatchStoredModel{Cfg: env.defaults}
	if has {
		if err := json.Unmarshal(ver.Model, &sm); err != nil {
			return dispatch.Model{}, ver, fmt.Errorf("dispatch version %d: %w", ver.Version, err)
		}
	}
	m := modelFromStored(sm, env.start, dispatchFutureHours)
	m.Cfg = sanitizeConstraints(env, m.Cfg)
	return m, ver, nil
}

// --- request / response --------------------------------------------------

type dispatchDraftRequest struct {
	StartHour   time.Time            `json:"start_hour"`
	BaseVersion int                  `json:"base_version"`
	Loads       []*float64           `json:"loads"`
	Commands    []*dispatch.Command  `json:"commands"`
	Cfg         dispatch.Constraints `json:"cfg"`
}

// model aligns the draft to the server's current hour: hours that went
// by since the client loaded the page are dropped.
func (r dispatchDraftRequest) model(start time.Time) (dispatch.Model, error) {
	shift := 0
	if !r.StartHour.IsZero() {
		shift = int(math.Round(start.Sub(r.StartHour.UTC()).Hours()))
	}
	if shift < 0 {
		return dispatch.Model{}, fmt.Errorf("start_hour is in the future")
	}
	m := dispatch.Model{Loads: make([]*float64, dispatchFutureHours), Commands: make([]*dispatch.Command, dispatchFutureHours), Cfg: r.Cfg}
	for i := 0; i < dispatchFutureHours; i++ {
		src := i + shift
		if src < len(r.Loads) {
			m.Loads[i] = r.Loads[src]
		}
		if src < len(r.Commands) {
			if c := r.Commands[src]; c != nil && c.Type != dispatch.CmdAuto {
				m.Commands[i] = c
			}
		}
	}
	return m, nil
}

// validateModel runs the mockup's input checks on every command and on
// the horizon-wide limits. "" = valid.
func validateModel(env *dispatchEnv, m dispatch.Model) string {
	if msg := dispatch.ValidateConstraints(env.site, m.Cfg, env.exportCeiling); msg != "" {
		return msg
	}
	for i, c := range m.Commands {
		if c == nil {
			continue
		}
		if msg := dispatch.ValidateCommand(env.site, m.Cfg, *c); msg != "" {
			return hourLabel(env, i) + ": " + msg
		}
	}
	return ""
}

func hourLabel(env *dispatchEnv, i int) string {
	t := env.start.Add(time.Duration(i) * time.Hour).In(env.loc)
	return t.Format("15:04") + "–" + t.Add(time.Hour).Format("15:04")
}
