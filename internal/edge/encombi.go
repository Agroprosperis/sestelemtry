package edge

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"github.com/nesh/sestelemetry/internal/modbusclient"
)

// Encombi reads (ems_offgrid_island_generator.md §4.1), FC04, unit 1:
// the generator meter block from 0 (power at 0, bus phases at 38, 40,
// 42, all 32-bit big-endian), the meter's online flag at 199 and the
// discrete inputs at 20500. Read only: nothing here ever writes.
const (
	encombiMeterStart = 0
	encombiMeterQty   = 44
	encombiAliveAddr  = 199
	encombiDIAddr     = 20500
)

// encombiReading is one complete poll.
type encombiReading struct {
	At         time.Time
	DIBits     uint16
	GenPowerKw float64
	BusV       [3]float64
	Alive      bool
}

func decodeEncombi(meter, alive, di []byte, at time.Time) (encombiReading, error) {
	if len(meter) < encombiMeterQty*2 || len(alive) < 2 || len(di) < 2 {
		return encombiReading{}, fmt.Errorf("encombi: short frame (%d/%d/%d bytes)", len(meter), len(alive), len(di))
	}
	u32 := func(reg int) uint32 { return binary.BigEndian.Uint32(meter[reg*2:]) }
	r := encombiReading{
		At:         at,
		DIBits:     binary.BigEndian.Uint16(di),
		GenPowerKw: float64(int32(u32(0))) / 1000,
		Alive:      binary.BigEndian.Uint16(alive) == 1,
	}
	for i := range r.BusV {
		r.BusV[i] = float64(u32(38+2*i)) / 10
	}
	return r, nil
}

// values are the tick keys of a fresh reading.
func (r encombiReading) values() map[string]float64 {
	v := map[string]float64{
		keyEncombiDI:  float64(r.DIBits),
		keyGenPowerKw: round3(r.GenPowerKw),
		keyGenAlive:   0,
	}
	if r.Alive {
		v[keyGenAlive] = 1
	}
	for i, k := range busVoltageKeys {
		v[k] = round1(r.BusV[i])
	}
	return v
}

type inputReader interface {
	ReadInput(ctx context.Context, start, quantity uint16) ([]byte, error)
	Close() error
}

func readEncombi(ctx context.Context, sess inputReader) (encombiReading, error) {
	meter, err := sess.ReadInput(ctx, encombiMeterStart, encombiMeterQty)
	if err != nil {
		return encombiReading{}, fmt.Errorf("encombi %d: %w", encombiMeterStart, err)
	}
	alive, err := sess.ReadInput(ctx, encombiAliveAddr, 1)
	if err != nil {
		return encombiReading{}, fmt.Errorf("encombi %d: %w", encombiAliveAddr, err)
	}
	di, err := sess.ReadInput(ctx, encombiDIAddr, 1)
	if err != nil {
		return encombiReading{}, fmt.Errorf("encombi %d: %w", encombiDIAddr, err)
	}
	return decodeEncombi(meter, alive, di, time.Now().UTC())
}

// runEncombiPoller keeps the latest complete reading in lastEncombi.
// A failed poll drops the session and keeps the last good reading; its
// age alone makes the power mode unknown.
func (s *Service) runEncombiPoller(ctx context.Context) {
	cfg := s.cfg.Encombi
	log := s.log.With("host", cfg.Host)
	log.Info("edge_encombi_start", "interval", cfg.PollInterval.String())
	var sess inputReader
	defer func() {
		if sess != nil {
			_ = sess.Close()
		}
	}()
	failures := 0
	t := time.NewTicker(cfg.PollInterval)
	defer t.Stop()
	for {
		if sess == nil {
			ns, err := modbusclient.Dial(ctx, modbusclient.DialTarget{
				Host: cfg.Host, Port: cfg.Port, UnitID: cfg.UnitID,
				ConnectTimeout: cfg.ConnectTimeout, RequestTimeout: cfg.RequestTimeout,
			})
			if err == nil {
				sess = ns
			} else if ctx.Err() == nil {
				failures++
				if failures == 1 || failures%60 == 0 {
					log.Warn("edge_encombi_dial", "err", err, "failures", failures)
				}
			}
		}
		if sess != nil {
			r, err := readEncombi(ctx, sess)
			switch {
			case err == nil:
				if failures > 0 {
					log.Info("edge_encombi_ok", "after_failures", failures)
				}
				failures = 0
				s.lastEncombi.Store(&r)
			case ctx.Err() == nil:
				failures++
				if failures == 1 || failures%60 == 0 {
					log.Warn("edge_encombi_read", "err", err, "failures", failures)
				}
				_ = sess.Close()
				sess = nil
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// encombiValues are the Encombi keys for a tick at now, nil when the
// last reading is older than stale_after.
func (s *Service) encombiValues(now time.Time) map[string]float64 {
	r := s.lastEncombi.Load()
	if r == nil || now.Sub(r.At) > s.cfg.Encombi.StaleAfter {
		return nil
	}
	return r.values()
}

// observeSupply puts a fresh Encombi reading into the tick and takes
// the power mode. No-op while encombi is off.
func (s *Service) observeSupply(ctx context.Context, now time.Time, tick *Tick) {
	if s.supply == nil {
		return
	}
	if v := s.encombiValues(now); v != nil {
		if tick.Values == nil {
			tick.Values = map[string]float64{}
		}
		for k, x := range v {
			tick.Values[k] = x
		}
	}
	in := supplyInputsOf(*tick)
	changed, swChanged, prevMode, prevSw := s.supply.observe(now, classifySupply(in, s.cfg.Encombi.BusLiveMinV), switchPosition(in))
	s.supplyNow = s.supply.state(in)
	s.supplyNow.DataAge = time.Duration(math.MaxInt64)
	if r := s.lastEncombi.Load(); r != nil {
		s.supplyNow.DataAge = now.Sub(r.At)
	}
	s.avail = s.availabilityNow(now, *tick)
	if changed {
		s.writeEventDeduped(ctx, supplyEvent(now, prevMode, s.supplyNow.Mode, s.supplyNow))
	}
	if swChanged && prevSw != SwitchUnknown && s.supplyNow.Switch != SwitchUnknown {
		s.writeEventDeduped(ctx, switchEvent(now, prevSw, s.supplyNow.Switch))
	}
}

// publishSupply builds the status line after this tick's write step.
func (s *Service) publishSupply(tick Tick) *SupplyStatus {
	if s.supply == nil {
		return nil
	}
	st := buildSupplyStatus(s.supplyNow, tick, s.controllerNow(), s.cfg, s.essCapacityKwh(tick))
	if s.avail != nil {
		st.Restriction, st.Actions = s.avail.Restriction, s.avail
	}
	s.supplyView.Store(st)
	return st
}

// supplyInputs are the controller's view of the power mode; nil while
// encombi is off.
func (s *Service) supplyInputs() (*SupplyState, *Availability) {
	if s.supply == nil {
		return nil, nil
	}
	st := s.supplyNow
	return &st, s.avail
}

// controllerNow is «Хто керує»: the IoT2050 while its flag is on and no
// stop holds the battery at 0, nobody under such a stop, Encombi while
// the flag is off.
func (s *Service) controllerNow() string {
	if !s.gate.Enabled() {
		return ControllerEncombi
	}
	switch s.ctl.State() {
	case WriteStopped, WriteStopping:
		return ControllerNone
	}
	return ControllerEMS
}

// essCapacityKwh is the passport capacity, else the logger's rating.
func (s *Service) essCapacityKwh(t Tick) float64 {
	if c := s.cfg.Limits.Bess.PassportKwh; c > 0 {
		return c
	}
	return t.Values["ess_rated_kwh"]
}
