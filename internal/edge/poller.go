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
	EvSLAlarm           = "SL_ALARM"
	EvInverterFault     = "INVERTER_FAULT"
	EvInverterRecovered = "INVERTER_RECOVERED"
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

// runDevicePoller polls one SmartLogger forever, pushing successful
// readings to `out` and outage transitions to `events`. Session
// lifecycle mirrors cmd/collector: any poll error closes the TCP
// session and the next iteration re-dials.
func runDevicePoller(
	ctx context.Context,
	log *slog.Logger,
	dev Device,
	pollInterval time.Duration,
	entries []registers.ResolvedEntry,
	out chan<- reading,
	events chan<- Event,
) {
	log = log.With("role", string(dev.Role), "host", dev.Host)
	chunks := modbusclient.PlanChunks(entries)
	log.Info("edge_device_start", "metrics", len(entries), "modbus_reads", len(chunks))

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

		values, err := pollOnce(ctx, sess, dev, entries, chunks)
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
