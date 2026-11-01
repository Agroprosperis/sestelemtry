package aichat

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// PromptContext is what the assistant is told about the user's place
// in the dashboard. The organization comes from the server; the screen
// and the period come from the browser and are only a hint for the
// model, which is why anything not in a known shape is dropped.
type PromptContext struct {
	OrganizationID   string
	OrganizationName string
	// Now is the current instant in the sites' zone (Europe/Kyiv).
	Now time.Time
	// View, Tab: the page and tab in the dashboard's query string.
	View, Tab string
	// Preset/Anchor describe the analytics period, Range/Anchor/From/To
	// the economics one (same keys as the dashboard's URL).
	Preset, Range, Anchor, From, To string
	// OperationStart (YYYY-MM-DD) is the first day the dashboard charts
	// for the organization.
	OperationStart string
	// NoMoney: the caller can't read economics on the organization, so
	// the tools leave money totals out.
	NoMoney bool
}

var (
	dayPattern   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	monthPattern = regexp.MustCompile(`^\d{4}-\d{2}$`)
)

var monthNames = [...]string{
	"січень", "лютий", "березень", "квітень", "травень", "червень",
	"липень", "серпень", "вересень", "жовтень", "листопад", "грудень",
}

// Instructions is the system prompt of one turn.
func Instructions(pc PromptContext) string {
	var b strings.Builder
	b.WriteString("Ти — аналітичний асистент диспетчерської панелі SESTelemetry. Допомагаєш персоналу зрозуміти, як працюють сонячна електростанція (СЕС), установка зберігання енергії (УЗЕ) і споживання елеватора на об'єкті, і що це означає в енергії та грошах.\n\n")

	name := strings.TrimSpace(pc.OrganizationName)
	if name == "" {
		name = pc.OrganizationID
	}
	fmt.Fprintf(&b, "Об'єкт: %s (код %s). Інструменти відповідають лише про цей об'єкт; зведення portfolio, якщо воно є, порівнює об'єкти, економіку яких бачить користувач. Про інший об'єкт порадь перемкнути «Об'єкт» угорі сторінки.\n", name, strings.ToUpper(pc.OrganizationID))
	fmt.Fprintf(&b, "Зараз: %s, %s за київським часом (Europe/Kyiv); дати й години в даних — теж київські.\n", pc.Now.Format("02.01.2006"), pc.Now.Format("15:04"))
	if screen := screenLabel(pc.View, pc.Tab); screen != "" {
		fmt.Fprintf(&b, "Користувач зараз на екрані %s", screen)
		if period := periodLabel(pc); period != "" {
			fmt.Fprintf(&b, ", вибраний період — %s", period)
		}
		b.WriteString(". Питання без явної дати зазвичай стосуються цього періоду.\n")
	}
	if dayPattern.MatchString(pc.OperationStart) {
		fmt.Fprintf(&b, "Дані об'єкта є з %s; за раніші дати даних немає.\n", HumanDay(pc.OperationStart))
	}

	b.WriteString(`
Правила:
- Відповідай українською, коротко і по суті: 2–6 речень або короткий список з «- ». Без заголовків, таблиць і markdown-розмітки.
- Усі числа бери лише з результатів інструментів у цій розмові. Не вигадуй, не екстраполюй і не «округлюй на око» значення. Якщо інструмент повернув помилку або порожні дані — так і скажи.
- Для питань про конкретний період спершу виклич відповідний інструмент; якщо потрібно кілька періодів чи показників — виклич кілька.
- Якщо в даних days_covered менше за days_expected, є quality_flags або день ще не завершено (is_final=false), попередь, що дані неповні.
- Одиниці: потужність — кВт, енергія — кВт·год або МВт·год, гроші — ₴, SOC — %. Ціни подавай в одиницях з назви поля: *_uah_per_mwh — ₴/МВт·год, *_uah_per_kwh — ₴/кВт·год. Дати пиши як ДД.ММ.РРРР.
- Ти лише читаєш дані. Не обіцяй і не радь самостійно змінювати режими УЗЕ, ліміти, тарифи чи маніфести: це рішення людей у режимі «Керування». Можеш пояснити, що показують дані і які є резерви.
- Якщо питання не стосується об'єкта, енергетики чи економіки — ввічливо поверни розмову до теми.
`)
	if pc.NoMoney {
		b.WriteString("- Фінансові підсумки (ефект, витрати, EBITDA) цьому користувачу недоступні, тож інструменти їх не повертають. Не оцінюй їх самостійно.\n")
	}

	b.WriteString(`
Глосарій:
- СЕС — сонячна електростанція; УЗЕ — установка зберігання енергії (батарея); SOC — рівень заряду УЗЕ.
- РДН — ринок «на добу наперед», ціни погодинні.
- Імпорт — енергія, куплена з мережі; експорт — енергія, віддана в мережу.
- Потоки енергії: pv_to_load (СЕС → елеватор), pv_to_ess (СЕС → УЗЕ), pv_to_grid (СЕС → мережа), grid_to_ess (мережа → УЗЕ), ess_to_load (УЗЕ → елеватор), ess_to_grid (УЗЕ → мережа), grid_to_load (мережа → елеватор).
- Ефект проєкту — економія проти сценарію без СЕС і УЗЕ. Оптимум УЗЕ — найкращий можливий результат батареї за фактичних цін; резерв — різниця між оптимумом і фактом.
`)
	return b.String()
}

func screenLabel(view, tab string) string {
	switch view {
	case "", "dashboard":
		return "«Система» (енергетична аналітика)"
	case "economics":
		return "«Економіка»"
	case "control":
		label := "«Керування»"
		if t := controlTabLabel(tab); t != "" {
			label += ", вкладка «" + t + "»"
		}
		return label
	case "station":
		return "«Паспорт станції»"
	case "import":
		return "«Імпорт архівів»"
	case "alerts":
		return "«Сповіщення»"
	case "users":
		return "«Користувачі»"
	}
	return ""
}

func controlTabLabel(tab string) string {
	switch tab {
	case "", "state":
		return "Стан"
	case "plan":
		return "План УЗЕ"
	case "modes":
		return "Режими"
	case "limits":
		return "Обмеження"
	case "journal":
		return "Журнал"
	}
	return ""
}

func periodLabel(pc PromptContext) string {
	switch pc.View {
	case "", "dashboard":
		return granularPeriod(pc.Preset, pc.Anchor)
	case "economics":
		switch pc.Range {
		case "payback":
			return "окупність проєкту за весь час"
		case "portfolio", "year":
			if monthPattern.MatchString(pc.From) && monthPattern.MatchString(pc.To) {
				prefix := ""
				if pc.Range == "portfolio" {
					prefix = "портфель об'єктів, "
				}
				return prefix + HumanMonth(pc.From) + " — " + HumanMonth(pc.To)
			}
			if pc.Range == "portfolio" {
				return "портфель об'єктів"
			}
			return granularPeriod("year", pc.Anchor)
		case "month":
			return granularPeriod("month", pc.Anchor)
		default:
			return granularPeriod("day", pc.Anchor)
		}
	}
	return ""
}

// granularPeriod describes the day, month or year holding anchor
// (YYYY-MM-DD).
func granularPeriod(preset, anchor string) string {
	if !dayPattern.MatchString(anchor) {
		return ""
	}
	switch preset {
	case "month":
		return "місяць " + HumanMonth(anchor[:7])
	case "year":
		return "рік " + anchor[:4]
	default:
		return "день " + HumanDay(anchor)
	}
}

// HumanDay renders a YYYY-MM-DD day as ДД.ММ.РРРР.
func HumanDay(day string) string {
	if !dayPattern.MatchString(day) {
		return day
	}
	return day[8:10] + "." + day[5:7] + "." + day[:4]
}

// HumanMonth renders a YYYY-MM month as "травень 2026".
func HumanMonth(month string) string {
	if !monthPattern.MatchString(month) {
		return month
	}
	m, err := strconv.Atoi(month[5:7])
	if err != nil || m < 1 || m > 12 {
		return month
	}
	return monthNames[m-1] + " " + month[:4]
}
