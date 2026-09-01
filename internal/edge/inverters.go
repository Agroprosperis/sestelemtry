package edge

import (
	"context"
	"encoding/binary"
	"fmt"
	"sort"
	"time"

	"github.com/nesh/sestelemetry/internal/modbusclient"
)

// Inverter fleet diagnostics: the slow remapped-51xxx poll of the PV
// SmartLogger (diagnostics spec §6). Runs on its own Modbus session so
// a 12-block sweep with timeouts can never stall the 1 s telemetry
// poll. Read-only, ~30 s cadence — the same blocks Encombi reads every
// ~23 s per the ze PCAP.

// Inverter status classes (spec §6.2, SUN2000 status high byte).
const (
	InvOnGrid      = "on_grid"
	InvStarting    = "starting"
	InvStandby     = "standby"
	InvFault       = "fault"
	InvShutdown    = "shutdown"
	InvUnknown     = "unknown"
	InvUnreachable = "unreachable"
)

// inverterBlockQty is the remapped per-inverter block size.
const inverterBlockQty = 25

// inverterRegisterBase maps an RS-485 device address to the start of
// its remapped block (Issue 52 / encombi §3.1).
func inverterRegisterBase(addr int) int { return 51000 + inverterBlockQty*(addr-1) }

// InverterSnapshot is one row of health.inverters[] (spec §6.3
// contract). Numeric fields are nil when the block was unreachable;
// the row itself always stays in the array.
type InverterSnapshot struct {
	DeviceAddress  int       `json:"device_address"`
	RegisterBase   int       `json:"register_base"`
	Label          string    `json:"label,omitempty"`
	Class          string    `json:"class"`
	StatusRaw      string    `json:"status_raw,omitempty"`
	StatusLabel    string    `json:"status_label"`
	PKw            *float64  `json:"p_kw"`
	QKvar          *float64  `json:"q_kvar"`
	PDcKw          *float64  `json:"p_dc_kw"`
	IDcA           *float64  `json:"i_dc_a"`
	Pf             *float64  `json:"pf"`
	InsulationMohm *float64  `json:"insulation_mohm"`
	TempC          *float64  `json:"temp_c"`
	MajorFault     string    `json:"major_fault,omitempty"`
	MinorFault     string    `json:"minor_fault,omitempty"`
	Warning        string    `json:"warning,omitempty"`
	PollOK         bool      `json:"poll_ok"`
	PollError      *string   `json:"poll_error"`
	TS             time.Time `json:"ts"`
}

// inverterFleet is the latest full sweep; length always equals
// len(diagnostics.inverters.device_addresses).
type inverterFleet struct {
	TS        time.Time          `json:"ts"`
	Inverters []InverterSnapshot `json:"inverters"`
}

// decodeInverterBlock maps the 25-register payload onto a snapshot
// (offsets per encombi §3.1; all words big-endian like the rest of the
// SmartLogger northbound map).
func decodeInverterBlock(addr int, label string, raw []byte, ts time.Time) InverterSnapshot {
	u16 := func(off int) uint16 { return binary.BigEndian.Uint16(raw[off*2:]) }
	u32 := func(off int) uint32 { return binary.BigEndian.Uint32(raw[off*2:]) }
	i16 := func(off int) int16 { return int16(u16(off)) }
	i32 := func(off int) int32 { return int32(u32(off)) }

	status := u16(9)
	major := u32(12)
	class, statusLabel := classifyInverter(status, major)

	return InverterSnapshot{
		DeviceAddress:  addr,
		RegisterBase:   inverterRegisterBase(addr),
		Label:          label,
		Class:          class,
		StatusRaw:      fmt.Sprintf("0x%04x", status),
		StatusLabel:    statusLabel,
		PKw:            f64ptr(float64(i32(0)) / 1000),
		QKvar:          f64ptr(float64(i32(2)) / 1000),
		IDcA:           f64ptr(float64(i16(4)) / 100),
		PDcKw:          f64ptr(float64(u32(5)) / 1000),
		InsulationMohm: f64ptr(float64(u16(7)) / 1000),
		Pf:             f64ptr(float64(i16(8)) / 1000),
		TempC:          f64ptr(float64(i16(11)) / 10),
		MajorFault:     hexU32(major),
		MinorFault:     hexU32(u32(14)),
		Warning:        hexU32(u32(16)),
		PollOK:         true,
		TS:             ts,
	}
}

// classifyInverter reduces the SUN2000 status enum to the UI classes
// of spec §6.2. A non-zero major fault code wins over the status word.
func classifyInverter(status uint16, major uint32) (class, label string) {
	if major != 0 || status == 0x0300 {
		return InvFault, "аварія"
	}
	switch status >> 8 {
	case 0x02:
		return InvOnGrid, "у мережі"
	case 0x01:
		return InvStarting, "пуск"
	case 0x00:
		return InvStandby, "standby"
	case 0x07:
		return InvStandby, "standby (немає опромінення)"
	case 0xa0:
		// Live ze firmware reports 0xA000 at night ("Standby: no
		// irradiation") — seen on 10/12 units, §10.6 night run.
		return InvStandby, "standby (ніч)"
	case 0x03:
		return InvShutdown, "вимкнено"
	default:
		return InvUnknown, fmt.Sprintf("0x%04x", status)
	}
}

func hexU32(v uint32) string {
	if v == 0 {
		return "0x0"
	}
	return fmt.Sprintf("0x%x", v)
}

func unreachableSnapshot(addr int, label string, err error, ts time.Time) InverterSnapshot {
	msg := err.Error()
	return InverterSnapshot{
		DeviceAddress: addr,
		RegisterBase:  inverterRegisterBase(addr),
		Label:         label,
		Class:         InvUnreachable,
		StatusLabel:   "без зв'язку",
		PollError:     &msg,
		TS:            ts,
	}
}

// pvDiagDevice picks the SmartLogger that carries the remapped 51xxx
// map: the PV box on dual sites, the single box otherwise.
func pvDiagDevice(cfg *Config) (Device, bool) {
	var fallback *Device
	for i := range cfg.SmartLogger.Devices {
		d := &cfg.SmartLogger.Devices[i]
		if d.Role == RolePV {
			return *d, true
		}
		if d.Role == RoleAll {
			fallback = d
		}
	}
	if fallback != nil {
		return *fallback, true
	}
	return Device{}, false
}

// runInverterPoller sweeps every configured inverter block on the
// diagnostics interval and publishes the fleet snapshot for the local
// console and the health heartbeat. INVERTER_FAULT / _RECOVERED fire
// only on class transitions, never on every sweep.
func (s *Service) runInverterPoller(ctx context.Context, events chan<- Event) {
	inv := s.cfg.Diagnostics.Inverters
	addrs := append([]int{}, inv.DeviceAddresses...)
	sort.Ints(addrs)
	dev, ok := pvDiagDevice(s.cfg)
	if !ok {
		s.log.Warn("edge_inverters_no_pv_device")
		return
	}
	log := s.log.With("host", dev.Host, "inverters", len(addrs))
	log.Info("edge_inverters_start", "interval", inv.PollInterval.String())

	var sess *modbusclient.Session
	defer func() {
		if sess != nil {
			_ = sess.Close()
		}
	}()

	prevClass := map[int]string{}
	t := time.NewTicker(inv.PollInterval)
	defer t.Stop()
	for {
		sess = s.sweepInverters(ctx, sess, dev, addrs, inv.Labels, prevClass, events)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// sweepInverters polls every block once, stores the fleet snapshot and
// emits transition events. Returns the (possibly re-dialed or dropped)
// session for the next sweep.
func (s *Service) sweepInverters(
	ctx context.Context,
	sess *modbusclient.Session,
	dev Device,
	addrs []int,
	labels map[int]string,
	prevClass map[int]string,
	events chan<- Event,
) *modbusclient.Session {
	// The whole sweep must finish well inside one poll interval even
	// with per-read timeouts on a dead link.
	sweepCtx, cancel := context.WithTimeout(ctx, s.cfg.Diagnostics.Inverters.PollInterval)
	defer cancel()

	var dialErr error
	rows := make([]InverterSnapshot, 0, len(addrs))
	for _, addr := range addrs {
		now := time.Now().UTC()
		if sess == nil {
			if dialErr != nil {
				// Dial already failed this sweep — don't hammer a dead
				// host once per address.
				rows = append(rows, unreachableSnapshot(addr, labels[addr], dialErr, now))
				continue
			}
			s2, err := modbusclient.Dial(sweepCtx, modbusclient.DialTarget{
				Host:           dev.Host,
				Port:           dev.Port,
				UnitID:         dev.EffectiveUnitID(),
				ConnectTimeout: dev.ConnectTimeout,
				RequestTimeout: dev.RequestTimeout,
			})
			if err != nil {
				dialErr = err
				rows = append(rows, unreachableSnapshot(addr, labels[addr], err, now))
				continue
			}
			sess = s2
		}
		base := inverterRegisterBase(addr)
		raw, err := sess.ReadHolding(sweepCtx, uint16(base), inverterBlockQty)
		if err != nil || len(raw) < inverterBlockQty*2 {
			if err == nil {
				err = fmt.Errorf("short frame: %d bytes", len(raw))
			}
			rows = append(rows, unreachableSnapshot(addr, labels[addr], err, now))
			// A failed read may be a per-slave gateway timeout or a dead
			// TCP session; drop the session so the next address re-dials.
			_ = sess.Close()
			sess = nil
			continue
		}
		rows = append(rows, decodeInverterBlock(addr, labels[addr], raw, now))
	}

	s.lastInverters.Store(&inverterFleet{TS: time.Now().UTC(), Inverters: rows})
	s.emitInverterTransitions(ctx, rows, prevClass, events)
	return sess
}

// emitInverterTransitions raises INVERTER_FAULT when a unit enters
// fault/unreachable and INVERTER_RECOVERED when it leaves (spec §6.2).
func (s *Service) emitInverterTransitions(ctx context.Context, rows []InverterSnapshot, prevClass map[int]string, events chan<- Event) {
	bad := func(class string) bool { return class == InvFault || class == InvUnreachable }
	for _, r := range rows {
		prev, seen := prevClass[r.DeviceAddress]
		prevClass[r.DeviceAddress] = r.Class
		name := r.Label
		if name == "" {
			name = fmt.Sprintf("addr %d", r.DeviceAddress)
		}
		switch {
		case bad(r.Class) && (!seen || !bad(prev)):
			msg := fmt.Sprintf("inverter %s: %s", name, r.Class)
			if r.Class == InvFault {
				msg += fmt.Sprintf(" (status %s, major %s, minor %s)", r.StatusRaw, r.MajorFault, r.MinorFault)
			}
			ctxMap := map[string]any{"device_address": r.DeviceAddress, "class": r.Class}
			if r.PollError != nil {
				ctxMap["poll_error"] = *r.PollError
			}
			emitEvent(ctx, events, Event{
				TS: r.TS, Severity: SevAlarm, Code: EvInverterFault,
				Message: msg, Context: ctxMap,
			})
		case seen && bad(prev) && !bad(r.Class):
			emitEvent(ctx, events, Event{
				TS: r.TS, Severity: SevInfo, Code: EvInverterRecovered,
				Message: fmt.Sprintf("inverter %s recovered: %s", name, r.StatusLabel),
				Context: map[string]any{"device_address": r.DeviceAddress, "class": r.Class},
			})
		}
	}
}
