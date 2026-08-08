package alerts

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/config"
)

func validSettings() Settings {
	s := DefaultSettings()
	s.Enabled = true
	s.SMTP.Host = "smtp.example.com"
	s.SMTP.From = "СЕС Моніторинг <alerts@example.com>"
	s.Recipients = []string{"ops@example.com"}
	return s
}

func TestDurationRoundTripsAsString(t *testing.T) {
	type wrapper struct {
		D Duration `json:"d"`
	}
	raw, err := json.Marshal(wrapper{D: Duration(90 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"d":"1h30m0s"}` {
		t.Fatalf("marshalled = %s", raw)
	}
	var back wrapper
	if err := json.Unmarshal([]byte(`{"d":"10m"}`), &back); err != nil {
		t.Fatal(err)
	}
	if back.D.Duration() != 10*time.Minute {
		t.Fatalf("unmarshalled = %v", back.D)
	}
	// A number is a common mistake from a hand-written client; it must
	// fail loudly rather than being read as nanoseconds.
	if err := json.Unmarshal([]byte(`{"d":600}`), &back); err == nil {
		t.Fatal("expected numeric duration to be rejected")
	}
	if err := json.Unmarshal([]byte(`{"d":"ten minutes"}`), &back); err == nil {
		t.Fatal("expected garbage duration to be rejected")
	}
}

func TestNormalizeFillsDefaults(t *testing.T) {
	s := Settings{SMTP: SMTPSettings{TLS: " IMPLICIT ", Host: " smtp.example.com "}}
	s.Recipients = []string{" b@example.com ", "", "a@example.com", "a@example.com"}
	s.Normalize()

	if s.CheckInterval.Duration() != time.Minute || s.StaleAfter.Duration() != 10*time.Minute {
		t.Fatalf("intervals = %v / %v", s.CheckInterval, s.StaleAfter)
	}
	if s.SMTP.TLS != TLSImplicit {
		t.Fatalf("tls = %q", s.SMTP.TLS)
	}
	if s.SMTP.Port != 465 {
		t.Fatalf("implicit tls must default to port 465, got %d", s.SMTP.Port)
	}
	if s.SMTP.Host != "smtp.example.com" {
		t.Fatalf("host = %q", s.SMTP.Host)
	}
	if want := []string{"a@example.com", "b@example.com"}; !reflect.DeepEqual(s.Recipients, want) {
		t.Fatalf("recipients = %#v, want %#v", s.Recipients, want)
	}
}

func TestNormalizeTreatsNegativeRepeatAsDisabled(t *testing.T) {
	s := Settings{RepeatInterval: Duration(-time.Second)}
	s.Normalize()
	if s.RepeatInterval != 0 {
		t.Fatalf("repeat_interval = %v, want 0", s.RepeatInterval)
	}
	if s.Thresholds().RepeatInterval != 0 {
		t.Fatal("thresholds must carry the disabled reminder through")
	}
}

func TestValidateRejectsBadSettings(t *testing.T) {
	cases := map[string]func(s *Settings){
		"check interval too short": func(s *Settings) { s.CheckInterval = Duration(time.Second) },
		"check interval too long":  func(s *Settings) { s.CheckInterval = Duration(2 * time.Hour) },
		"stale below check interval": func(s *Settings) {
			s.CheckInterval = Duration(30 * time.Minute)
			s.StaleAfter = Duration(5 * time.Minute)
		},
		"stale too long":       func(s *Settings) { s.StaleAfter = Duration(48 * time.Hour) },
		"repeat too short":     func(s *Settings) { s.RepeatInterval = Duration(time.Minute) },
		"unknown tls":          func(s *Settings) { s.SMTP.TLS = "ssl" },
		"bad port":             func(s *Settings) { s.SMTP.Port = 70000 },
		"timeout too long":     func(s *Settings) { s.SMTP.Timeout = Duration(10 * time.Minute) },
		"bad from":             func(s *Settings) { s.SMTP.From = "not an address" },
		"bad recipient":        func(s *Settings) { s.Recipients = []string{"ops at example.com"} },
		"enabled without host": func(s *Settings) { s.SMTP.Host = "" },
		"enabled without from": func(s *Settings) { s.SMTP.From = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := validSettings()
			mutate(&s)
			if err := s.Validate(); err == nil {
				t.Fatal("expected a validation error, got nil")
			}
		})
	}
}

// TestValidateAllowsIncompleteWhenDisabled lets an operator save a
// half-filled form and come back to it later.
func TestValidateAllowsIncompleteWhenDisabled(t *testing.T) {
	s := DefaultSettings()
	if err := s.Validate(); err != nil {
		t.Fatalf("empty disabled settings must validate: %v", err)
	}
}

// TestValidateAcceptsCredentialsWithoutTLS covers the internal relay on
// port 25: a private-network setup the operator picks deliberately, and
// one the form must not refuse to save.
func TestValidateAcceptsCredentialsWithoutTLS(t *testing.T) {
	s := validSettings()
	s.SMTP.TLS = TLSNone
	s.SMTP.Port = 25
	s.SMTP.Username = "s-elevators@example.com"
	if err := s.Validate(); err != nil {
		t.Fatalf("plain relay with a username must validate: %v", err)
	}
}

func TestValidateAcceptsZeroRepeatInterval(t *testing.T) {
	s := validSettings()
	s.RepeatInterval = 0
	if err := s.Validate(); err != nil {
		t.Fatalf("reminders off must be valid: %v", err)
	}
}

func TestDeliveryForInheritsGlobalList(t *testing.T) {
	s := validSettings()
	got := s.DeliveryFor("ke", nil)
	if !got.Enabled || !reflect.DeepEqual(got.Recipients, []string{"ops@example.com"}) {
		t.Fatalf("delivery = %+v", got)
	}
}

// TestDeliveryForOwnListReplaces is the semantic the operator picked:
// an organization with its own addresses does not also copy the global
// ones.
func TestDeliveryForOwnListReplaces(t *testing.T) {
	s := validSettings()
	overrides := map[string]OrgSettings{
		"ke": {Enabled: true, Recipients: []string{"ke-operator@example.com"}},
	}
	got := s.DeliveryFor("ke", overrides)
	if !reflect.DeepEqual(got.Recipients, []string{"ke-operator@example.com"}) {
		t.Fatalf("recipients = %#v", got.Recipients)
	}
	if other := s.DeliveryFor("pde", overrides); !reflect.DeepEqual(other.Recipients, []string{"ops@example.com"}) {
		t.Fatalf("untouched org must keep the global list, got %#v", other.Recipients)
	}
}

func TestDeliveryForDisabled(t *testing.T) {
	s := validSettings()
	if got := s.DeliveryFor("ke", map[string]OrgSettings{"ke": {Enabled: false}}); got.Enabled {
		t.Fatal("per-org switch must win")
	}
	off := validSettings()
	off.Enabled = false
	if got := off.DeliveryFor("ke", nil); got.Enabled {
		t.Fatal("global switch must win")
	}
	empty := validSettings()
	empty.Recipients = nil
	if got := empty.DeliveryFor("ke", nil); got.Enabled {
		t.Fatal("no recipients must resolve to disabled")
	}
}

func TestGroupByRecipients(t *testing.T) {
	global := []string{"ops@example.com"}
	own := []string{"ke-operator@example.com"}
	events := []Event{
		{Kind: KindDown, Device: ke, At: now},
		{Kind: KindDown, Device: pde, At: now},
		{Kind: KindRecovered, Device: Device{OrganizationID: "sm", Host: "10.36.40.102"}, At: now},
	}
	batches := GroupByRecipients(events, func(orgID string) []string {
		switch orgID {
		case "ke":
			return own
		case "sm":
			return nil // no recipients: dropped
		default:
			return global
		}
	})
	if len(batches) != 2 {
		t.Fatalf("batches = %d, want 2", len(batches))
	}
	byKey := map[string][]Event{}
	for _, b := range batches {
		byKey[b.Recipients[0]] = b.Events
	}
	if len(byKey["ke-operator@example.com"]) != 1 || len(byKey["ops@example.com"]) != 1 {
		t.Fatalf("unexpected grouping: %#v", byKey)
	}
}

// TestGroupByRecipientsCollapsesToOneEmail keeps the original promise:
// with everyone on the global list a site-wide outage is one message.
func TestGroupByRecipientsCollapsesToOneEmail(t *testing.T) {
	global := []string{"boss@example.com", "ops@example.com"}
	events := []Event{
		{Kind: KindDown, Device: ke, At: now},
		{Kind: KindDown, Device: pde, At: now},
	}
	batches := GroupByRecipients(events, func(string) []string {
		// Same addresses, different order: must still be one batch.
		return []string{global[1], global[0]}
	})
	if len(batches) != 1 {
		t.Fatalf("batches = %d, want 1", len(batches))
	}
	if len(batches[0].Events) != 2 {
		t.Fatalf("events = %d, want 2", len(batches[0].Events))
	}
	if !reflect.DeepEqual(batches[0].Recipients, global) {
		t.Fatalf("recipients = %#v", batches[0].Recipients)
	}
}

func TestSettingsFromConfig(t *testing.T) {
	notify := false
	got := SettingsFromConfig(config.Alerts{
		Enabled:        true,
		CheckInterval:  2 * time.Minute,
		StaleAfter:     15 * time.Minute,
		RepeatInterval: 3 * time.Hour,
		NotifyRecovery: &notify,
		SMTP: config.SMTP{
			Host:     "smtp.example.com",
			Port:     465,
			TLS:      config.SMTPTLSImplicit,
			Username: "alerts@example.com",
			From:     "alerts@example.com",
			To:       []string{"ops@example.com"},
			Timeout:  30 * time.Second,
		},
	})
	if !got.Enabled || got.NotifyRecovery {
		t.Fatalf("flags = %+v", got)
	}
	if got.CheckInterval.Duration() != 2*time.Minute || got.RepeatInterval.Duration() != 3*time.Hour {
		t.Fatalf("intervals = %v / %v", got.CheckInterval, got.RepeatInterval)
	}
	if got.SMTP.TLS != TLSImplicit || got.SMTP.Port != 465 {
		t.Fatalf("smtp = %+v", got.SMTP)
	}
	if !reflect.DeepEqual(got.Recipients, []string{"ops@example.com"}) {
		t.Fatalf("recipients = %#v", got.Recipients)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("config-derived settings must validate: %v", err)
	}
}

// TestSettingsFromConfigNegativeRepeat translates the YAML "reminders
// off" spelling into the Settings one.
func TestSettingsFromConfigNegativeRepeat(t *testing.T) {
	got := SettingsFromConfig(config.Alerts{RepeatInterval: -time.Second})
	if got.RepeatInterval != 0 {
		t.Fatalf("repeat_interval = %v, want 0", got.RepeatInterval)
	}
}
