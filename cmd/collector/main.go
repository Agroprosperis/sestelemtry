package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nesh/sestelemetry/internal/bootstrap"
	"github.com/nesh/sestelemetry/internal/config"
	"github.com/nesh/sestelemetry/internal/decode"
	"github.com/nesh/sestelemetry/internal/energyflow"
	"github.com/nesh/sestelemetry/internal/modbusclient"
	"github.com/nesh/sestelemetry/internal/registers"
	"github.com/nesh/sestelemetry/internal/storage"
)

type modbusReader interface {
	ReadHolding(ctx context.Context, start, quantity uint16) ([]byte, error)
	ReadInput(ctx context.Context, start, quantity uint16) ([]byte, error)
}

var insertSamples = storage.InsertSamples

func main() {
	configPath := flag.String("config", "config.yaml", "path to YAML config")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	runtime, err := bootstrap.Load(*configPath)
	if err != nil {
		log.Error("bootstrap", "err", err)
		os.Exit(1)
	}
	cfg := runtime.Config
	resolved := runtime.Resolved
	bootstrap.ApplyDatabaseURLEnv(cfg)
	if cfg.DatabaseURL == "" {
		log.Error("database_url missing", "hint", "set in config YAML or DATABASE_URL")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := storage.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("db_open", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := storage.InitSchema(ctx, pool); err != nil {
		log.Error("db_schema", "err", err)
		os.Exit(1)
	}
	if err := storage.InitContinuousAggregates(ctx, pool); err != nil {
		// Non-fatal: API still works against the raw hypertable, just
		// slower for month/year presets. Surface the error so it shows up
		// in logs and Watchtower-style monitors can flag it.
		log.Warn("db_caggs", "err", err)
	}

	log.Info("collector_start", "orgs", len(cfg.Organizations), "metrics", len(resolved))

	var wg sync.WaitGroup
	for _, org := range cfg.Organizations {
		org := org
		wg.Add(1)
		go func() {
			defer wg.Done()
			runOrg(ctx, log, cfg, org, resolved, pool)
		}()
	}
	wg.Wait()
	log.Info("collector_stop")
}

// dialFunc is a package-level seam so tests can stub Dial without spinning
// up a real Modbus listener.
var dialFunc = modbusclient.Dial

// runOrg spawns one goroutine per Modbus device declared on the organization.
// Single-device orgs (legacy `modbus:` block) get a one-element device slice
// from `org.Devices()`, so the call site stays identical for both shapes.
//
// In addition to the per-device pollers, runOrg starts one
// energyflow.Aggregator per organization when the device whitelist
// covers both the PV and ESS accumulators. The aggregator runs on
// its own goroutine, ticks every allocation_window_seconds, and
// emits four cumulative-counter samples (pv_to_ess_kwh, …) into
// telemetry_samples each tick.
func runOrg(
	ctx context.Context,
	log *slog.Logger,
	cfg *config.Root,
	org config.Organization,
	resolved []registers.ResolvedEntry,
	pool *pgxpool.Pool,
) {
	agg := buildEnergyFlowAggregator(ctx, log, org, pool)
	var aggDone chan struct{}
	if agg != nil {
		aggDone = make(chan struct{})
		go func() {
			defer close(aggDone)
			agg.Run(ctx)
		}()
	}

	devices := org.Devices()
	roles := make([]energyflow.Role, len(devices))
	for i, dev := range devices {
		roles[i] = energyflow.DetectRole(dev.MetricKeys)
	}

	var wg sync.WaitGroup
	for i, dev := range devices {
		dev := dev
		i := i
		role := roles[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			runDevice(ctx, log, cfg, org, dev, i, resolved, pool, agg, role)
		}()
	}
	wg.Wait()
	if aggDone != nil {
		<-aggDone
	}
}

// runDevice is the per-device polling loop. It filters the global resolved
// catalog down to the device's `metric_keys` whitelist, plans its own
// Modbus read chunks, and maintains a single TCP session to the device.
// Each device is otherwise independent: dial failures, backoff, and
// per-poll timeouts are scoped to that device.
func runDevice(
	ctx context.Context,
	log *slog.Logger,
	cfg *config.Root,
	org config.Organization,
	dev config.ModbusDevice,
	devIndex int,
	resolvedAll []registers.ResolvedEntry,
	pool *pgxpool.Pool,
	agg *energyflow.Aggregator,
	role energyflow.Role,
) {
	log = log.With(
		"organization_id", org.ID,
		"device_host", dev.Host,
		"device_index", devIndex,
		"energy_flow_role", string(role),
	)

	resolved, err := registers.Subset(resolvedAll, dev.MetricKeys)
	if err != nil {
		log.Error("device_subset", "err", err)
		return
	}
	chunks := modbusclient.PlanChunks(resolved)
	log.Info("device_start", "metrics", len(resolved), "modbus_reads", len(chunks))

	t := time.NewTicker(org.PollInterval)
	defer t.Stop()

	var sess *modbusclient.Session
	dialFailures := 0
	defer func() {
		if sess != nil {
			_ = sess.Close()
		}
	}()

	for {
		if sess == nil {
			s, err := dialFunc(ctx, dialTargetForDevice(dev))
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				dialFailures++
				wait := reconnectWait(org, dev, dialFailures)
				log.Error("modbus_dial", "err", err, "retry_in", wait.String())
				select {
				case <-ctx.Done():
					return
				case <-time.After(wait):
				}
				continue
			}
			sess = s
			dialFailures = 0
		}

		if err := pollAndStore(ctx, log, cfg, org, dev, sess, resolved, chunks, pool, agg, role); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			log.Error("poll", "err", err)
			_ = sess.Close()
			sess = nil
		}

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func pollAndStore(
	ctx context.Context,
	log *slog.Logger,
	cfg *config.Root,
	org config.Organization,
	dev config.ModbusDevice,
	sess modbusReader,
	resolved []registers.ResolvedEntry,
	chunks []modbusclient.ReadChunk,
	pool *pgxpool.Pool,
	agg *energyflow.Aggregator,
	role energyflow.Role,
) error {
	readCtx, cancel := context.WithTimeout(ctx, readBudgetForPoll(dev.RequestTimeout, len(chunks)))
	defer cancel()

	data, err := readChunkData(readCtx, cfg.ModbusRegisterMap, sess, chunks)
	if err != nil {
		return err
	}
	ts := time.Now().UTC()
	samples, err := buildSamples(org, dev, resolved, chunks, data, ts)
	if err != nil {
		return err
	}

	if err := insertSamples(ctx, pool, samples); err != nil {
		return err
	}
	log.Info("poll_ok", "samples", len(samples))

	// Forward the same decoded values to the per-org energy-flow
	// aggregator so the next allocation_window flush sees the latest
	// snapshot from this device. Submit is non-blocking and never
	// returns an error: a bad reading is filtered inside the
	// aggregator and counted in its diagnostics.
	if agg != nil && role != energyflow.RoleNone {
		values := make(map[string]float64, len(samples))
		for _, s := range samples {
			values[s.MetricKey] = s.Value
		}
		agg.Submit(role, ts, values)
	}
	return nil
}

func readChunkData(ctx context.Context, registerMap config.ModbusRegisterMap, sess modbusReader, chunks []modbusclient.ReadChunk) (map[uint16][]byte, error) {
	data := make(map[uint16][]byte, len(chunks))
	for _, ch := range chunks {
		var b []byte
		var err error
		switch registerMap {
		case config.MapInput:
			b, err = sess.ReadInput(ctx, ch.Start, ch.Quantity)
		default:
			b, err = sess.ReadHolding(ctx, ch.Start, ch.Quantity)
		}
		if err != nil {
			return nil, err
		}
		data[ch.Start] = b
	}
	return data, nil
}

func buildSamples(
	org config.Organization,
	dev config.ModbusDevice,
	resolved []registers.ResolvedEntry,
	chunks []modbusclient.ReadChunk,
	data map[uint16][]byte,
	ts time.Time,
) ([]storage.Sample, error) {
	samples := make([]storage.Sample, 0, len(resolved))
	labels := labelsForDevice(org, dev)
	for _, e := range resolved {
		payload := payloadForEntry(e, chunks, data)
		if payload == nil {
			return nil, errors.New("internal: missing modbus slice for " + e.MetricKey)
		}
		v, err := decode.Scaled(e.DataType, payload, e.Gain, e.Offset)
		if err != nil {
			return nil, err
		}
		samples = append(samples, storage.Sample{
			Time:           ts,
			OrganizationID: org.ID,
			MetricKey:      e.MetricKey,
			Value:          v,
			Labels:         labels,
		})
	}
	return samples, nil
}

func payloadForEntry(e registers.ResolvedEntry, chunks []modbusclient.ReadChunk, data map[uint16][]byte) []byte {
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

// labelsForDevice tags every sample with the org-level metadata plus
// the physical Modbus host the value was read from. The device_host
// label matters when one organization has multiple SmartLoggers
// (e.g. ze splits ESS and PV across two boxes): without it we can't
// tell which SmartLogger reported a given value, and per-device
// diagnostics like `local_time_epoch_s` collapse to a single
// timeline. The label is omitted for mock/empty hosts so unit tests
// that don't care about the device dimension stay clean.
func labelsForDevice(org config.Organization, dev config.ModbusDevice) map[string]string {
	labels := map[string]string{}
	if org.SiteID != "" {
		labels["site_id"] = org.SiteID
	}
	if org.DeviceID != "" {
		labels["device_id"] = org.DeviceID
	}
	if h := strings.TrimSpace(dev.Host); h != "" {
		labels["device_host"] = h
	}
	return labels
}

func dialTargetForDevice(dev config.ModbusDevice) modbusclient.DialTarget {
	return modbusclient.DialTarget{
		Host:           dev.Host,
		Port:           dev.Port,
		UnitID:         dev.UnitID,
		ConnectTimeout: dev.ConnectTimeout,
		RequestTimeout: dev.RequestTimeout,
	}
}

func reconnectWait(org config.Organization, dev config.ModbusDevice, attempt int) time.Duration {
	wait := org.PollInterval
	if wait <= 0 {
		wait = time.Second
	}
	if !dev.ReconnectBackoff || attempt <= 1 {
		return wait
	}
	maxWait := dev.MaxReconnectBackoff
	if maxWait <= 0 {
		maxWait = 2 * time.Minute
	}
	for i := 1; i < attempt; i++ {
		if wait >= maxWait/2 {
			return maxWait
		}
		wait *= 2
	}
	if wait > maxWait {
		return maxWait
	}
	return wait
}

// readBudgetForPoll gives one request timeout window per planned read, plus one extra
// timeout to account for session and scheduler jitter in the poll cycle.
func readBudgetForPoll(requestTimeout time.Duration, chunkCount int) time.Duration {
	return requestTimeout * time.Duration(max(1, chunkCount+1))
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// buildEnergyFlowAggregator returns a configured aggregator for the
// organization, or nil when the org's device whitelist does not cover
// both the PV and ESS accumulator sets. The detection is based on the
// auto-detect rule selected for this project (plan §Топологія):
// each device declares which metric_keys it polls, and the package's
// DetectRole helper classifies each whitelist into RolePV/RoleESS/
// RoleSingle/RoleNone. When at least one device covers PV and at
// least one covers ESS (possibly the same device), energy flow is
// enabled for the org.
//
// On startup the aggregator is reseeded from the latest values
// already present in telemetry_samples so cumulative totals survive
// a process restart. A missing or unreadable seed degrades to
// "start from zero" with a warning rather than aborting.
func buildEnergyFlowAggregator(
	ctx context.Context,
	log *slog.Logger,
	org config.Organization,
	pool *pgxpool.Pool,
) *energyflow.Aggregator {
	devices := org.Devices()
	var coversPV, coversESS bool
	for _, dev := range devices {
		switch energyflow.DetectRole(dev.MetricKeys) {
		case energyflow.RoleSingle:
			coversPV, coversESS = true, true
		case energyflow.RolePV:
			coversPV = true
		case energyflow.RoleESS:
			coversESS = true
		}
	}
	if !(coversPV && coversESS) {
		log.Info("energy_flow_disabled_for_org",
			"organization_id", org.ID,
			"covers_pv", coversPV,
			"covers_ess", coversESS,
			"hint", "extend modbus_devices.metric_keys with the PV and ESS accumulators",
		)
		return nil
	}

	emit := func(ctx context.Context, samples []energyflow.EmittedSample) error {
		if pool == nil || len(samples) == 0 {
			return nil
		}
		out := make([]storage.Sample, 0, len(samples))
		for _, s := range samples {
			out = append(out, storage.Sample{
				Time:           s.Time.UTC(),
				OrganizationID: org.ID,
				MetricKey:      s.MetricKey,
				Value:          s.Value,
				Labels:         map[string]string{"synthetic": "energy_flow"},
			})
		}
		return insertSamples(ctx, pool, out)
	}
	opts := energyflow.Options{EssDischargeSign: org.EssDischargeSign}
	agg := energyflow.New(org.ID, opts, emit, log)
	if pool != nil {
		seedCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		seeds, err := loadEnergyFlowSeeds(seedCtx, pool, org.ID)
		if err != nil {
			log.Warn("energy_flow_seed_load",
				"organization_id", org.ID,
				"err", err,
			)
		} else if len(seeds) > 0 {
			agg.Reseed(seeds)
			log.Info("energy_flow_reseeded",
				"organization_id", org.ID,
				"keys", len(seeds),
			)
		}
	}
	log.Info("energy_flow_enabled_for_org",
		"organization_id", org.ID,
		"devices", len(devices),
	)
	return agg
}

// loadEnergyFlowSeeds queries the latest cumulative value for each
// synthetic energy-flow metric. Used once at startup so the
// aggregator can resume the running totals from where the previous
// process left them.
func loadEnergyFlowSeeds(ctx context.Context, pool *pgxpool.Pool, orgID string) (map[string]float64, error) {
	if pool == nil {
		return nil, nil
	}
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT ON (metric_key)
			metric_key, value
		FROM telemetry_samples
		WHERE organization_id = $1
			AND metric_key = ANY($2)
		ORDER BY metric_key, time DESC
	`, orgID, energyflow.SyntheticMetricKeys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]float64, len(energyflow.SyntheticMetricKeys))
	for rows.Next() {
		var key string
		var value float64
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
