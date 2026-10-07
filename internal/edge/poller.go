package edge

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/nesh/sestelemetry/internal/decode"
	"github.com/nesh/sestelemetry/internal/modbusclient"
	"github.com/nesh/sestelemetry/internal/registers"
)

// Event codes from the MVP spec §8. Only the poller- and dispatch-level
// ones exist in MVP-0/1; Janitza/DI codes arrive with MVP-4.
const (
	EvSLPollFail      = "SL_POLL_FAIL"
	EvSLPollRecovered = "SL_POLL_RECOVERED"
	EvUplinkOffline   = "UPLINK_OFFLINE"
	EvUplinkBacklog   = "UPLINK_BACKLOG"
	EvShadowAnomaly   = "SHADOW_ANOMALY"
	EvDispatchDegrade = "DISPATCH_DEGRADED"
	EvManifestApplied = "MANIFEST_APPLIED"
	EvManifestExpired = "MANIFEST_EXPIRED"
	// Diagnostics spec §8.2:
	EvSLAlarm              = "SL_ALARM"
	EvInverterFault        = "INVERTER_FAULT"
	EvInverterRecovered    = "INVERTER_RECOVERED"
	EvSLAlarmWordsFallback = "SL_ALARM_WORDS_FALLBACK"
	EvMeterReadDisabled    = "METER_READ_DISABLED"
)

const (
	SevInfo    = "info"
	SevWarning = "warning"
	SevAlarm   = "alarm"
)

// Event is one black-box event row (spec §8).
type Event struct {
	TS       time.Time      `json:"ts"`
	Severity string         `json:"severity"`
	Code     string         `json:"code"`
	Message  string         `json:"message"`
	Context  map[string]any `json:"context,omitempty"`
}

// consecutive poll failures before an SL_POLL_FAIL event is raised;
// one failed read on a 1 s cycle is jitter, not an outage.
const pollFailThreshold = 3

// deviceReads is the per-device catalog split: required fails the
// poll, optional/slow/meter never do (ems_sl_write_cutover.md §12.2).
type deviceReads struct {
	required []registers.ResolvedEntry
	optional []registers.ResolvedEntry
	slow     []registers.ResolvedEntry
	meter    []registers.ResolvedEntry
}

// holdingReader is the Modbus surface the poller needs. *Session
// satisfies it; tests inject a fake.
type holdingReader interface {
	ReadHolding(ctx context.Context, start, quantity uint16) ([]byte, error)
	ReadHoldingUnit(ctx context.Context, unitID byte, start, quantity uint16) ([]byte, error)
}

// runDevicePoller polls one SmartLogger forever, pushing successful
// readings to `out` and outage transitions to `events`. Session
// lifecycle mirrors cmd/collector: any poll error closes the TCP
// session and the next iteration re-dials.
func runDevicePoller(
	ctx context.Context,
	log *slog.Logger,
	dev Device,
	pollInterval time.Duration,
	reads deviceReads,
	out chan<- reading,
	events chan<- Event,
) {
	log = log.With("role", string(dev.Role), "host", dev.Host)
	chunks := modbusclient.PlanChunks(reads.required)
	log.Info("edge_device_start",
		"metrics", len(reads.required), "modbus_reads", len(chunks),
		"optional", len(reads.optional), "slow", len(reads.slow),
		"meter", len(reads.meter))

	t := time.NewTicker(pollInterval)
	defer t.Stop()

	var sess *modbusclient.Session
	defer func() {
		if sess != nil {
			_ = sess.Close()
		}
	}()

	failures := 0
	failEventSent := false
	held := map[string]float64{}
	optional := append([]registers.ResolvedEntry(nil), reads.optional...)
	slow := append([]registers.ResolvedEntry(nil), reads.slow...)
	meter := append([]registers.ResolvedEntry(nil), reads.meter...)
	var nextOptional, nextSlow, nextMeter time.Time // zero = due now

	for {
		if sess == nil {
			s, err := modbusclient.Dial(ctx, modbusclient.DialTarget{
				Host:           dev.Host,
				Port:           dev.Port,
				UnitID:         dev.EffectiveUnitID(),
				ConnectTimeout: dev.ConnectTimeout,
				RequestTimeout: dev.RequestTimeout,
			})
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				failures++
				failEventSent = maybeEmitPollFail(ctx, events, dev, failures, failEventSent, err)
				log.Error("edge_modbus_dial", "err", err, "failures", failures)
				select {
				case <-ctx.Done():
					return
				case <-t.C:
				}
				continue
			}
			sess = s
		}

		values, err := pollOnce(ctx, sess, dev, reads.required, chunks)
		now := time.Now().UTC()
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			failures++
			failEventSent = maybeEmitPollFail(ctx, events, dev, failures, failEventSent, err)
			log.Error("edge_poll", "err", err, "failures", failures)
			_ = sess.Close()
			sess = nil
		} else {
			if failEventSent {
				emitEvent(ctx, events, Event{
					TS: now, Severity: SevInfo, Code: EvSLPollRecovered,
					Message: "SmartLogger poll recovered",
					Context: map[string]any{"host": dev.Host, "role": string(dev.Role), "failures": failures},
				})
			}
			failures = 0
			failEventSent = false

			if !now.Before(nextOptional) {
				var dropped []string
				var ioErr error
				optional, dropped, ioErr, err = mergeSoft(ctx, sess, dev, optional, held, nil)
				if err != nil {
					return
				}
				if alarmWordsDropped(dropped) {
					emitEvent(ctx, events, Event{
						TS: now, Severity: SevWarning, Code: EvSLAlarmWordsFallback,
						Message: "firmware refused 50006/50007 — reading six alarm words",
						Context: map[string]any{"host": dev.Host, "dropped": dropped},
					})
				}
				nextOptional = softDue(log, "optional", now, optionalReadInterval, ioErr)
			}
			if !now.Before(nextSlow) {
				var dropped []string
				var ioErr error
				slow, dropped, ioErr, err = mergeSoft(ctx, sess, dev, slow, held, nil)
				if err != nil {
					return
				}
				if len(dropped) > 0 {
					log.Warn("edge_slow_read_disabled", "dropped", dropped)
				}
				nextSlow = softDue(log, "slow", now, slowReadInterval, ioErr)
			}
			if dev.Meter != nil && len(meter) > 0 && !now.Before(nextMeter) {
				uid := byte(dev.Meter.UnitID)
				var dropped []string
				var ioErr error
				meter, dropped, ioErr, err = mergeSoft(ctx, sess, dev, meter, held, &uid)
				if err != nil {
					return
				}
				if len(dropped) > 0 {
					emitEvent(ctx, events, Event{
						TS: now, Severity: SevWarning, Code: EvMeterReadDisabled,
						Message: "meter unit refused the cutover registers — meter poll disabled",
						Context: map[string]any{"host": dev.Host, "unit_id": dev.Meter.UnitID, "dropped": dropped},
					})
				}
				nextMeter = softDue(log, "meter", now, dev.Meter.Interval, ioErr)
			}
			for k, v := range held {
				values[k] = v
			}

			select {
			case out <- reading{role: dev.Role, host: dev.Host, at: now, values: values}:
			case <-ctx.Done():
				return
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func alarmWordsDropped(keys []string) bool {
	for _, k := range keys {
		if k == "sl_alarm_7" || k == "sl_alarm_8" {
			return true
		}
	}
	return false
}

// softReadBackoff pauses a soft group after a transport error: a dead
// meter or a slow register would otherwise cost a full request timeout
// of the 1 s cycle on every attempt.
const softReadBackoff = time.Minute

// softDue is when a soft group is read next: one interval after a good
// attempt, softReadBackoff after a transport error (also for the
// hourly group, so a blip does not hide 40737 for an hour).
func softDue(log *slog.Logger, group string, now time.Time, every time.Duration, ioErr error) time.Time {
	if ioErr == nil {
		return now.Add(every)
	}
	log.Warn("edge_soft_read_io", "group", group, "err", ioErr, "retry_in", softReadBackoff.String())
	return now.Add(softReadBackoff)
}

// mergeSoft reads optional entries and stores them under
// DeviceMetricKey. A key holds a value only while its latest attempt
// succeeded: refused (exception) and failed (I/O) keys are removed, so
// nothing stale reaches the tick. Exception keys are also dropped from
// the next cycle. Context cancel is the only fatal return.
func mergeSoft(
	ctx context.Context,
	sess holdingReader,
	dev Device,
	entries []registers.ResolvedEntry,
	held map[string]float64,
	unitID *byte,
) (next []registers.ResolvedEntry, dropped []string, ioErr error, err error) {
	if len(entries) == 0 {
		return entries, nil, nil, nil
	}
	got, remaining, dropped, ioErr, err := pollSoft(ctx, sess, dev.RequestTimeout, entries, unitID)
	if err != nil {
		return entries, nil, nil, err
	}
	for _, e := range entries {
		key := DeviceMetricKey(dev.Role, e.MetricKey)
		if v, ok := got[e.MetricKey]; ok {
			held[key] = v
		} else {
			delete(held, key)
		}
	}
	return remaining, dropped, ioErr, nil
}

func pollOnce(
	ctx context.Context,
	sess *modbusclient.Session,
	dev Device,
	entries []registers.ResolvedEntry,
	chunks []modbusclient.ReadChunk,
) (map[string]float64, error) {
	budget := dev.RequestTimeout * time.Duration(len(chunks)+1)
	readCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	data := make(map[uint16][]byte, len(chunks))
	for _, ch := range chunks {
		b, err := sess.ReadHolding(readCtx, ch.Start, ch.Quantity)
		if err != nil {
			return nil, err
		}
		data[ch.Start] = b
	}

	values := make(map[string]float64, len(entries))
	for _, e := range entries {
		payload := slicePayload(e, chunks, data)
		if payload == nil {
			return nil, errors.New("edge: missing modbus slice for " + e.MetricKey)
		}
		v, err := decode.Scaled(e.DataType, payload, e.Gain, e.Offset)
		if err != nil {
			return nil, err
		}
		values[e.MetricKey] = v
	}
	return values, nil
}

// pollSoft reads each chunk independently. A Modbus exception drops
// that chunk's keys for good (firmware refused them). The first
// transport error stops the group for this cycle and is returned as
// ioErr, so one dead unit costs at most one request timeout. Context
// cancel is the only fatal error.
func pollSoft(
	ctx context.Context,
	sess holdingReader,
	timeout time.Duration,
	entries []registers.ResolvedEntry,
	unitID *byte,
) (values map[string]float64, remaining []registers.ResolvedEntry, dropped []string, ioErr error, err error) {
	chunks := modbusclient.PlanChunks(entries)
	budget := timeout * time.Duration(len(chunks)+1)
	if budget < timeout {
		budget = timeout
	}
	readCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	data := make(map[uint16][]byte, len(chunks))
	disabled := map[string]bool{}
	for _, ch := range chunks {
		var (
			b       []byte
			readErr error
		)
		if unitID != nil {
			b, readErr = sess.ReadHoldingUnit(readCtx, *unitID, ch.Start, ch.Quantity)
		} else {
			b, readErr = sess.ReadHolding(readCtx, ch.Start, ch.Quantity)
		}
		if readErr != nil {
			if ctx.Err() != nil {
				return nil, entries, nil, nil, ctx.Err()
			}
			if _, ok := modbusclient.ExceptionCode(readErr); ok {
				for _, e := range entriesInChunk(entries, ch) {
					disabled[e.MetricKey] = true
					dropped = append(dropped, e.MetricKey)
				}
				continue
			}
			ioErr = readErr
			break
		}
		data[ch.Start] = b
	}

	values = make(map[string]float64, len(entries))
	remaining = make([]registers.ResolvedEntry, 0, len(entries))
	for _, e := range entries {
		if disabled[e.MetricKey] {
			continue
		}
		remaining = append(remaining, e)
		payload := slicePayload(e, chunks, data)
		if payload == nil {
			continue
		}
		v, decErr := decode.Scaled(e.DataType, payload, e.Gain, e.Offset)
		if decErr != nil {
			continue
		}
		values[e.MetricKey] = v
	}
	return values, remaining, dropped, ioErr, nil
}

func entriesInChunk(entries []registers.ResolvedEntry, ch modbusclient.ReadChunk) []registers.ResolvedEntry {
	last := ch.Start + ch.Quantity - 1
	var out []registers.ResolvedEntry
	for _, e := range entries {
		if e.PDUStart >= ch.Start && e.PDUEnd <= last {
			out = append(out, e)
		}
	}
	return out
}

func slicePayload(e registers.ResolvedEntry, chunks []modbusclient.ReadChunk, data map[uint16][]byte) []byte {
	for _, ch := range chunks {
		last := ch.Start + ch.Quantity - 1
		if e.PDUStart < ch.Start || e.PDUEnd > last {
			continue
		}
		raw, ok := data[ch.Start]
		if !ok {
			continue
		}
		return modbusclient.SliceForEntry(ch.Start, raw, e)
	}
	return nil
}

func maybeEmitPollFail(ctx context.Context, events chan<- Event, dev Device, failures int, alreadySent bool, err error) bool {
	if alreadySent || failures < pollFailThreshold {
		return alreadySent
	}
	emitEvent(ctx, events, Event{
		TS: time.Now().UTC(), Severity: SevWarning, Code: EvSLPollFail,
		Message: "SmartLogger poll failing: " + err.Error(),
		Context: map[string]any{"host": dev.Host, "role": string(dev.Role), "failures": failures},
	})
	return true
}

func emitEvent(ctx context.Context, events chan<- Event, ev Event) {
	select {
	case events <- ev:
	case <-ctx.Done():
	}
}
