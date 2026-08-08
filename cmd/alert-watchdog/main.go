// Command alert-watchdog emails operators when a site stops reporting.
//
// Detection is database-side: every `alerts.check_interval` the daemon
// asks how fresh the newest telemetry row of each configured Modbus
// device is, and anything quieter than `alerts.stale_after` is announced
// as a lost connection. Watching the data rather than the Modbus link
// means a crashed collector, a dead network and a powered-off
// SmartLogger all produce the same alert — which is what the operator
// cares about — and it keeps the 1 s polling loop free of alerting code.
//
// Structure mirrors cmd/weather-collector: one long-lived process on a
// ticker, plus a `-once` mode for cron and a `-test-email` mode for
// verifying SMTP credentials during deployment.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nesh/sestelemetry/internal/alerts"
	"github.com/nesh/sestelemetry/internal/config"
	"github.com/nesh/sestelemetry/internal/storage"
)

// probePreferredKey is the metric every SmartLogger reports regardless of
// what else it owns: the device clock. It makes the freshness probe
// independent of whether a site currently produces PV, charges its ESS
// or sits idle.
const probePreferredKey = "local_time_epoch_s"

// minLookback bounds the freshness query's time range. It must exceed
// stale_after by a wide margin (so a device just past the threshold is
// still found and its real last-seen time reported), while staying small
// enough that the index range scan over a multi-gigabyte hypertable
// stays cheap.
const minLookback = 24 * time.Hour

func main() {
	configPath := flag.String("config", "config.yaml", "path to YAML config")
	once := flag.Bool("once", false, "run a single check pass and exit")
	testEmail := flag.Bool("test-email", false, "send a test email to the configured recipients and exit")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}
	if v := strings.TrimSpace(os.Getenv("DATABASE_URL")); v != "" {
		cfg.DatabaseURL = v
	}
	if v := strings.TrimSpace(os.Getenv("SMTP_PASSWORD")); v != "" {
		cfg.Alerts.SMTP.Password = v
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	devices := monitoredDevices(cfg)

	// -test-email must work before `alerts.enabled` is flipped on and
	// without a reachable database, so a deployment can verify SMTP
	// credentials in isolation.
	if *testEmail {
		mailer, err := newMailer(cfg.Alerts.SMTP)
		if err != nil {
			log.Error("alert_mailer", "err", err)
			os.Exit(1)
		}
		msg := alerts.BuildTestMessage(deviceList(devices), time.Now(), time.Local)
		if err := mailer.Send(ctx, msg); err != nil {
			log.Error("alert_email_failed", "err", err, "test", true)
			os.Exit(1)
		}
		log.Info("alert_email_sent", "test", true, "recipients", len(mailer.Recipients()), "subject", msg.Subject)
		return
	}

	if !cfg.Alerts.Enabled && !*once {
		log.Info("alert_watchdog_disabled",
			"hint", "set alerts.enabled: true in config.yaml to start checking")
		<-ctx.Done()
		return
	}
	if len(devices) == 0 {
		log.Warn("alert_no_devices",
			"hint", "no organizations with a modbus host are configured; nothing to watch")
	}

	mailer, err := newMailer(cfg.Alerts.SMTP)
	if err != nil {
		log.Error("alert_mailer", "err", err)
		os.Exit(1)
	}

	if cfg.DatabaseURL == "" {
		log.Error("database_url missing", "hint", "set in config YAML or DATABASE_URL")
		os.Exit(1)
	}
	pool, err := storage.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("db_open", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := storage.InitAlertSchema(ctx, pool); err != nil {
		log.Error("db_schema", "err", err)
		os.Exit(1)
	}

	th := alerts.Thresholds{
		StaleAfter:     cfg.Alerts.StaleAfter,
		RepeatInterval: cfg.Alerts.RepeatInterval,
		NotifyRecovery: cfg.Alerts.NotifyRecoveryEnabled(),
	}

	log.Info("alert_watchdog_start",
		"devices", len(devices),
		"check_interval", cfg.Alerts.CheckInterval,
		"stale_after", cfg.Alerts.StaleAfter,
		"repeat_interval", cfg.Alerts.RepeatInterval,
		"notify_recovery", th.NotifyRecovery,
		"recipients", len(mailer.Recipients()),
		"once", *once,
	)

	check := func() {
		if err := runCheck(ctx, log, pool, mailer, devices, th); err != nil {
			// A database hiccup must not be mistaken for "every site is
			// down": the pass is abandoned without touching stored state
			// or sending anything, and the next tick retries.
			log.Error("alert_check_failed", "err", err)
		}
	}
	check()

	if *once {
		return
	}

	ticker := time.NewTicker(cfg.Alerts.CheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("alert_watchdog_stop")
			return
		case <-ticker.C:
			check()
		}
	}
}

// monitoredDevice pairs a device identity with the metric key used to
// probe its freshness.
type monitoredDevice struct {
	alerts.Device
	ProbeMetricKey string
}

func newMailer(smtp config.SMTP) (*alerts.Mailer, error) {
	return alerts.NewMailer(alerts.SMTPOptions{
		Host:     smtp.Host,
		Port:     smtp.Port,
		TLS:      string(smtp.TLS),
		Username: smtp.Username,
		Password: smtp.Password,
		From:     smtp.From,
		To:       smtp.To,
		Timeout:  smtp.Timeout,
	})
}

// monitoredDevices flattens the config into the endpoints to watch. It
// walks org.Devices() rather than distinct hosts already present in the
// database, so a device that has never written a single row is reported
// as down instead of silently ignored.
func monitoredDevices(cfg *config.Root) []monitoredDevice {
	out := make([]monitoredDevice, 0, len(cfg.Organizations))
	for i := range cfg.Organizations {
		org := &cfg.Organizations[i]
		devices := org.Devices()
		counts := metricKeyCounts(devices)
		for _, dev := range devices {
			host := strings.TrimSpace(dev.Host)
			if host == "" {
				continue
			}
			out = append(out, monitoredDevice{
				Device: alerts.Device{
					OrganizationID:   org.ID,
					OrganizationName: org.Name,
					Host:             host,
				},
				ProbeMetricKey: probeMetricKey(dev, counts),
			})
		}
	}
	return out
}

func deviceList(devices []monitoredDevice) []alerts.Device {
	out := make([]alerts.Device, 0, len(devices))
	for _, d := range devices {
		out = append(out, d.Device)
	}
	return out
}

// metricKeyCounts tallies how many devices of one organization declare
// each metric key.
func metricKeyCounts(devices []config.ModbusDevice) map[string]int {
	counts := make(map[string]int)
	for _, dev := range devices {
		for _, key := range dev.MetricKeys {
			counts[key]++
		}
	}
	return counts
}

// probeMetricKey picks the metric whose freshness stands for the whole
// device.
//
// Samples carry an organization_id but no device host, so the probe must
// be a key only this device writes — otherwise a two-SmartLogger site
// would look healthy while one of the loggers is dark. Multi-device orgs
// scope their keys with a `metric_keys` whitelist, and in practice both
// loggers also list the shared clock register, so shared keys are
// filtered out first. Single-device orgs poll the whole catalog and use
// the clock register directly.
func probeMetricKey(dev config.ModbusDevice, counts map[string]int) string {
	if len(dev.MetricKeys) == 0 {
		return probePreferredKey
	}
	exclusive := make([]string, 0, len(dev.MetricKeys))
	for _, key := range dev.MetricKeys {
		if counts[key] == 1 {
			exclusive = append(exclusive, key)
		}
	}
	candidates := exclusive
	if len(candidates) == 0 {
		// Whitelists overlap completely; any key at least tells us the
		// organization is alive.
		candidates = append(candidates, dev.MetricKeys...)
	}
	for _, key := range candidates {
		if key == probePreferredKey {
			return key
		}
	}
	sorted := append([]string(nil), candidates...)
	sort.Strings(sorted)
	return sorted[0]
}

func runCheck(
	ctx context.Context,
	log *slog.Logger,
	pool *pgxpool.Pool,
	mailer *alerts.Mailer,
	devices []monitoredDevice,
	th alerts.Thresholds,
) error {
	if len(devices) == 0 {
		return nil
	}
	now := time.Now()
	notBefore := now.Add(-lookback(th.StaleAfter))

	observations := make([]alerts.Observation, 0, len(devices))
	for _, d := range devices {
		last, err := storage.LatestSampleAt(ctx, pool, d.OrganizationID, d.ProbeMetricKey, notBefore)
		if err != nil {
			return fmt.Errorf("freshness %s/%s: %w", d.OrganizationID, d.Host, err)
		}
		observations = append(observations, alerts.Observation{Device: d.Device, LastSampleAt: last})
	}

	stored, err := storage.LoadDeviceAlertStates(ctx, pool)
	if err != nil {
		return err
	}
	prev := make(map[alerts.Key]alerts.State, len(stored))
	for _, s := range stored {
		prev[alerts.Key{OrganizationID: s.OrganizationID, Host: s.DeviceHost}] = alerts.State{
			State:          s.State,
			Since:          s.Since,
			LastSampleAt:   s.LastSampleAt,
			LastNotifiedAt: s.LastNotifiedAt,
		}
	}

	res := alerts.Evaluate(observations, prev, now, th)
	for _, e := range res.Events {
		event := "alert_device_recovered"
		if e.Kind != alerts.KindRecovered {
			event = "alert_device_down"
		}
		log.Warn(event,
			"kind", string(e.Kind),
			"organization_id", e.OrganizationID,
			"device_host", e.Host,
			"last_sample_at", formatTime(e.LastSampleAt),
			"duration", e.Duration().Round(time.Second).String(),
		)
	}

	if msg, ok := alerts.BuildMessage(res.Events, time.Local); ok {
		if err := mailer.Send(ctx, msg); err != nil {
			log.Error("alert_email_failed", "err", err, "events", len(res.Events))
			// Leave last_notified_at as it was so the next tick retries.
			// Losing the site's uplink usually takes the mail relay with
			// it, and the operator still needs the email when it returns.
			res.RevertNotifications(prev)
		} else {
			log.Info("alert_email_sent",
				"subject", msg.Subject,
				"events", len(res.Events),
				"recipients", len(mailer.Recipients()),
			)
		}
	}

	rows := make([]storage.DeviceAlertState, 0, len(res.States))
	for key, s := range res.States {
		rows = append(rows, storage.DeviceAlertState{
			OrganizationID: key.OrganizationID,
			DeviceHost:     key.Host,
			State:          s.State,
			Since:          s.Since,
			LastSampleAt:   s.LastSampleAt,
			LastNotifiedAt: s.LastNotifiedAt,
		})
	}
	return storage.UpsertDeviceAlertStates(ctx, pool, rows)
}

// lookback is how far back the freshness query looks. Four times the
// staleness threshold keeps a device that just crossed it well inside
// the window; the 24 h floor means a device down overnight still reports
// its true last-seen timestamp rather than "no data at all".
func lookback(staleAfter time.Duration) time.Duration {
	if d := 4 * staleAfter; d > minLookback {
		return d
	}
	return minLookback
}

func formatTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
