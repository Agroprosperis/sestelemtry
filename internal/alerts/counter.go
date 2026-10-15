package alerts

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Counter episode kinds the watchdog announces; they match the strings
// the stitcher stores in counter_episodes.
const (
	CounterKindDropout = "dropout"
	CounterKindFrozen  = "frozen"
)

// CounterEvent is one lifetime-counter fault to announce: a SmartLogger
// counter that fell or froze (Closed false), or came back (Closed true).
type CounterEvent struct {
	OrganizationID   string
	OrganizationName string
	MetricKey        string
	Kind             string
	Closed           bool
	StartedAt        time.Time
	EndedAt          *time.Time
	ValueBefore      float64
	ValueAfter       float64
	GapKwh           *float64
}

// counterLabels names the stitched counters for operators.
var counterLabels = map[string]string{
	"accumulated_pv_energy_yield_kwh":       "генерація СЕС",
	"accumulated_electricity_purchased_kwh": "імпорт з мережі",
	"accumulated_electricity_sold_kwh":      "експорт у мережу",
	"total_energy_charged_kwh":              "заряд УЗЕ",
	"total_energy_discharged_kwh":           "розряд УЗЕ",
}

// Label renders the organization and the counter.
func (e CounterEvent) Label() string {
	name := e.OrganizationName
	if name == "" {
		name = e.OrganizationID
	}
	counter := counterLabels[e.MetricKey]
	if counter == "" {
		counter = e.MetricKey
	}
	return name + " — " + counter
}

// BuildCounterMessage renders the counter events of one check into a
// single email. Returns ok=false when there is nothing to announce.
func BuildCounterMessage(events []CounterEvent, now time.Time, loc *time.Location) (Message, bool) {
	if len(events) == 0 {
		return Message{}, false
	}
	if loc == nil {
		loc = time.Local
	}
	var faults, recovered []CounterEvent
	for _, e := range events {
		if e.Closed {
			recovered = append(recovered, e)
		} else {
			faults = append(faults, e)
		}
	}

	var b strings.Builder
	writeCounterSection(&b, "Лічильник випав або застиг", faults, loc)
	writeCounterSection(&b, "Лічильник відновився", recovered, loc)
	fmt.Fprintf(&b, "Перевірено: %s\n", now.In(loc).Format(timestampLayout))

	var subject string
	switch {
	case len(faults) == 1 && len(recovered) == 0:
		subject = "СЕС: збій лічильника — " + faults[0].Label()
	case len(faults) == 0 && len(recovered) == 1:
		subject = "СЕС: лічильник відновився — " + recovered[0].Label()
	case len(faults) > 0:
		subject = fmt.Sprintf("СЕС: збої лічильників — %d", len(faults))
	default:
		subject = fmt.Sprintf("СЕС: лічильники відновились — %d", len(recovered))
	}
	return Message{Subject: subject, Body: b.String()}, true
}

func writeCounterSection(b *strings.Builder, title string, events []CounterEvent, loc *time.Location) {
	if len(events) == 0 {
		return
	}
	fmt.Fprintf(b, "%s (%d):\n\n", title, len(events))
	for _, e := range events {
		fmt.Fprintf(b, "  • %s\n", e.Label())
		fmt.Fprintf(b, "    %s\n\n", describeCounterEvent(e, loc))
	}
}

func describeCounterEvent(e CounterEvent, loc *time.Location) string {
	dayLayout := "02.01.2006"
	switch {
	case e.Kind == CounterKindFrozen && !e.Closed:
		return fmt.Sprintf("не змінюється з %s (показує %s кВт·год), хоча дані надходять. Перевірте лічильник і його зв'язок зі SmartLogger.",
			e.StartedAt.In(loc).Format(dayLayout), formatKwh(e.ValueBefore))
	case e.Kind == CounterKindFrozen:
		days := 0
		if e.EndedAt != nil {
			days = int(math.Round(e.EndedAt.Sub(e.StartedAt).Hours() / 24))
		}
		return fmt.Sprintf("знову змінюється; стояв %d %s з %s.", days, pluralUk(days, "день", "дні", "днів"),
			e.StartedAt.In(loc).Format(dayLayout))
	case !e.Closed:
		return fmt.Sprintf("з %s показує %s кВт·год замість %s кВт·год: SmartLogger не бачить пристрою (лічильника мережі чи інвертора). "+
			"Перевірте його зв'язок зі SmartLogger. Енергію за час обриву буде дораховано, коли значення повернеться.",
			e.StartedAt.In(loc).Format(timestampLayout), formatKwh(e.ValueAfter), formatKwh(e.ValueBefore))
	default:
		var at string
		var outage time.Duration
		if e.EndedAt != nil {
			at = e.EndedAt.In(loc).Format(timestampLayout)
			outage = e.EndedAt.Sub(e.StartedAt)
		}
		out := fmt.Sprintf("знову показує справжнє значення з %s (обрив тривав %s)", at, formatDuration(outage))
		if e.GapKwh != nil && *e.GapKwh >= 0.5 {
			out += fmt.Sprintf("; за цей час дораховано %s кВт·год", formatKwh(*e.GapKwh))
		}
		return out + "."
	}
}

// formatKwh renders whole kWh with space-separated thousands, the way
// the dashboard shows them.
func formatKwh(v float64) string {
	n := int64(math.Round(v))
	sign := ""
	if n < 0 {
		sign = "-"
		n = -n
	}
	digits := fmt.Sprintf("%d", n)
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
	}
	return sign + b.String()
}
