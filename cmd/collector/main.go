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

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}
	if v := os.Getenv("DATABASE_URL"); v != "" {
		cfg.DatabaseURL = strings.TrimSpace(v)
	}
	if cfg.DatabaseURL == "" {
		log.Error("database_url missing", "hint", "set in config YAML or DATABASE_URL")
		os.Exit(1)
	}

	cat, err := registers.Load(cfg.RegisterCatalog)
	if err != nil {
		log.Error("register_catalog", "path", cfg.RegisterCatalog, "err", err)
		os.Exit(1)
	}

	base := cat.Addressing.HoldingAddressBase
	if cfg.RegisterAddressing.HoldingAddressBase != 0 {
		base = cfg.RegisterAddressing.HoldingAddressBase
	}
	resolved, err := cat.Resolve(base)
	if err != nil {
		log.Error("resolve_registers", "err", err)
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
	defer func() {
		if sess != nil {
			_ = sess.Close()
		}
	}()

	for {
		if sess == nil {
			s, err := modbusclient.Dial(ctx, org)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				log.Error("modbus_dial", "err", err)
				select {
				case <-ctx.Done():
					return
				case <-time.After(org.PollInterval):
				}
				continue
			}
			sess = s
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
	readBudget := org.Modbus.RequestTimeout * time.Duration(max(1, len(chunks)+1))
	readCtx, cancel := context.WithTimeout(ctx, readBudget)
	defer cancel()

	data := make(map[uint16][]byte, len(chunks))
	for _, ch := range chunks {
		var b []byte
		var err error
		switch cfg.ModbusRegisterMap {
		case config.MapInput:
			b, err = sess.ReadInput(readCtx, ch.Start, ch.Quantity)
		default:
			b, err = sess.ReadHolding(readCtx, ch.Start, ch.Quantity)
		}
		if err != nil {
			return err
		}
		data[ch.Start] = b
	}

	ts := time.Now().UTC()
	samples := make([]storage.Sample, 0, len(resolved))
	for _, e := range resolved {
		var payload []byte
		for _, ch := range chunks {
			last := ch.Start + ch.Quantity - 1
			if e.PDUStart >= ch.Start && e.PDUEnd <= last {
				raw, ok := data[ch.Start]
				if !ok {
					continue
				}
				payload = modbusclient.SliceForEntry(ch.Start, raw, e)
				break
			}
		}
		if payload == nil {
			return errors.New("internal: missing modbus slice for " + e.MetricKey)
		}
		v, err := decode.Scaled(e.DataType, payload, e.Gain, e.Offset)
		if err != nil {
			return err
		}
		labels := map[string]string{}
		if org.SiteID != "" {
			labels["site_id"] = org.SiteID
		}
		if org.DeviceID != "" {
			labels["device_id"] = org.DeviceID
		}
		samples = append(samples, storage.Sample{
			Time:           ts,
			OrganizationID: org.ID,
			MetricKey:      e.MetricKey,
			Value:          v,
			Labels:         labels,
		})
	}

	if err := insertSamples(ctx, pool, samples); err != nil {
		return err
	}
	log.Info("poll_ok", "samples", len(samples))
	return nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
