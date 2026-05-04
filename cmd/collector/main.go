package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nesh/sestelemetry/internal/bootstrap"
	"github.com/nesh/sestelemetry/internal/config"
	"github.com/nesh/sestelemetry/internal/decode"
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

	chunks := modbusclient.PlanChunks(resolved)
	log.Info("collector_start", "orgs", len(cfg.Organizations), "metrics", len(resolved), "modbus_reads", len(chunks))

	var wg sync.WaitGroup
	for _, org := range cfg.Organizations {
		org := org
		wg.Add(1)
		go func() {
			defer wg.Done()
			runOrg(ctx, log, cfg, org, resolved, chunks, pool)
		}()
	}
	wg.Wait()
	log.Info("collector_stop")
}

func runOrg(
	ctx context.Context,
	log *slog.Logger,
	cfg *config.Root,
	org config.Organization,
	resolved []registers.ResolvedEntry,
	chunks []modbusclient.ReadChunk,
	pool *pgxpool.Pool,
) {
	log = log.With("organization_id", org.ID)
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
			s, err := modbusclient.Dial(ctx, dialTargetForOrg(org))
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				dialFailures++
				wait := reconnectWait(org, dialFailures)
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

		if err := pollAndStore(ctx, log, cfg, org, sess, resolved, chunks, pool); err != nil {
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
	sess modbusReader,
	resolved []registers.ResolvedEntry,
	chunks []modbusclient.ReadChunk,
	pool *pgxpool.Pool,
) error {
	readCtx, cancel := context.WithTimeout(ctx, readBudgetForPoll(org.Modbus.RequestTimeout, len(chunks)))
	defer cancel()

	data, err := readChunkData(readCtx, cfg.ModbusRegisterMap, sess, chunks)
	if err != nil {
		return err
	}
	ts := time.Now().UTC()
	samples, err := buildSamples(org, resolved, chunks, data, ts)
	if err != nil {
		return err
	}

	if err := insertSamples(ctx, pool, samples); err != nil {
		return err
	}
	log.Info("poll_ok", "samples", len(samples))
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
	resolved []registers.ResolvedEntry,
	chunks []modbusclient.ReadChunk,
	data map[uint16][]byte,
	ts time.Time,
) ([]storage.Sample, error) {
	samples := make([]storage.Sample, 0, len(resolved))
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
			Labels:         labelsForOrg(org),
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

func labelsForOrg(org config.Organization) map[string]string {
	labels := map[string]string{}
	if org.SiteID != "" {
		labels["site_id"] = org.SiteID
	}
	if org.DeviceID != "" {
		labels["device_id"] = org.DeviceID
	}
	return labels
}

func dialTargetForOrg(org config.Organization) modbusclient.DialTarget {
	return modbusclient.DialTarget{
		Host:           org.Modbus.Host,
		Port:           org.Modbus.Port,
		UnitID:         org.Modbus.UnitID,
		ConnectTimeout: org.Modbus.ConnectTimeout,
		RequestTimeout: org.Modbus.RequestTimeout,
	}
}

func reconnectWait(org config.Organization, attempt int) time.Duration {
	wait := org.PollInterval
	if wait <= 0 {
		wait = time.Second
	}
	if !org.Modbus.ReconnectBackoff || attempt <= 1 {
		return wait
	}
	maxWait := org.Modbus.MaxReconnectBackoff
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
