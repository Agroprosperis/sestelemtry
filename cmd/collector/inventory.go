package main

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nesh/sestelemetry/internal/config"
	"github.com/nesh/sestelemetry/internal/decode"
	"github.com/nesh/sestelemetry/internal/energyflow"
	"github.com/nesh/sestelemetry/internal/inventory"
	"github.com/nesh/sestelemetry/internal/modbusclient"
	"github.com/nesh/sestelemetry/internal/registers"
	"github.com/nesh/sestelemetry/internal/storage"
)

var insertPlantInventory = storage.InsertPlantInventorySnapshot

// excludeInventoryKeys drops plant-passport metrics from the high-frequency
// telemetry set so orgs with an empty metric_keys whitelist (full catalog)
// do not write rare static registers every poll_interval.
func excludeInventoryKeys(entries []registers.ResolvedEntry) []registers.ResolvedEntry {
	out := make([]registers.ResolvedEntry, 0, len(entries))
	for _, e := range entries {
		if inventory.IsInventoryMetricKey(e.MetricKey) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// runOrgInventory polls plant-passport registers on a rare schedule
// (startup, hourly, daily), merges dual-SmartLogger readings, and
// stores one site-level snapshot from controller data only.
func runOrgInventory(
	ctx context.Context,
	log *slog.Logger,
	cfg *config.Root,
	org config.Organization,
	resolvedAll []registers.ResolvedEntry,
	pool *pgxpool.Pool,
) {
	log = log.With("organization_id", org.ID, "component", "inventory")

	// Immediate startup snapshot.
	if err := pollOrgInventory(ctx, log, cfg, org, resolvedAll, pool, inventory.PollReasonStartup); err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		log.Error("inventory_poll", "reason", inventory.PollReasonStartup, "err", err)
	}

	hourly := time.NewTicker(time.Hour)
	defer hourly.Stop()
	daily := time.NewTicker(24 * time.Hour)
	defer daily.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-hourly.C:
			if err := pollOrgInventory(ctx, log, cfg, org, resolvedAll, pool, inventory.PollReasonHourly); err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				log.Error("inventory_poll", "reason", inventory.PollReasonHourly, "err", err)
			}
		case <-daily.C:
			if err := pollOrgInventory(ctx, log, cfg, org, resolvedAll, pool, inventory.PollReasonDaily); err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				log.Error("inventory_poll", "reason", inventory.PollReasonDaily, "err", err)
			}
		}
	}
}

func pollOrgInventory(
	ctx context.Context,
	log *slog.Logger,
	cfg *config.Root,
	org config.Organization,
	resolvedAll []registers.ResolvedEntry,
	pool *pgxpool.Pool,
	reason string,
) error {
	devices := org.Devices()
	readings := make([]inventory.DeviceReading, 0, len(devices))
	for _, dev := range devices {
		r := readDeviceInventory(ctx, cfg, dev, resolvedAll)
		readings = append(readings, r)
	}
	ts := time.Now().UTC()
	snap := inventory.Merge(org.ID, reason, ts, readings)
	if err := insertPlantInventory(ctx, pool, snap); err != nil {
		return err
	}
	attrs := []any{
		"reason", reason,
		"device_host", snap.DeviceHost,
		"quality_flags", snap.QualityFlags,
	}
	if snap.PVRatedKw != nil {
		attrs = append(attrs, "pv_rated_kw", *snap.PVRatedKw)
	}
	if snap.ESSRatedKw != nil {
		attrs = append(attrs, "ess_rated_kw", *snap.ESSRatedKw)
	}
	if snap.ESSRatedKwh != nil {
		attrs = append(attrs, "ess_rated_kwh", *snap.ESSRatedKwh)
	}
	if snap.ESSCount != nil {
		attrs = append(attrs, "ess_count", *snap.ESSCount)
	}
	if snap.ActivePowerControlMode != nil {
		attrs = append(attrs, "control_mode", *snap.ActivePowerControlMode)
	}
	if len(snap.QualityFlags) > 0 {
		log.Warn("inventory_ok", attrs...)
	} else {
		log.Info("inventory_ok", attrs...)
	}
	return nil
}

func readDeviceInventory(
	ctx context.Context,
	cfg *config.Root,
	dev config.ModbusDevice,
	resolvedAll []registers.ResolvedEntry,
) inventory.DeviceReading {
	host := strings.TrimSpace(dev.Host)
	role := energyflow.DetectRole(dev.MetricKeys)
	keys := inventory.KeysForRole(role)
	resolved, err := registers.Subset(resolvedAll, keys)
	if err != nil {
		return inventory.DeviceReading{Host: host, Err: err}
	}
	if len(resolved) == 0 {
		return inventory.DeviceReading{Host: host, Err: errors.New("no inventory registers for device role")}
	}
	chunks := modbusclient.PlanChunks(resolved)

	sess, err := dialFunc(ctx, dialTargetForDevice(dev))
	if err != nil {
		return inventory.DeviceReading{Host: host, Err: err}
	}
	defer func() { _ = sess.Close() }()

	readCtx, cancel := context.WithTimeout(ctx, readBudgetForPoll(dev.RequestTimeout, len(chunks)))
	defer cancel()

	data, err := readChunkData(readCtx, cfg.ModbusRegisterMap, sess, chunks)
	if err != nil {
		return inventory.DeviceReading{Host: host, Err: err}
	}

	values := make(map[string]float64, len(resolved))
	for _, e := range resolved {
		payload := payloadForEntry(e, chunks, data)
		if payload == nil {
			return inventory.DeviceReading{Host: host, Err: errors.New("missing modbus slice for " + e.MetricKey)}
		}
		v, err := decode.Scaled(e.DataType, payload, e.Gain, e.Offset)
		if err != nil {
			return inventory.DeviceReading{Host: host, Err: err}
		}
		values[e.MetricKey] = v
	}
	return inventory.ReadingFromValues(host, values)
}
