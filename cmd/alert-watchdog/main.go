// Command alert-watchdog emails operators when a site stops reporting.
//
// Detection is database-side: every check interval the daemon asks how
// fresh the newest telemetry row of each configured Modbus device is, and
// anything quieter than the staleness threshold is announced as a lost
// connection. Watching the data rather than the Modbus link means a
// crashed collector, a dead network and a powered-off SmartLogger all
// produce the same alert — which is what the operator cares about — and
// it keeps the 1 s polling loop free of alerting code.
//
// Delivery settings come from the database, where the dashboard's
// notifications page writes them, and are re-read on every tick: an
// operator who changes recipients or flips the switch does not have to
// restart this container. The `alerts:` block in config.yaml is the
// fallback for a deployment that has never opened that page.
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
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	fallbackPassword := strings.TrimSpace(os.Getenv("SMTP_PASSWORD"))
	if fallbackPassword == "" {
		fallbackPassword = cfg.Alerts.SMTP.Password
	}

	wd := &watchdog{
		log:                log,
		devices:            monitoredDevices(cfg),
		fallback:           alerts.SettingsFromConfig(cfg.Alerts),
		fallbackPassword:   fallbackPassword,
		warnedNoRecipients: map[string]bool{},
	}

	if cfg.DatabaseURL == "" && !*testEmail {
		log.Error("database_url missing", "hint", "set in config YAML or DATABASE_URL")
		os.Exit(1)
	}
	if cfg.DatabaseURL != "" {
		pool, err := storage.Open(ctx, cfg.DatabaseURL)
		if err != nil {
			// -test-email must stay usable while the database is down:
			// its whole point is to verify mail credentials in isolation.
			if !*testEmail {
				log.Error("db_open", "err", err)
				os.Exit(1)
			}
			log.Warn("db_open", "err", err, "hint", "using config.yaml settings for the test email")
		} else {
			defer pool.Close()
			wd.pool = pool
		}
	}
	// Before any schema work, so a test email still goes out when the
	// database is unreachable.
	if *testEmail {
		if err := wd.sendTestEmail(ctx); err != nil {
			log.Error("alert_email_failed", "err", err, "test", true)
			os.Exit(1)
		}
		return
	}

	if err := storage.InitAlertSchema(ctx, wd.pool); err != nil {
		log.Error("db_schema", "err", err)
		os.Exit(1)
	}
	// Also owned by the API, but the watchdog may well come up first on
	// a fresh host and it only reads these tables.
	if err := storage.InitAlertSettingsSchema(ctx, wd.pool); err != nil {
		log.Error("db_schema_alert_settings", "err", err)
		os.Exit(1)
	}

	if len(wd.devices) == 0 {
		log.Warn("alert_no_devices",
			"hint", "no organizations with a modbus host are configured; nothing to watch")
	}

	interval, err := wd.runOnce(ctx)
	if err != nil {
		log.Error("alert_check_failed", "err", err)
	}
	if *once {
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("alert_watchdog_stop")
			return
		case <-ticker.C:
			next, err := wd.runOnce(ctx)
			if err != nil {
				// A database hiccup must not be mistaken for "every site
				// is down": the pass is abandoned without touching stored
				// state or sending anything, and the next tick retries.
				log.Error("alert_check_failed", "err", err)
			}
			if next > 0 && next != interval {
				interval = next
				ticker.Reset(interval)
				log.Info("alert_check_interval_changed", "check_interval", interval.String())
			}
		}
	}
}

// watchdog holds everything one check pass needs plus the little state
// that keeps the log readable across ticks.
type watchdog struct {
	log     *slog.Logger
	pool    *pgxpool.Pool
	devices []monitoredDevice

	// fallback / fallbackPassword are the config.yaml settings used
	// until the notifications page saves something.
	fallback         alerts.Settings
	fallbackPassword string

	// warnedNoRecipients and lastEnabled make the log report changes
	// rather than repeating the same line every check interval.
	warnedNoRecipients map[string]bool
	lastEnabled        *bool
}

// effective is the configuration one pass acts on.
type effective struct {
	settings  alerts.Settings
	password  string
	overrides map[string]alerts.OrgSettings
	fromDB    bool
}

// loadSettings reads the settings the dashboard saved, falling back to
// config.yaml when the page has never been used. Called every tick so a
// change in the UI takes effect without a restart.
func (w *watchdog) loadSettings(ctx context.Context) (effective, error) {
	out := effective{settings: w.fallback, password: w.fallbackPassword}
	if w.pool == nil {
		return out, nil
	}
	raw, _, ok, err := storage.GetAlertSettings(ctx, w.pool)
	if err != nil {
		return effective{}, err
	}
	if ok {
		settings, err := alerts.DecodeSettings(raw)
		if err != nil {
			return effective{}, err
		}
		password, err := storage.GetSMTPPassword(ctx, w.pool)
		if err != nil {
			return effective{}, err
		}
		if password == "" {
			// The page can be used to set everything but the password,
			// leaving SMTP_PASSWORD in place as the deployment secret.
			password = w.fallbackPassword
		}
		out = effective{settings: settings, password: password, fromDB: true}
	}
	rows, err := storage.LoadOrgAlertSettings(ctx, w.pool)
	if err != nil {
		return effective{}, err
	}
	if len(rows) > 0 {
		out.overrides = make(map[string]alerts.OrgSettings, len(rows))
		for _, row := range rows {
			parsed, err := alerts.DecodeOrgSettings(row.Settings)
			if err != nil {
				return effective{}, fmt.Errorf("organization %s: %w", row.OrganizationID, err)
			}
			out.overrides[row.OrganizationID] = parsed
		}
	}
	return out, nil
}

// runOnce performs one check pass and returns the interval until the
// next one, which the caller uses to retune the ticker.
func (w *watchdog) runOnce(ctx context.Context) (time.Duration, error) {
	eff, err := w.loadSettings(ctx)
	if err != nil {
		return 0, err
	}
	interval := eff.settings.CheckInterval.Duration()

	if !eff.settings.Enabled {
		// Keep ticking instead of sleeping forever: the switch lives in
		// the UI now, and it has to take effect without a restart.
		w.logEnabledChange(false, eff)
		return interval, nil
	}
	w.logEnabledChange(true, eff)

	targets, recipients := w.targets(eff)
	if len(targets) == 0 || w.pool == nil {
		return interval, nil
	}
	if err := w.check(ctx, eff, targets, recipients); err != nil {
		return interval, err
	}
	return interval, nil
}

// targets picks the devices to watch this tick and resolves where each
// organization's alerts go. Organizations that are switched off, or that
// would have nobody to email, drop out.
func (w *watchdog) targets(eff effective) ([]monitoredDevice, map[string][]string) {
	recipients := make(map[string][]string)
	out := make([]monitoredDevice, 0, len(w.devices))
	for _, d := range w.devices {
		orgID := d.OrganizationID
		if _, seen := recipients[orgID]; !seen {
			delivery := eff.settings.DeliveryFor(orgID, eff.overrides)
			if delivery.Enabled {
				recipients[orgID] = delivery.Recipients
			} else {
				recipients[orgID] = nil
				w.warnIfUnreachable(orgID, eff)
			}
		}
		if len(recipients[orgID]) == 0 {
			continue
		}
		out = append(out, d)
	}
	return out, recipients
}

// warnIfUnreachable flags an organization that is switched on but has
// nobody to notify — a silent misconfiguration that otherwise looks
// exactly like healthy operation. Logged once per organization until it
// is fixed.
func (w *watchdog) warnIfUnreachable(orgID string, eff effective) {
	override, hasOverride := eff.overrides[orgID]
	if hasOverride && !override.Enabled {
		return // deliberately muted
	}
	if w.warnedNoRecipients[orgID] {
		return
	}
	w.warnedNoRecipients[orgID] = true
	w.log.Warn("alert_org_without_recipients",
		"organization_id", orgID,
		"hint", "add an address on the notifications page or to the default list")
}

func (w *watchdog) logEnabledChange(enabled bool, eff effective) {
	if w.lastEnabled != nil && *w.lastEnabled == enabled {
		return
	}
	w.lastEnabled = &enabled
	source := "config.yaml"
	if eff.fromDB {
		source = "database"
	}
	if !enabled {
		w.log.Info("alert_watchdog_disabled",
			"source", source,
			"hint", "enable alerts on the dashboard's notifications page")
		return
	}
	w.log.Info("alert_watchdog_enabled",
		"source", source,
		"devices", len(w.devices),
		"check_interval", eff.settings.CheckInterval.String(),
		"stale_after", eff.settings.StaleAfter.String(),
		"repeat_interval", eff.settings.RepeatInterval.String(),
		"notify_recovery", eff.settings.NotifyRecovery,
		"default_recipients", len(eff.settings.Recipients),
	)
}

// sendTestEmail proves the mail path end to end before an outage does.
func (w *watchdog) sendTestEmail(ctx context.Context) error {
	eff, err := w.loadSettings(ctx)
	if err != nil {
		// The point of this mode is to check the mail path, so a
		// database that is down must not stop it.
		w.log.Warn("alert_settings_load", "err", err, "hint", "using config.yaml settings")
		eff = effective{settings: w.fallback, password: w.fallbackPassword}
	}
	recipients := eff.settings.Recipients
	if len(recipients) == 0 {
		return fmt.Errorf("no recipients configured")
	}
	mailer, err := alerts.NewMailer(eff.settings.SMTP, eff.password)
	if err != nil {
		return err
	}
	msg := alerts.BuildTestMessage(deviceList(w.devices), time.Now(), time.Local)
	if err := mailer.Send(ctx, recipients, msg); err != nil {
		return err
	}
	w.log.Info("alert_email_sent", "test", true, "recipients", len(recipients), "subject", msg.Subject)
	return nil
}

// monitoredDevice pairs a device identity with the metric key used to
// probe its freshness.
type monitoredDevice struct {
	alerts.Device
	ProbeMetricKey string
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

// check runs the freshness probe, evaluates state transitions and
// delivers one email per distinct recipient list.
func (w *watchdog) check(
	ctx context.Context,
	eff effective,
	devices []monitoredDevice,
	recipients map[string][]string,
) error {
	th := eff.settings.Thresholds()
	now := time.Now()
	notBefore := now.Add(-lookback(th.StaleAfter))

	observations := make([]alerts.Observation, 0, len(devices))
	for _, d := range devices {
		last, err := storage.LatestSampleAt(ctx, w.pool, d.OrganizationID, d.ProbeMetricKey, notBefore)
		if err != nil {
			return fmt.Errorf("freshness %s/%s: %w", d.OrganizationID, d.Host, err)
		}
		observations = append(observations, alerts.Observation{Device: d.Device, LastSampleAt: last})
	}

	stored, err := storage.LoadDeviceAlertStates(ctx, w.pool)
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
		w.log.Warn(event,
			"kind", string(e.Kind),
			"organization_id", e.OrganizationID,
			"device_host", e.Host,
			"last_sample_at", formatTime(e.LastSampleAt),
			"duration", e.Duration().Round(time.Second).String(),
		)
	}

	if batches := alerts.GroupByRecipients(res.Events, func(orgID string) []string {
		return recipients[orgID]
	}); len(batches) > 0 {
		mailer, err := alerts.NewMailer(eff.settings.SMTP, eff.password)
		if err != nil {
			// Abandon the pass rather than persisting an outage nobody
			// was told about: a state saved as "down" would keep the
			// next tick from re-announcing it once SMTP is fixed.
			return fmt.Errorf("smtp settings: %w", err)
		}
		for _, batch := range batches {
			msg, ok := alerts.BuildMessage(batch.Events, time.Local)
			if !ok {
				continue
			}
			if err := mailer.Send(ctx, batch.Recipients, msg); err != nil {
				w.log.Error("alert_email_failed", "err", err, "events", len(batch.Events))
				// Leave last_notified_at as it was so the next tick
				// retries. Losing the site's uplink usually takes the
				// mail relay with it, and the operator still needs the
				// email when it returns. Only this batch is rolled back:
				// the other recipient lists may have been delivered.
				res.RevertNotifications(prev, batch.Events)
				continue
			}
			w.log.Info("alert_email_sent",
				"subject", msg.Subject,
				"events", len(batch.Events),
				"recipients", len(batch.Recipients),
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
	return storage.UpsertDeviceAlertStates(ctx, w.pool, rows)
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
