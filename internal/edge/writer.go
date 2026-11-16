package edge

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/nesh/sestelemetry/internal/modbusclient"
)

// writeReg is one SmartLogger register this build writes
// (ems_sl_write_cutover.md §12.1). Nothing else is ever written: not
// 40197/40201 (inverter start/stop), not 40380, not 44365/44376.
type writeReg struct {
	Addr   uint16
	Name   string
	Role   DeviceRole // the logger it is written to
	Signed bool
	Scale  float64 // raw = kW × Scale
	// Active is the active-power setpoint of its logger; the reactive
	// ones are always 0.
	Active bool
}

var (
	regPVLimit  = writeReg{Addr: 40378, Name: "40378", Role: RolePV, Signed: false, Scale: 10, Active: true}
	regESSPower = writeReg{Addr: 40381, Name: "40381", Role: RoleESS, Signed: true, Scale: 10, Active: true}
	regPVQ      = writeReg{Addr: 40384, Name: "40384", Role: RolePV, Signed: true, Scale: 10}
	regESSQ     = writeReg{Addr: 40386, Name: "40386", Role: RoleESS, Signed: true, Scale: 10}
	writeRegs   = []writeReg{regPVLimit, regPVQ, regESSPower, regESSQ}
)

var (
	errWriteOff     = errors.New("запис вимкнено")
	errNotWritable  = errors.New("регістр не дозволений до запису")
	errReadbackDiff = errors.New("readback не збігся із записаним")
)

func writableAddr(addr uint16) bool {
	for _, r := range writeRegs {
		if r.Addr == addr {
			return true
		}
	}
	return false
}

// encode renders kW as the register's two big-endian words.
func (r writeReg) encode(kw float64) ([]byte, int64, error) {
	if math.IsNaN(kw) || math.IsInf(kw, 0) {
		return nil, 0, fmt.Errorf("%s: value %v", r.Name, kw)
	}
	raw := int64(math.Round(kw * r.Scale))
	b := make([]byte, 4)
	if r.Signed {
		if raw < math.MinInt32 || raw > math.MaxInt32 {
			return nil, 0, fmt.Errorf("%s: %v kW out of range", r.Name, kw)
		}
		binary.BigEndian.PutUint32(b, uint32(int32(raw)))
	} else {
		if raw < 0 || raw > math.MaxUint32 {
			return nil, 0, fmt.Errorf("%s: %v kW out of range", r.Name, kw)
		}
		binary.BigEndian.PutUint32(b, uint32(raw))
	}
	return b, raw, nil
}

func (r writeReg) decode(b []byte) (int64, error) {
	if len(b) < 4 {
		return 0, fmt.Errorf("%s: readback %d bytes", r.Name, len(b))
	}
	u := binary.BigEndian.Uint32(b)
	if r.Signed {
		return int64(int32(u)), nil
	}
	return int64(u), nil
}

// valueFor is the command's value for reg; false = nothing to write.
func (c WriteCommand) valueFor(reg writeReg) (float64, bool) {
	switch reg.Addr {
	case regESSPower.Addr:
		return c.ESSKw, true
	case regPVLimit.Addr:
		return c.PVLimitKw, c.PVKnown
	default:
		return 0, true
	}
}

// regsFor lists what a logger gets: on a dual site each its own pair,
// on a single site all four.
func regsFor(role DeviceRole) []writeReg {
	if role == RoleAll {
		return writeRegs
	}
	var out []writeReg
	for _, r := range writeRegs {
		if r.Role == role {
			out = append(out, r)
		}
	}
	return out
}

// registerSession is the Modbus surface of a writer. *modbusclient
// .Session satisfies it; tests inject a fake.
type registerSession interface {
	WriteMultiple(ctx context.Context, start uint16, payload []byte) error
	WriteSingle(ctx context.Context, addr, value uint16) error
	ReadHolding(ctx context.Context, start, quantity uint16) ([]byte, error)
	Close() error
}

type dialFunc func(ctx context.Context, dev Device) (registerSession, error)

func dialModbus(ctx context.Context, dev Device) (registerSession, error) {
	return modbusclient.Dial(ctx, modbusclient.DialTarget{
		Host:           dev.Host,
		Port:           dev.Port,
		UnitID:         dev.EffectiveUnitID(),
		ConnectTimeout: dev.ConnectTimeout,
		RequestTimeout: dev.RequestTimeout,
	})
}

// WriteFeedback is the last write of one register, for the journal and
// the consoles.
type WriteFeedback struct {
	Register   string    `json:"register"`
	Host       string    `json:"host"`
	At         time.Time `json:"at"`
	Kw         float64   `json:"kw"`
	Raw        int64     `json:"raw"`
	ReadbackKw *float64  `json:"readback_kw,omitempty"`
	OK         bool      `json:"ok"`
	Err        string    `json:"error,omitempty"`
	// Tolerated: the PV logger refused with exception 4 while the sun is
	// not in operation, the expected answer (handoff §9.1 п. 2).
	Tolerated bool `json:"tolerated,omitempty"`
}

type writeFault struct {
	at  time.Time
	msg string
}

// guardedWrite is the only call of a SmartLogger write. The flag is
// checked here, right before the bytes go out, whoever asked.
func (s *Service) guardedWrite(ctx context.Context, w registerSession, reg writeReg, payload []byte) error {
	if !s.gate.Enabled() {
		return errWriteOff
	}
	if !writableAddr(reg.Addr) {
		return errNotWritable
	}
	return w.WriteMultiple(ctx, reg.Addr, payload)
}

// guardedWriteSingle is the only call of an FC06: the flag is checked
// right before, and only 0 into 40196…40201 goes out.
func (s *Service) guardedWriteSingle(ctx context.Context, w registerSession, addr, value uint16) error {
	if !s.gate.Enabled() {
		return errWriteOff
	}
	if !oneShotAddr(addr) || value != 0 {
		return errNotWritable
	}
	return w.WriteSingle(ctx, addr, value)
}

// pvTolerated reports a PV logger refusal that is the expected answer:
// exception 4 while the sun is not in operation (at night, or stopped
// before the hand-over). The battery logger's refusals never are.
func (s *Service) pvTolerated(reg writeReg, err error) bool {
	if reg.Role != RolePV {
		return false
	}
	if code, ok := modbusclient.ExceptionCode(err); !ok || code != 4 {
		return false
	}
	t := s.lastTick.Load()
	return t == nil || !pvRunningOn(*t)
}

// writeOne writes one value and reads the same register back.
func (s *Service) writeOne(ctx context.Context, sess registerSession, dev Device, reg writeReg, kw float64) WriteFeedback {
	fb := WriteFeedback{Register: reg.Name, Host: dev.Host, At: time.Now().UTC(), Kw: kw}
	payload, raw, err := reg.encode(kw)
	if err != nil {
		fb.Err = err.Error()
		return fb
	}
	fb.Raw = raw
	wctx, cancel := context.WithTimeout(ctx, dev.RequestTimeout)
	defer cancel()
	if err := s.guardedWrite(wctx, sess, reg, payload); err != nil {
		fb.Err = err.Error()
		if s.pvTolerated(reg, err) {
			fb.Err, fb.Tolerated = "exception 4: СЕС не в роботі", true
		}
		return fb
	}
	b, err := sess.ReadHolding(wctx, reg.Addr, 2)
	if err != nil {
		fb.Err = "readback: " + err.Error()
		return fb
	}
	got, err := reg.decode(b)
	if err != nil {
		fb.Err = err.Error()
		return fb
	}
	rb := float64(got) / reg.Scale
	fb.ReadbackKw = &rb
	if got != raw {
		fb.Err = fmt.Sprintf("%s: записано %d, прочитано %d", errReadbackDiff, raw, got)
		return fb
	}
	fb.OK = true
	return fb
}

// runWriter rewrites one logger's setpoints every `every` while the flag
// is on (cutover §12.1: СЕС ~2 с, УЗЕ ~3 с), each followed by a
// readback. With the flag off it holds no connection at all. A failed
// write or a readback that differs is reported to the core loop, which
// puts the battery to 0 and turns writing off; it is not retried here.
func (s *Service) runWriter(ctx context.Context, dev Device, regs []writeReg, every time.Duration, dial dialFunc, jobs <-chan oneShotJob) {
	t := time.NewTicker(every)
	defer t.Stop()
	var sess registerSession
	var lastWrite time.Time
	closeSess := func() {
		if sess != nil {
			_ = sess.Close()
			sess = nil
		}
	}
	defer closeSess()
	ensure := func() bool {
		if sess != nil {
			return true
		}
		ns, err := dial(ctx, dev)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				s.log.Error("edge_write_dial", "host", dev.Host, "err", err)
				for _, reg := range regs {
					s.storeFeedback(WriteFeedback{Register: reg.Name, Host: dev.Host, At: time.Now().UTC(), Err: "dial: " + err.Error()})
				}
			}
			return false
		}
		sess = ns
		return true
	}
	// Huawei takes a new command at most every 200 ms.
	space := func() bool {
		if wait := 250*time.Millisecond - time.Since(lastWrite); wait > 0 {
			select {
			case <-ctx.Done():
				return false
			case <-time.After(wait):
			}
		}
		return true
	}
	for {
		var job *oneShotJob
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case j := <-jobs:
			job = &j
		}
		if job != nil {
			res := cmdResult{id: job.id, err: errWriteOff}
			if s.gate.Enabled() && ensure() && space() {
				wctx, cancel := context.WithTimeout(ctx, dev.RequestTimeout)
				res.err = s.guardedWriteSingle(wctx, sess, job.addr, 0)
				cancel()
				lastWrite = time.Now()
				if res.err != nil {
					if _, refused := modbusclient.ExceptionCode(res.err); !refused {
						closeSess()
					}
				}
			} else if s.gate.Enabled() {
				res.err = errors.New("немає зв'язку з логером " + dev.Host)
			}
			res.at = time.Now().UTC()
			s.reportCommandResult(res)
			continue
		}
		if !s.gate.Enabled() {
			closeSess()
			continue
		}
		cmd := s.currentWriteCommand(time.Now().UTC())
		if !ensure() {
			if errors.Is(ctx.Err(), context.Canceled) {
				return
			}
			continue
		}
		for _, reg := range regs {
			kw, ok := cmd.valueFor(reg)
			if !ok {
				continue
			}
			if !space() {
				return
			}
			fb := s.writeOne(ctx, sess, dev, reg, kw)
			lastWrite = time.Now()
			s.storeFeedback(fb)
			if reg.Role == RolePV {
				s.notePVAnswer(fb)
			}
			if fb.OK || fb.Tolerated {
				continue
			}
			if errors.Is(ctx.Err(), context.Canceled) {
				return
			}
			if fb.Err == errWriteOff.Error() {
				break
			}
			s.reportWriteFault(writeFault{at: fb.At, msg: fmt.Sprintf("%s на %s: %s", reg.Name, dev.Host, fb.Err)})
			closeSess()
			break
		}
	}
}

// notePVAnswer logs the PV logger going from accepting to refusing (the
// sun went to standby) and back, once per transition.
func (s *Service) notePVAnswer(fb WriteFeedback) {
	switch {
	case fb.Tolerated && !s.pvRefusing.Swap(true):
		s.pushEvent(Event{TS: fb.At, Severity: SevInfo, Code: EvPVWriteRefused,
			Message: "логер СЕС не приймає " + fb.Register + " (exception 4): СЕС не в роботі — запис триває, УЗЕ не зупиняється",
			Context: map[string]any{"register": fb.Register, "host": fb.Host}})
	case fb.OK && s.pvRefusing.Swap(false):
		s.pushEvent(Event{TS: fb.At, Severity: SevInfo, Code: EvPVWriteAccepted,
			Message: "логер СЕС знову приймає " + fb.Register,
			Context: map[string]any{"register": fb.Register, "host": fb.Host}})
	}
}

func (s *Service) reportCommandResult(r cmdResult) {
	select {
	case s.cmdResults <- r:
	default:
		s.log.Error("edge_command_result_dropped", "command", r.id)
	}
}

func (s *Service) storeFeedback(fb WriteFeedback) {
	s.writeFB.Store(fb.Register, fb)
}

func (s *Service) feedback(reg writeReg) (WriteFeedback, bool) {
	v, ok := s.writeFB.Load(reg.Name)
	if !ok {
		return WriteFeedback{}, false
	}
	return v.(WriteFeedback), true
}

func (s *Service) reportWriteFault(f writeFault) {
	select {
	case s.writeFaults <- f:
	default:
	}
}

// currentWriteCommand is the core loop's latest command, or the safe
// values when it is missing or stale (the loop stalled): battery 0, PV
// at the last value that went out.
func (s *Service) currentWriteCommand(now time.Time) WriteCommand {
	if c := s.writeCmd.Load(); c != nil && now.Sub(c.At) <= commandStaleAfter {
		return *c
	}
	safe := WriteCommand{At: now, State: WriteStopped, ESSKw: 0, Reason: "команда застаріла"}
	if fb, ok := s.feedback(regPVLimit); ok && fb.OK {
		safe.PVLimitKw, safe.PVKnown = fb.Kw, true
	}
	return safe
}

// configuredWriteRegs lists the registers this site's loggers get.
func (s *Service) configuredWriteRegs() []writeReg {
	var out []writeReg
	for _, d := range s.cfg.SmartLogger.Devices {
		out = append(out, regsFor(d.Role)...)
	}
	return out
}

// stoppingConfirmed reports that every logger took the safe values after
// turning off began: the battery written 0 and read back, PV read back.
func (s *Service) stoppingConfirmed(since time.Time) bool {
	for _, reg := range s.configuredWriteRegs() {
		if reg.Addr == regPVLimit.Addr {
			if c := s.writeCmd.Load(); c != nil && !c.PVKnown {
				continue
			}
		}
		fb, ok := s.feedback(reg)
		if !ok || !(fb.OK || fb.Tolerated) || fb.At.Before(since) {
			return false
		}
		if reg.Addr == regESSPower.Addr && fb.Kw != 0 {
			return false
		}
	}
	return true
}
