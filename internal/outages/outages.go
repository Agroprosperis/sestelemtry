// Package outages reads each station's scheduled grid outages — the hours
// ahead its operator plans to cut it off — so the day chart can show them
// before they happen.
//
// Vinnytsiaoblenergo and Sumyoblenergo run their consumer cabinets on the
// e-svitlo platform. Each elevator is its own consumer with its own
// cabinet: the console signs in with the login its passport holds, and
// the cabinet lists the outages of that consumer's metering points. The
// operators' public schedule sites sit behind a bot check this package
// does not get round.
package outages

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Operator is a distribution operator whose cabinet runs on e-svitlo.
type Operator struct {
	Host string `json:"host"`
	Name string `json:"name"`
}

// Operators are the cabinets the console can sign in to.
var Operators = []Operator{
	{Host: "vn.e-svitlo.com.ua", Name: "Вінницяобленерго"},
	{Host: "sm.e-svitlo.com.ua", Name: "Сумиобленерго"},
}

// OperatorFor returns the operator whose cabinet is at host.
func OperatorFor(host string) (Operator, bool) {
	for _, o := range Operators {
		if o.Host == host {
			return o, true
		}
	}
	return Operator{}, false
}

// Account is a station's cabinet: the operator it is at, the login it
// signs in with (email or phone) and the EIC of the station's metering
// point. Without an EIC no outages are read — each point has its own
// schedule. The password is stored and passed apart, so every read of
// it is a call a reviewer can grep.
type Account struct {
	OrganizationID string `json:"organization_id"`
	Host           string `json:"host"`
	Login          string `json:"login"`
	EIC            string `json:"eic"`
}

// eicRe is an EIC code: 16 Latin capitals, digits or dashes. A Cyrillic
// look-alike letter would never match in the cabinet, so it is refused
// here.
var eicRe = regexp.MustCompile(`^[0-9A-Z-]{16}$`)

// NormalizeEIC trims and upper-cases an EIC code as typed.
func NormalizeEIC(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

// Validate refuses an account the cabinet could not be asked with.
// Messages are shown on the passport as-is.
func (a Account) Validate() error {
	if _, ok := OperatorFor(a.Host); !ok {
		return fmt.Errorf("невідомий кабінет %q", a.Host)
	}
	if a.Login == "" {
		return errors.New("вкажіть логін кабінету: email або телефон")
	}
	if a.EIC == "" {
		return errors.New("вкажіть EIC точки обліку з розділу «Площадки комерційного обліку»")
	}
	if !eicRe.MatchString(a.EIC) {
		return errors.New("EIC — 16 латинських літер і цифр, як у договорі, наприклад 62Z…")
	}
	return nil
}

// Kind tells an hourly-schedule outage from any other the cabinet lists.
type Kind string

const (
	// KindSchedule is the hourly outage schedule (ГПВ).
	KindSchedule Kind = "schedule"
	// KindOther is planned work or an accident the cabinet announces.
	KindOther Kind = "other"
)

// Interval is one stretch the station is planned to be off the grid.
type Interval struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Kind  Kind      `json:"kind"`
	// EIC is the point the cabinet named for the outage, "" when it
	// named none.
	EIC string `json:"eic,omitempty"`
}

// Read is the outcome of the last time a station's schedule was read.
type Read struct {
	AttemptedAt time.Time `json:"attempted_at"`
	SucceededAt time.Time `json:"succeeded_at"`
	Error       string    `json:"error,omitempty"`
}
