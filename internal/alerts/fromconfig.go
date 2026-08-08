package alerts

import "github.com/nesh/sestelemetry/internal/config"

// SettingsFromConfig projects the YAML `alerts:` block onto Settings.
//
// The database is the source of truth once an operator saves the
// settings page, but a deployment that has never opened that page must
// still behave the way its config.yaml says. Both the API (which shows
// the fallback in the form) and the watchdog (which acts on it) go
// through this one conversion so they never disagree about what an
// unsaved deployment is configured to do.
func SettingsFromConfig(a config.Alerts) Settings {
	s := Settings{
		Enabled:        a.Enabled,
		CheckInterval:  Duration(a.CheckInterval),
		StaleAfter:     Duration(a.StaleAfter),
		NotifyRecovery: a.NotifyRecoveryEnabled(),
		SMTP: SMTPSettings{
			Host:     a.SMTP.Host,
			Port:     a.SMTP.Port,
			TLS:      string(a.SMTP.TLS),
			Username: a.SMTP.Username,
			From:     a.SMTP.From,
			Timeout:  Duration(a.SMTP.Timeout),
		},
		Recipients: a.SMTP.To,
	}
	// The YAML block spells "no reminders" as a negative duration
	// because 0 there means "apply the default"; Settings has no
	// defaulting pass of its own, so 0 is the disabled value.
	if a.RepeatInterval > 0 {
		s.RepeatInterval = Duration(a.RepeatInterval)
	}
	s.Normalize()
	return s
}
