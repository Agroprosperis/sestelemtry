// Package outages reads each station's scheduled grid outages — the hours
// ahead its operator plans to cut it off — so the day chart can show them
// before they happen.
//
// Vinnytsiaoblenergo and Sumyoblenergo run their consumer cabinets on the
// e-svitlo platform: the console signs in to the company's cabinet once
// per operator, and the cabinet answers a metering point's planned
// outages by its EIC code. The operators' public schedule sites sit
// behind a bot check this package does not get round.
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

// Account is the company's cabinet at one operator: the login it signs in
// with (email or phone). The password is stored and passed apart, so
// every read of it is a call a reviewer can grep.
type Account struct {
	Host  string `json:"host"`
	Login string `json:"login"`
}

// Point is a station's metering point: its EIC code and the operator
// whose cabinet knows it.
type Point struct {
	OrganizationID string `json:"organization_id"`
	EIC            string `json:"eic"`
	Host           string `json:"host"`
}

// eicRe is an EIC code: 16 Latin capitals, digits or dashes. A Cyrillic
// look-alike letter would never match in the cabinet, so it is refused
// here.
var eicRe = regexp.MustCompile(`^[0-9A-Z-]{16}$`)

// NormalizeEIC trims and upper-cases an EIC code as typed.
func NormalizeEIC(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

// Validate refuses a point the cabinet could not be asked about. Messages
// are shown to the engineer as-is.
func (p Point) Validate() error {
	if !eicRe.MatchString(p.EIC) {
		return errors.New("EIC — 16 латинських літер і цифр, як у договорі, наприклад 62Z…")
	}
	if _, ok := OperatorFor(p.Host); !ok {
		return fmt.Errorf("невідомий кабінет %q", p.Host)
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

// Read is the outcome of the last time a point's schedule was read.
type Read struct {
	AttemptedAt time.Time `json:"attempted_at"`
	SucceededAt time.Time `json:"succeeded_at"`
	Error       string    `json:"error,omitempty"`
}
