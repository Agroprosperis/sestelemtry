package alerts

import (
	"fmt"
	"strings"
	"time"
)

// Message is a rendered plain-text email: what the SMTP layer needs on
// top of the recipient list.
type Message struct {
	Subject string
	Body    string
}

// timestampLayout is a compact local-time stamp. Operators read these
// next to SCADA screens that use the same day-first ordering.
const timestampLayout = "02.01.2006 15:04:05"

// BuildMessage renders every event of one check into a single email.
//
// One message per tick is deliberate: a site-wide network drop takes out
// all seven elevators at once, and seven separate emails would bury the
// signal. Returns ok=false when there is nothing to announce.
func BuildMessage(events []Event, loc *time.Location) (Message, bool) {
	if len(events) == 0 {
		return Message{}, false
	}
	if loc == nil {
		loc = time.Local
	}

	var fresh, ongoing, recovered []Event
	for _, e := range events {
		switch e.Kind {
		case KindDown:
			fresh = append(fresh, e)
		case KindReminder:
			ongoing = append(ongoing, e)
		case KindRecovered:
			recovered = append(recovered, e)
		}
	}

	var b strings.Builder
	writeSection(&b, "Втрачено звʼязок", fresh, loc)
	writeSection(&b, "Звʼязок досі відсутній", ongoing, loc)
	writeSection(&b, "Звʼязок відновлено", recovered, loc)

	at := events[0].At
	fmt.Fprintf(&b, "Перевірено: %s\n", at.In(loc).Format(timestampLayout))

	return Message{
		Subject: buildSubject(fresh, ongoing, recovered),
		Body:    b.String(),
	}, true
}

// BuildTestMessage renders the message sent by `alert-watchdog
// -test-email`. It doubles as a deployment check: if this lands in the
// operator's inbox, the SMTP credentials, the sender and the recipient
// list are all correct, and the fleet listing confirms the daemon parsed
// the config the operator expects.
func BuildTestMessage(devices []Device, now time.Time, loc *time.Location) Message {
	if loc == nil {
		loc = time.Local
	}
	var b strings.Builder
	b.WriteString("Це тестове повідомлення системи моніторингу СЕС.\n")
	b.WriteString("Якщо ви його отримали — сповіщення про втрату звʼязку налаштовані правильно.\n\n")
	if len(devices) == 0 {
		b.WriteString("Увага: у конфігурації немає жодного пристрою для моніторингу.\n\n")
	} else {
		fmt.Fprintf(&b, "Під наглядом (%d):\n\n", len(devices))
		for _, d := range devices {
			fmt.Fprintf(&b, "  • %s\n", d.Label())
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "Надіслано: %s\n", now.In(loc).Format(timestampLayout))
	return Message{
		Subject: "СЕС: тест сповіщень",
		Body:    b.String(),
	}
}

func buildSubject(fresh, ongoing, recovered []Event) string {
	outages := append(append([]Event{}, fresh...), ongoing...)
	switch {
	case len(outages) > 0:
		prefix := "СЕС: втрачено звʼязок"
		if len(fresh) == 0 {
			prefix = "СЕС: звʼязок досі відсутній"
		}
		if len(outages) == 1 {
			return prefix + " — " + outages[0].Label()
		}
		return fmt.Sprintf("%s — %s", prefix, deviceCount(len(outages)))
	case len(recovered) == 1:
		return "СЕС: звʼязок відновлено — " + recovered[0].Label()
	default:
		return fmt.Sprintf("СЕС: звʼязок відновлено — %s", deviceCount(len(recovered)))
	}
}

func writeSection(b *strings.Builder, title string, events []Event, loc *time.Location) {
	if len(events) == 0 {
		return
	}
	fmt.Fprintf(b, "%s (%d):\n\n", title, len(events))
	for _, e := range events {
		fmt.Fprintf(b, "  • %s\n", e.Label())
		fmt.Fprintf(b, "    %s\n", describeEvent(e, loc))
	}
	b.WriteString("\n")
}

func describeEvent(e Event, loc *time.Location) string {
	if e.Kind == KindRecovered {
		return fmt.Sprintf("дані знову надходять, простій тривав %s", formatDuration(e.Duration()))
	}
	if e.LastSampleAt == nil {
		return "даних немає взагалі (жодного запису за період перевірки)"
	}
	return fmt.Sprintf("останні дані: %s, немає %s",
		e.LastSampleAt.In(loc).Format(timestampLayout),
		formatDuration(e.Duration()),
	)
}

// deviceCount renders "N пристроїв" with the Ukrainian plural forms the
// count needs (1 пристрій / 2 пристрої / 5 пристроїв).
func deviceCount(n int) string {
	return fmt.Sprintf("%d %s", n, pluralUk(n, "пристрій", "пристрої", "пристроїв"))
}

// pluralUk picks the Ukrainian plural form for n: `one` for 1, 21, 31…,
// `few` for 2-4, 22-24…, `many` for 0, 5-20, 25-30…
func pluralUk(n int, one, few, many string) string {
	if n < 0 {
		n = -n
	}
	if n%100 >= 11 && n%100 <= 14 {
		return many
	}
	switch n % 10 {
	case 1:
		return one
	case 2, 3, 4:
		return few
	default:
		return many
	}
}

// formatDuration renders an outage length in Ukrainian at the coarsest
// useful resolution: seconds never matter once a site has been dark for
// hours, and "12 хв" reads faster than "12m34.5s".
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return "менше хвилини"
	}
	if d < time.Hour {
		return fmt.Sprintf("%d хв", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		hours := int(d.Hours())
		minutes := int(d.Minutes()) - hours*60
		if minutes == 0 {
			return fmt.Sprintf("%d год", hours)
		}
		return fmt.Sprintf("%d год %d хв", hours, minutes)
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) - days*24
	if hours == 0 {
		return fmt.Sprintf("%d %s", days, pluralUk(days, "день", "дні", "днів"))
	}
	return fmt.Sprintf("%d %s %d год", days, pluralUk(days, "день", "дні", "днів"), hours)
}
