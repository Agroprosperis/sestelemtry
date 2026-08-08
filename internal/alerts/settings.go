package alerts

import (
	"encoding/json"
	"fmt"
	"net/mail"
	"sort"
	"strings"
	"time"
)

// Duration is a time.Duration that crosses the wire as a Go duration
// string ("10m", "6h"). The dashboard edits the same values the YAML
// config uses, so keeping one textual form avoids a unit mismatch
// between the settings page, the database and config.yaml.
type Duration time.Duration

func (d Duration) Duration() time.Duration { return time.Duration(d) }

func (d Duration) String() string { return time.Duration(d).String() }

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*d = 0
		return nil
	}
	var raw string
	if err := json.Unmarshal(b, &raw); err != nil {
		return fmt.Errorf("alerts: duration must be a string like \"10m\": %w", err)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		*d = 0
		return nil
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("alerts: invalid duration %q: %w", raw, err)
	}
	*d = Duration(parsed)
	return nil
}

// SMTPSettings is the mail server, minus the password. The password
// lives in its own database column and is never part of any payload the
// API returns, so it cannot leak through a struct that gets serialized
// by accident.
type SMTPSettings struct {
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	TLS      string   `json:"tls"`
	Username string   `json:"username"`
	From     string   `json:"from"`
	Timeout  Duration `json:"timeout"`
}

// Settings is the site-wide alert configuration edited on the dashboard
// and consumed by the watchdog.
type Settings struct {
	Enabled bool `json:"enabled"`
	// CheckInterval is how often the watchdog samples freshness.
	CheckInterval Duration `json:"check_interval"`
	// StaleAfter is the silence that counts as a lost connection.
	StaleAfter Duration `json:"stale_after"`
	// RepeatInterval re-announces an ongoing outage. Zero disables
	// reminders (unlike the YAML block, which uses a negative value for
	// that — a form control cannot sensibly offer "-1s").
	RepeatInterval Duration     `json:"repeat_interval"`
	NotifyRecovery bool         `json:"notify_recovery"`
	SMTP           SMTPSettings `json:"smtp"`
	// Recipients is the default address list. An organization without
	// its own list inherits this one.
	Recipients []string `json:"recipients"`
}

// OrgSettings overrides delivery for one organization.
type OrgSettings struct {
	Enabled bool `json:"enabled"`
	// Recipients replaces the global list for this organization.
	// Empty means "inherit the global list".
	Recipients []string `json:"recipients"`
}

// Bounds for Validate. They mirror the YAML validation in
// internal/config so a value typed into the dashboard is accepted or
// rejected exactly like the same value written into config.yaml.
const (
	minCheckInterval  = 10 * time.Second
	maxCheckInterval  = time.Hour
	minStaleAfter     = time.Minute
	maxStaleAfter     = 24 * time.Hour
	minRepeatInterval = 5 * time.Minute
	maxRepeatInterval = 30 * 24 * time.Hour
	minSMTPTimeout    = time.Second
	maxSMTPTimeout    = 2 * time.Minute
	maxRecipients     = 32
)

// DefaultSettings is what the dashboard shows before anything has been
// saved and what the watchdog assumes when neither the database nor the
// YAML block says otherwise. Alerts start disabled: an unconfigured
// deployment must not try to email anyone.
func DefaultSettings() Settings {
	return Settings{
		Enabled:        false,
		CheckInterval:  Duration(time.Minute),
		StaleAfter:     Duration(10 * time.Minute),
		RepeatInterval: Duration(6 * time.Hour),
		NotifyRecovery: true,
		SMTP: SMTPSettings{
			Port:    587,
			TLS:     TLSStartTLS,
			Timeout: Duration(20 * time.Second),
		},
	}
}

// Normalize fills defaults and cleans user input. It runs before both
// validation and persistence, so what the database holds is already
// canonical and the watchdog can use it as-is.
func (s *Settings) Normalize() {
	if s.CheckInterval <= 0 {
		s.CheckInterval = Duration(time.Minute)
	}
	if s.StaleAfter <= 0 {
		s.StaleAfter = Duration(10 * time.Minute)
	}
	if s.RepeatInterval < 0 {
		s.RepeatInterval = 0
	}
	s.SMTP.Host = strings.TrimSpace(s.SMTP.Host)
	s.SMTP.Username = strings.TrimSpace(s.SMTP.Username)
	s.SMTP.From = strings.TrimSpace(s.SMTP.From)
	s.SMTP.TLS = strings.ToLower(strings.TrimSpace(s.SMTP.TLS))
	if s.SMTP.TLS == "" {
		s.SMTP.TLS = TLSStartTLS
	}
	if s.SMTP.Port == 0 {
		if s.SMTP.TLS == TLSImplicit {
			s.SMTP.Port = 465
		} else {
			s.SMTP.Port = 587
		}
	}
	if s.SMTP.Timeout <= 0 {
		s.SMTP.Timeout = Duration(20 * time.Second)
	}
	s.Recipients = NormalizeRecipients(s.Recipients)
}

// Validate reports the first problem that would keep the watchdog from
// working. Only an enabled configuration is held to the SMTP
// requirements: an operator must be able to save a half-filled form and
// come back to it.
func (s Settings) Validate() error {
	if d := s.CheckInterval.Duration(); d < minCheckInterval || d > maxCheckInterval {
		return fmt.Errorf("check_interval must be between %s and %s, got %s", minCheckInterval, maxCheckInterval, d)
	}
	if d := s.StaleAfter.Duration(); d < minStaleAfter || d > maxStaleAfter {
		return fmt.Errorf("stale_after must be between %s and %s, got %s", minStaleAfter, maxStaleAfter, d)
	}
	// A threshold shorter than the sampling cadence would fire on the
	// gap between two checks rather than on a real outage.
	if s.StaleAfter < s.CheckInterval {
		return fmt.Errorf("stale_after (%s) must be >= check_interval (%s)", s.StaleAfter, s.CheckInterval)
	}
	if d := s.RepeatInterval.Duration(); d != 0 && (d < minRepeatInterval || d > maxRepeatInterval) {
		return fmt.Errorf("repeat_interval must be 0 or between %s and %s, got %s", minRepeatInterval, maxRepeatInterval, d)
	}
	switch s.SMTP.TLS {
	case TLSStartTLS, TLSImplicit, TLSNone:
	default:
		return fmt.Errorf("smtp.tls must be %q, %q or %q, got %q", TLSStartTLS, TLSImplicit, TLSNone, s.SMTP.TLS)
	}
	if s.SMTP.Port < 1 || s.SMTP.Port > 65535 {
		return fmt.Errorf("smtp.port out of range: %d", s.SMTP.Port)
	}
	if d := s.SMTP.Timeout.Duration(); d < minSMTPTimeout || d > maxSMTPTimeout {
		return fmt.Errorf("smtp.timeout must be between %s and %s, got %s", minSMTPTimeout, maxSMTPTimeout, d)
	}
	if s.SMTP.From != "" {
		if _, err := mail.ParseAddress(s.SMTP.From); err != nil {
			return fmt.Errorf("smtp.from is not a valid address: %q", s.SMTP.From)
		}
	}
	if err := validateRecipients(s.Recipients); err != nil {
		return err
	}
	if s.Enabled {
		if s.SMTP.Host == "" {
			return fmt.Errorf("smtp.host is required when alerts are enabled")
		}
		if s.SMTP.From == "" {
			return fmt.Errorf("smtp.from is required when alerts are enabled")
		}
		if s.SMTP.Username != "" && s.SMTP.TLS == TLSNone {
			return fmt.Errorf("smtp.username requires tls %q or %q", TLSStartTLS, TLSImplicit)
		}
	}
	return nil
}

// Thresholds projects the settings onto the evaluator's tunables.
func (s Settings) Thresholds() Thresholds {
	return Thresholds{
		StaleAfter:     s.StaleAfter.Duration(),
		RepeatInterval: s.RepeatInterval.Duration(),
		NotifyRecovery: s.NotifyRecovery,
	}
}

// DecodeSettings parses a stored settings blob.
//
// It starts from the defaults so a row written by an older build — one
// that predates a field added since — lands on a sane value instead of a
// zero one: a stored `{"enabled":true}` must not turn into a zero-length
// check interval that spins the watchdog.
func DecodeSettings(raw []byte) (Settings, error) {
	out := DefaultSettings()
	if err := json.Unmarshal(raw, &out); err != nil {
		return Settings{}, fmt.Errorf("alerts: decode settings: %w", err)
	}
	out.Normalize()
	return out, nil
}

// DecodeOrgSettings parses one organization's stored override.
func DecodeOrgSettings(raw []byte) (OrgSettings, error) {
	var out OrgSettings
	if err := json.Unmarshal(raw, &out); err != nil {
		return OrgSettings{}, fmt.Errorf("alerts: decode organization settings: %w", err)
	}
	out.Normalize()
	return out, nil
}

// Normalize cleans one organization's overrides.
func (o *OrgSettings) Normalize() {
	o.Recipients = NormalizeRecipients(o.Recipients)
}

// Validate checks the override's address list.
func (o OrgSettings) Validate() error {
	return validateRecipients(o.Recipients)
}

// Delivery is the effective decision for one organization: whether to
// alert about it at all, and where those emails go.
type Delivery struct {
	Enabled    bool
	Recipients []string
}

// DeliveryFor resolves the global settings and an organization's
// override into a delivery decision.
//
// An organization with its own address list gets exactly that list —
// the global one is replaced, not extended, so "send this site only to
// its own operator" needs no extra switch. An organization the operator
// has never touched inherits the global list and is enabled, so adding
// a site to config.yaml does not silently leave it unmonitored.
func (s Settings) DeliveryFor(organizationID string, overrides map[string]OrgSettings) Delivery {
	if !s.Enabled {
		return Delivery{}
	}
	recipients := s.Recipients
	if override, ok := overrides[organizationID]; ok {
		if !override.Enabled {
			return Delivery{}
		}
		if len(override.Recipients) > 0 {
			recipients = override.Recipients
		}
	}
	if len(recipients) == 0 {
		// Nothing to send to: treat as disabled rather than producing
		// batches the mailer would reject.
		return Delivery{}
	}
	return Delivery{Enabled: true, Recipients: recipients}
}

// Batch is one email: the events that share a destination list.
type Batch struct {
	Recipients []string
	Events     []Event
}

// GroupByRecipients splits a tick's events into one batch per distinct
// address list.
//
// When every organization uses the global list this collapses to a
// single batch, preserving the "one email per tick" behaviour that keeps
// a site-wide outage from fanning out into one message per elevator.
// Events whose organization resolves to no recipients are dropped.
func GroupByRecipients(events []Event, recipientsFor func(organizationID string) []string) []Batch {
	type bucket struct {
		recipients []string
		events     []Event
	}
	buckets := make(map[string]*bucket)
	order := make([]string, 0, 4)
	for _, e := range events {
		recipients := NormalizeRecipients(recipientsFor(e.OrganizationID))
		if len(recipients) == 0 {
			continue
		}
		key := strings.Join(recipients, ",")
		b, ok := buckets[key]
		if !ok {
			b = &bucket{recipients: recipients}
			buckets[key] = b
			order = append(order, key)
		}
		b.events = append(b.events, e)
	}
	sort.Strings(order)
	out := make([]Batch, 0, len(order))
	for _, key := range order {
		b := buckets[key]
		out = append(out, Batch{Recipients: b.recipients, Events: b.events})
	}
	return out
}

// NormalizeRecipients trims, drops blanks and de-duplicates while
// sorting, so two lists with the same addresses in a different order
// group into one email instead of two.
func NormalizeRecipients(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, addr := range in {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		if _, dup := seen[addr]; dup {
			continue
		}
		seen[addr] = struct{}{}
		out = append(out, addr)
	}
	sort.Strings(out)
	return out
}

func validateRecipients(list []string) error {
	if len(list) > maxRecipients {
		return fmt.Errorf("too many recipients: %d (max %d)", len(list), maxRecipients)
	}
	for _, addr := range list {
		if _, err := mail.ParseAddress(addr); err != nil {
			return fmt.Errorf("recipient is not a valid address: %q", addr)
		}
	}
	return nil
}
