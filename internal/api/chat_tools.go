package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/nesh/sestelemetry/internal/aichat"
	"github.com/nesh/sestelemetry/internal/auth"
	"github.com/nesh/sestelemetry/internal/economics"
)

// chatTZ is the zone the sites keep their civil days and DAM hours in,
// the same one the economics pages pin.
const chatTZ = "Europe/Kyiv"

const (
	// chatMaxDAMDays and chatMaxWeatherDays bound the price and forecast
	// windows of one tool call; chatHourlyWeatherDays is the widest
	// forecast still answered hour by hour.
	chatMaxDAMDays        = 31
	chatMaxWeatherDays    = 7
	chatHourlyWeatherDays = 2
)

var (
	chatNoArgsSchema = json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`)
	chatDaySchema    = json.RawMessage(`{"type":"object","properties":{"date":{"type":"string","description":"День, YYYY-MM-DD"}},"required":["date"],"additionalProperties":false}`)
	chatSpanSchema   = json.RawMessage(`{"type":"object","properties":{"from":{"type":"string","description":"Перший день, YYYY-MM-DD"},"to":{"type":"string","description":"Останній день включно, YYYY-MM-DD"}},"required":["from","to"],"additionalProperties":false}`)
	chatMonthSchema  = json.RawMessage(`{"type":"object","properties":{"month":{"type":"string","description":"Місяць, YYYY-MM"}},"required":["month"],"additionalProperties":false}`)
	chatYearSchema   = json.RawMessage(`{"type":"object","properties":{"year":{"type":"string","description":"Рік, YYYY"}},"required":["year"],"additionalProperties":false}`)
)

// chatTools returns the read-only tools the request's principal gets
// on org. Every tool is bound to org and offered only where the screen
// it mirrors would open for this role; money stays out of every result
// for a role that can't open the economics mode, even where the route
// behind a tool would return it.
func (h *Handlers) chatTools(r *http.Request, org string, loc *time.Location) []aichat.Tool {
	can := func(perms ...auth.Permission) bool { return h.visibleTo(r, org, perms...) }
	money := can(auth.PermEconomicsRead)
	full := can(auth.PermAnalyticsFull)
	p := principalFrom(r.Context())
	windowOn := func(site string) func(time.Duration) bool {
		return func(window time.Duration) bool { return h.auth == nil || h.windowAllowed(p, site, window) }
	}
	windowOK := windowOn(org)
	// An economic group has no telemetry, weather or passport of its own:
	// on one the assistant reads them site by site.
	g, group := h.economicGroup(org)
	perSite := func(tool func(site string) aichat.Tool) aichat.Tool {
		if group {
			return chatSiteTool(g, tool)
		}
		return tool(org)
	}

	var tools []aichat.Tool
	if can(auth.PermAnalyticsDay) {
		tools = append(tools,
			perSite(func(site string) aichat.Tool { return h.chatCurrentTool(site, loc) }),
			perSite(func(site string) aichat.Tool { return h.chatEnergyTool(site, loc, windowOn(site)) }),
			perSite(func(site string) aichat.Tool { return h.chatWeatherTool(site, loc) }),
		)
	}
	if money || full {
		tools = append(tools, h.chatPvPlanTool(org, loc, can(auth.PermAnalyticsDay) && !group, windowOK))
	}
	if can(auth.PermService) {
		tools = append(tools, perSite(func(site string) aichat.Tool { return h.chatPassportTool(site, loc) }))
	}
	if h.economics != nil {
		if money {
			tools = append(tools, h.chatEconomicsDayTool(org, loc))
		}
		if money || full {
			tools = append(tools, h.chatEconomicsMonthTool(org, loc, money), h.chatEconomicsYearTool(org, money))
		}
		if can(auth.PermAnalyticsDay, auth.PermEconomicsRead) {
			tools = append(tools, h.chatUzePlanTool(org, loc, money))
		}
		// The portfolio is the one tool that reaches past org: it rolls
		// up every object whose economics the caller reads, exactly as
		// the portfolio page does, and only pays off with two of them.
		visible := func(o string) bool { return h.visibleTo(r, o, auth.PermEconomicsRead) }
		if h.portfolioObjects(visible) >= 2 {
			tools = append(tools, h.chatPortfolioTool(loc, visible))
		}
	}
	return append(tools, h.chatDAMTool())
}

// chatSiteTool offers a site's tool on an economic group once, with the
// site to read as an argument: the group's sites keep their own meters,
// so the model reads them one by one.
func chatSiteTool(g economics.Group, tool func(site string) aichat.Tool) aichat.Tool {
	bySite := make(map[string]aichat.Tool, len(g.Members))
	labels := make(map[string]string, len(g.Members))
	ids := make([]string, 0, len(g.Members))
	sites := make([]string, 0, len(g.Members))
	var always []string
	joined := ""
	for _, m := range g.Members {
		bySite[m.ID] = tool(m.ID)
		labels[m.ID] = cmp.Or(m.Label, m.ID)
		ids = append(ids, m.ID)
		site := m.ID + " — " + labels[m.ID]
		if m.From == "" {
			always = append(always, m.ID)
		} else {
			site += " (з " + m.From + ")"
			if joined == "" || m.From < joined {
				joined = m.From
			}
		}
		sites = append(sites, site)
	}
	base := bySite[ids[0]]
	note := fmt.Sprintf(" Об'єкт — майданчики з окремими лічильниками: %s; site — чий майданчик читати, а про весь об'єкт питай кожен.",
		strings.Join(sites, ", "))
	if joined != "" && len(always) > 0 {
		note += fmt.Sprintf(" До %s увесь об'єкт ішов через %s.", joined, strings.Join(always, ", "))
	}
	pick := func(raw json.RawMessage) (aichat.Tool, string, bool) {
		var args struct {
			Site string `json:"site"`
		}
		_ = json.Unmarshal(raw, &args)
		t, ok := bySite[args.Site]
		return t, labels[args.Site], ok
	}
	return aichat.Tool{
		Name:        base.Name,
		Description: base.Description + note,
		Parameters:  withSiteParam(base.Parameters, ids),
		Status: func(raw json.RawMessage) string {
			t, label, ok := pick(raw)
			if !ok || t.Status == nil {
				return base.Status(raw)
			}
			return t.Status(raw) + " · " + label
		},
		Call: func(ctx context.Context, raw json.RawMessage) (any, aichat.Source, error) {
			t, label, ok := pick(raw)
			if !ok {
				return nil, aichat.Source{}, fmt.Errorf("site має бути одним із: %s", strings.Join(ids, ", "))
			}
			out, src, err := t.Call(ctx, raw)
			if src.Label != "" {
				src.Label = label + " · " + src.Label
			}
			return out, src, err
		},
	}
}

// withSiteParam adds the required site argument, one of ids, to a tool's
// strict-mode schema.
func withSiteParam(schema json.RawMessage, ids []string) json.RawMessage {
	var s struct {
		Type                 string                     `json:"type"`
		Properties           map[string]json.RawMessage `json:"properties"`
		Required             []string                   `json:"required"`
		AdditionalProperties bool                       `json:"additionalProperties"`
	}
	if err := json.Unmarshal(schema, &s); err != nil {
		panic("chat tool schema: " + err.Error())
	}
	site, _ := json.Marshal(map[string]any{"type": "string", "enum": ids, "description": "Майданчик об'єкта"})
	if s.Properties == nil {
		s.Properties = map[string]json.RawMessage{}
	}
	s.Properties["site"] = site
	s.Required = append([]string{"site"}, s.Required...)
	out, _ := json.Marshal(s)
	return out
}

// portfolioObjects counts the real objects of the portfolio visible
// accepts, an economic group once.
func (h *Handlers) portfolioObjects(visible func(org string) bool) int {
	n := 0
	for _, o := range h.economicObjects(visible) {
		if o.ID != demoOrgID {
			n++
		}
	}
	return n
}

// chatReadFailed logs a storage failure and gives the model a reason it
// may repeat to the user.
func (h *Handlers) chatReadFailed(tool, org string, err error) error {
	h.log.Error("api_chat_tool", "tool", tool, "organization_id", org, "err", err)
	return errors.New("не вдалося прочитати дані; спробуй пізніше або інший період")
}

type chatMetric struct {
	Value float64 `json:"value"`
	At    string  `json:"at"`
	AgeS  int64   `json:"age_s"`
}

func (h *Handlers) chatCurrentTool(org string, loc *time.Location) aichat.Tool {
	return aichat.Tool{
		Name: "current_snapshot",
		Description: "Останні значення телеметрії об'єкта (опитування щосекунди): потужності СЕС, УЗЕ, навантаження й точки приєднання (кВт), " +
			"SOC (%), лічильники з початку доби (*_day_kwh) і накопичені лічильники. active_ess_power_kw: плюс — розряд, мінус — заряд. " +
			"Знак grid_connected_active_power_kw залежить від прошивки, тож імпорт чи експорт визначай за electricity_purchased_day_kwh " +
			"і electricity_sold_day_kwh. age_s — скільки секунд тому прийшло значення: кілька хвилин і більше означає, що зв'язок з об'єктом втрачено.",
		Parameters: chatNoArgsSchema,
		Status:     func(json.RawMessage) string { return "Читаю поточні показники" },
		Call: func(ctx context.Context, _ json.RawMessage) (any, aichat.Source, error) {
			resp, err := h.store.Current(ctx, org, nil, time.Time{})
			if err != nil {
				return nil, aichat.Source{}, h.chatReadFailed("current_snapshot", org, err)
			}
			if len(resp.Metrics) == 0 {
				return nil, aichat.Source{}, errors.New("телеметрії від об'єкта ще немає")
			}
			now := time.Now()
			metrics := make(map[string]chatMetric, len(resp.Metrics))
			for key, m := range resp.Metrics {
				metrics[key] = chatMetric{
					Value: m.Value,
					At:    m.Time.In(loc).Format("2006-01-02 15:04:05"),
					AgeS:  int64(now.Sub(m.Time) / time.Second),
				}
			}
			return map[string]any{
				"now":     now.In(loc).Format("2006-01-02 15:04:05"),
				"metrics": metrics,
			}, aichat.Source{Label: "Поточні показники"}, nil
		},
	}
}

// chatEnergyBalance is the analytics summary card of a period: the
// metered totals and the load split by source, allocated the way the
// card allocates it (web/src/dashboard/transforms/summary.ts).
type chatEnergyBalance struct {
	From               string            `json:"from"`
	To                 string            `json:"to"`
	PvKwh              float64           `json:"pv_kwh"`
	LoadKwh            float64           `json:"load_kwh"`
	GridImportKwh      float64           `json:"grid_import_kwh"`
	GridExportKwh      float64           `json:"grid_export_kwh"`
	EssChargedKwh      float64           `json:"ess_charged_kwh"`
	EssDischargedKwh   float64           `json:"ess_discharged_kwh"`
	LoadFromGridKwh    float64           `json:"load_from_grid_kwh"`
	LoadFromEssKwh     float64           `json:"load_from_ess_kwh"`
	LoadFromPvKwh      float64           `json:"load_from_pv_kwh"`
	SelfSufficiencyPct float64           `json:"self_sufficiency_pct"`
	PvExportedPct      float64           `json:"pv_exported_pct"`
	EssFlows           *EnergyFlowTotals `json:"ess_flows,omitempty"`
	FlowsMeta          *EnergyFlowMeta   `json:"flows_meta,omitempty"`
}

func newChatEnergyBalance(totals map[string]float64) chatEnergyBalance {
	pv := max(totals["accumulated_pv_energy_yield_kwh"], 0)
	export := math.Abs(totals["accumulated_electricity_sold_kwh"])
	load := math.Abs(totals["accumulated_power_consumption_kwh"])
	imp := max(totals["accumulated_electricity_purchased_kwh"], 0)
	charged := math.Abs(totals["total_energy_charged_kwh"])
	discharged := math.Abs(totals["total_energy_discharged_kwh"])

	fromGrid := min(imp, load)
	rest := max(load-fromGrid, 0)
	fromEss := min(discharged, rest)
	fromPv := max(rest-fromEss, 0)
	out := chatEnergyBalance{
		PvKwh:            pv,
		LoadKwh:          load,
		GridImportKwh:    imp,
		GridExportKwh:    export,
		EssChargedKwh:    charged,
		EssDischargedKwh: discharged,
		LoadFromGridKwh:  fromGrid,
		LoadFromEssKwh:   fromEss,
		LoadFromPvKwh:    fromPv,
	}
	if load > 0 {
		out.SelfSufficiencyPct = (fromPv + fromEss) / load * 100
	}
	if pv > 0 {
		out.PvExportedPct = export / pv * 100
	}
	return out
}

func (h *Handlers) chatEnergyTool(org string, loc *time.Location, windowOK func(time.Duration) bool) aichat.Tool {
	return aichat.Tool{
		Name: "energy_balance",
		Description: "Енергобаланс за київські доби від from до to включно (сьогодні — з початку доби до зараз), за лічильниками SmartLogger: " +
			"виробіток СЕС, споживання елеватора, імпорт і експорт, заряд і розряд УЗЕ (кВт·год); розподіл споживання за джерелами " +
			"(load_from_*, self_sufficiency_pct) — так само, як на картці підсумку; ess_flows — потоки через УЗЕ. " +
			"Нуль може означати, що лічильник за період не дав даних. flows_meta.days_covered менше за days_expected означає, " +
			"що частина днів ще не порахована.",
		Parameters: chatSpanSchema,
		Status:     func(raw json.RawMessage) string { return "Рахую енергобаланс" + chatSpanStatus(raw) },
		Call: func(ctx context.Context, raw json.RawMessage) (any, aichat.Source, error) {
			first, last, err := chatSpan(raw, loc, int(maxTimeseriesRange/(24*time.Hour)))
			if err != nil {
				return nil, aichat.Source{}, err
			}
			from, to := first, last.AddDate(0, 0, 1)
			now := time.Now()
			if !from.Before(now) {
				return nil, aichat.Source{}, errors.New("період ще не почався")
			}
			if to.After(now) {
				to = now
			}
			if !windowOK(to.Sub(from)) {
				return nil, aichat.Source{}, errors.New("цій ролі енергобаланс доступний щонайбільше за дві доби в одному запиті")
			}
			resp, err := h.store.EnergySummary(ctx, org, EnergySummaryAccumulators, from, to)
			if err != nil {
				return nil, aichat.Source{}, h.chatReadFailed("energy_balance", org, err)
			}
			h.maybeAttachEnergyFlow(ctx, &resp, org, from, to, EnergyFlowSyntheticMetrics, loc)
			out := newChatEnergyBalance(resp.Totals)
			out.From, out.To = first.Format("2006-01-02"), last.Format("2006-01-02")
			out.EssFlows, out.FlowsMeta = resp.Flows, resp.FlowsMeta
			label := "Енергобаланс " + chatSpanLabel(out.From, out.To)
			if m := resp.FlowsMeta; m != nil && m.DaysExpected > 0 && m.DaysCovered < m.DaysExpected {
				label += fmt.Sprintf(" (потоки УЗЕ за %d з %d днів)", m.DaysCovered, m.DaysExpected)
			}
			return out, aichat.Source{Label: label}, nil
		},
	}
}

// chatEnergy is the kWh side of an economics period.
type chatEnergy struct {
	PvKwh            float64 `json:"pv_kwh"`
	LoadKwh          float64 `json:"load_kwh"`
	GridImportKwh    float64 `json:"grid_import_kwh"`
	GridExportKwh    float64 `json:"grid_export_kwh"`
	EssChargedKwh    float64 `json:"ess_charged_kwh"`
	EssDischargedKwh float64 `json:"ess_discharged_kwh"`
	PvToLoadKwh      float64 `json:"pv_to_load_kwh"`
	PvToEssKwh       float64 `json:"pv_to_ess_kwh"`
	PvToGridKwh      float64 `json:"pv_to_grid_kwh"`
	GridToLoadKwh    float64 `json:"grid_to_load_kwh"`
	GridToEssKwh     float64 `json:"grid_to_ess_kwh"`
	EssToLoadKwh     float64 `json:"ess_to_load_kwh"`
	EssToGridKwh     float64 `json:"ess_to_grid_kwh"`
}

// chatMoney is the UAH side, only for a role that reads economics.
type chatMoney struct {
	EffectUah               float64 `json:"effect_uah"`
	BaselineCostUah         float64 `json:"baseline_cost_uah"`
	ActualCostUah           float64 `json:"actual_cost_uah"`
	EssNetUah               float64 `json:"ess_net_uah"`
	EbitdaUah               float64 `json:"ebitda_uah"`
	RevenueTotalUah         float64 `json:"revenue_total_uah"`
	ExpenseTotalUah         float64 `json:"expense_total_uah"`
	AvgImportPriceUahPerKwh float64 `json:"avg_import_price_uah_per_kwh"`
	AvgExportPriceUahPerKwh float64 `json:"avg_export_price_uah_per_kwh"`
}

// chatPeriodMoney adds the month/year levers of the economics AI panel:
// the work-schedule reserve (web/src/economics/monthly/rollup.ts
// reserveSplit) and the УЗЕ fact against its optimum.
type chatPeriodMoney struct {
	chatMoney
	ScheduleReserveUah float64                `json:"schedule_reserve_uah"`
	EssFactUah         float64                `json:"ess_fact_uah"`
	EssOptimumUah      float64                `json:"ess_optimum_uah"`
	EssReserveUah      float64                `json:"ess_reserve_uah"`
	EssCapturedShare   float64                `json:"ess_captured_share"`
	BestDay            *EconomicsMonthExtreme `json:"best_day,omitempty"`
	WorstDay           *EconomicsMonthExtreme `json:"worst_day,omitempty"`
}

func chatEnergyOfDay(t economics.DailyTotals) chatEnergy {
	return chatEnergy{
		PvKwh: t.PV, LoadKwh: t.Load, GridImportKwh: t.GridImport, GridExportKwh: t.GridExport,
		EssChargedKwh: t.EssCharged, EssDischargedKwh: t.EssDischarged,
		PvToLoadKwh: t.PVToLoad, PvToEssKwh: t.PVToEss, PvToGridKwh: t.PVToGrid,
		GridToLoadKwh: t.GridToLoad, GridToEssKwh: t.GridToEss, EssToLoadKwh: t.EssToLoad, EssToGridKwh: t.EssToGrid,
	}
}

func chatMoneyOfDay(t economics.DailyTotals) chatMoney {
	return chatMoney{
		EffectUah: t.Effect, BaselineCostUah: t.BaselineCost, ActualCostUah: t.ActualCost, EssNetUah: t.EssNet,
		EbitdaUah: t.Ebitda, RevenueTotalUah: t.RevenueTotal, ExpenseTotalUah: t.ExpenseTotal,
		AvgImportPriceUahPerKwh: t.AvgImportPrice, AvgExportPriceUahPerKwh: t.AvgExportPrice,
	}
}

func chatEnergyOfPeriod(t EconomicsMonthlyTotals) chatEnergy {
	return chatEnergy{
		PvKwh: t.PvKwh, LoadKwh: t.LoadKwh, GridImportKwh: t.GridImportKwh, GridExportKwh: t.GridExportKwh,
		EssChargedKwh: t.EssChargedKwh, EssDischargedKwh: t.EssDischargedKwh,
		PvToLoadKwh: t.PvToLoadKwh, PvToEssKwh: t.PvToEssKwh, PvToGridKwh: t.PvToGridKwh,
		GridToLoadKwh: t.GridToLoadKwh, GridToEssKwh: t.GridToEssKwh, EssToLoadKwh: t.EssToLoadKwh, EssToGridKwh: t.EssToGridKwh,
	}
}

func chatMoneyOfPeriod(t EconomicsMonthlyTotals) *chatPeriodMoney {
	out := &chatPeriodMoney{
		chatMoney: chatMoney{
			EffectUah: t.EffectUah, BaselineCostUah: t.BaselineCostUah, ActualCostUah: t.ActualCostUah, EssNetUah: t.EssNetUah,
			EbitdaUah: t.EbitdaUah, RevenueTotalUah: t.RevenueTotalUah, ExpenseTotalUah: t.ExpenseTotalUah,
			AvgImportPriceUahPerKwh: t.AvgImportPriceUahPerKwh, AvgExportPriceUahPerKwh: t.AvgExportPriceUahPerKwh,
		},
		ScheduleReserveUah: min(t.PvToGridKwh, t.GridToLoadKwh) * max(0, t.AvgImportPriceUahPerKwh-t.AvgExportPriceUahPerKwh),
		EssFactUah:         t.EssFactUah,
		EssOptimumUah:      t.EssOptimumUah,
		EssReserveUah:      max(0, t.EssReserveUah),
		EssCapturedShare:   t.EssCapturedShare,
	}
	if t.BestDay.Date != "" {
		best := t.BestDay
		out.BestDay = &best
	}
	if t.MinEffectDay.Date != "" {
		worst := t.MinEffectDay
		out.WorstDay = &worst
	}
	return out
}

const chatMoneyNote = " Гривні (money, *_uah) є лише для ролей з доступом до режиму «Економіка»."

const chatReserveNote = " schedule_reserve_uah — резерв графіка робіт (перенести гнучке денне навантаження на години, коли СЕС іде в мережу), " +
	"ess_reserve_uah — резерв таймінгу УЗЕ (оптимум мінус факт); так само їх рахує панель «Економіка», більший із двох — головний резерв."

type chatEconomicsHour struct {
	Hour             int      `json:"hour"`
	RdnUahPerKwh     *float64 `json:"rdn_uah_per_kwh"`
	PvKwh            float64  `json:"pv_kwh"`
	LoadKwh          float64  `json:"load_kwh"`
	GridImportKwh    float64  `json:"grid_import_kwh"`
	GridExportKwh    float64  `json:"grid_export_kwh"`
	EssChargedKwh    float64  `json:"ess_charged_kwh"`
	EssDischargedKwh float64  `json:"ess_discharged_kwh"`
	EffectUah        float64  `json:"effect_uah"`
}

type chatEconomicsDay struct {
	Date              string              `json:"date"`
	IsFinal           bool                `json:"is_final"`
	HoursWithData     int                 `json:"hours_with_data"`
	HoursMissingPrice int                 `json:"hours_missing_price"`
	Reconciled        bool                `json:"reconciled"`
	QualityFlags      []string            `json:"quality_flags,omitempty"`
	Energy            chatEnergy          `json:"energy"`
	Money             chatMoney           `json:"money"`
	Hours             []chatEconomicsHour `json:"hours"`
}

func (h *Handlers) chatEconomicsDayTool(org string, loc *time.Location) aichat.Tool {
	return aichat.Tool{
		Name: "economics_day",
		Description: "Економіка однієї київської доби, як у звіті дня режиму «Економіка»: енергія за потоками (кВт·год), " +
			"ефект проєкту проти сценарію без СЕС і УЗЕ, вартість, EBITDA, середні ціни (₴) і погодинна розбивка (hour — година початку, 0–23). " +
			"is_final=false — день ще не завершено; quality_flags — діагностика неточних лічильників.",
		Parameters: chatDaySchema,
		Status:     func(raw json.RawMessage) string { return "Читаю економіку дня" + chatDayStatus(raw) },
		Call: func(ctx context.Context, raw json.RawMessage) (any, aichat.Source, error) {
			var args struct {
				Date string `json:"date"`
			}
			if err := decodeChatArgs(raw, &args); err != nil {
				return nil, aichat.Source{}, err
			}
			day, err := chatDate(args.Date, loc)
			if err != nil {
				return nil, aichat.Source{}, err
			}
			date := day.Format("2006-01-02")
			stored, err := h.economics.GetDay(ctx, org, date, chatTZ)
			if err != nil {
				return nil, aichat.Source{}, h.chatReadFailed("economics_day", org, err)
			}
			t := stored.Totals
			out := chatEconomicsDay{
				Date: date, IsFinal: stored.IsFinal, HoursWithData: t.HoursWithData, HoursMissingPrice: t.HoursMissingPrice,
				Reconciled: t.Reconciled, QualityFlags: t.QualityFlags,
				Energy: chatEnergyOfDay(t), Money: chatMoneyOfDay(t),
				Hours: make([]chatEconomicsHour, 0, len(stored.Rows)),
			}
			for _, row := range stored.Rows {
				hr := economicsHourToJSON(row)
				if hr == nil {
					continue
				}
				out.Hours = append(out.Hours, chatEconomicsHour{
					Hour: hr.Hour, RdnUahPerKwh: hr.RdnUahPerKwh, PvKwh: hr.PvKwh, LoadKwh: hr.LoadKwh,
					GridImportKwh: hr.GridImportKwh, GridExportKwh: hr.GridExportKwh,
					EssChargedKwh: hr.EssChargedKwh, EssDischargedKwh: hr.EssDischargedKwh, EffectUah: hr.EffectUah,
				})
			}
			if len(out.Hours) == 0 {
				return nil, aichat.Source{}, fmt.Errorf("за %s немає даних економіки", aichat.HumanDay(date))
			}
			label := "Економіка дня " + aichat.HumanDay(date)
			if !stored.IsFinal {
				label += " (день не завершено)"
			}
			return out, aichat.Source{Label: label}, nil
		},
	}
}

type chatMonthDay struct {
	Date             string   `json:"date"`
	IsFinal          bool     `json:"is_final"`
	PvKwh            float64  `json:"pv_kwh"`
	LoadKwh          float64  `json:"load_kwh"`
	GridImportKwh    float64  `json:"grid_import_kwh"`
	GridExportKwh    float64  `json:"grid_export_kwh"`
	EssChargedKwh    float64  `json:"ess_charged_kwh"`
	EssDischargedKwh float64  `json:"ess_discharged_kwh"`
	RdnAvgUahPerKwh  float64  `json:"rdn_avg_uah_per_kwh"`
	EffectUah        *float64 `json:"effect_uah,omitempty"`
	EssReserveUah    *float64 `json:"ess_reserve_uah,omitempty"`
	QualityFlags     []string `json:"quality_flags,omitempty"`
}

type chatEconomicsMonth struct {
	Month             string           `json:"month"`
	DaysInMonth       int              `json:"days_in_month"`
	DaysExpected      int              `json:"days_expected"`
	DaysWithData      int              `json:"days_with_data"`
	FlaggedDays       int              `json:"flagged_days"`
	HoursMissingPrice int              `json:"hours_missing_price"`
	Energy            chatEnergy       `json:"energy"`
	Money             *chatPeriodMoney `json:"money,omitempty"`
	Days              []chatMonthDay   `json:"days"`
}

func (h *Handlers) chatEconomicsMonthTool(org string, loc *time.Location, money bool) aichat.Tool {
	desc := "Підсумок календарного місяця, як у місячному звіті режиму «Економіка»: енергія за потоками (кВт·год) і рядок на кожен день з даними. " +
		"days_expected — дні місяця, що вже минули (разом із сьогодні); days_with_data менше за нього — частина днів без даних або ще не порахована; " +
		"flagged_days — дні з неточними лічильниками."
	if money {
		desc += " money — ефект проєкту, витрати, EBITDA, середні ціни (₴)." + chatReserveNote
	} else {
		desc += chatMoneyNote
	}
	return aichat.Tool{
		Name:        "economics_month",
		Description: desc,
		Parameters:  chatMonthSchema,
		Status: func(raw json.RawMessage) string {
			return "Читаю підсумок місяця" + chatMonthStatus(raw)
		},
		Call: func(ctx context.Context, raw json.RawMessage) (any, aichat.Source, error) {
			var args struct {
				Month string `json:"month"`
			}
			if err := decodeChatArgs(raw, &args); err != nil {
				return nil, aichat.Source{}, err
			}
			month := strings.TrimSpace(args.Month)
			first, err := time.ParseInLocation("2006-01", month, loc)
			if err != nil {
				return nil, aichat.Source{}, fmt.Errorf("місяць %q має бути у форматі YYYY-MM", args.Month)
			}
			stored, err := h.economics.GetMonth(ctx, org, month, chatTZ)
			if err != nil {
				return nil, aichat.Source{}, h.chatReadFailed("economics_month", org, err)
			}
			totals := monthlyTotalsToJSON(stored.Totals)
			if totals.DaysWithData == 0 {
				return nil, aichat.Source{}, fmt.Errorf("за %s немає даних", aichat.HumanMonth(month))
			}
			// StoredMonth.DaysInMonth counts the days that have data, so
			// the calendar is worked out here.
			daysInMonth := first.AddDate(0, 1, -1).Day()
			expected := daysInMonth
			if now := time.Now().In(loc); now.Year() == first.Year() && now.Month() == first.Month() {
				expected = now.Day()
			}
			out := chatEconomicsMonth{
				Month: month, DaysInMonth: daysInMonth, DaysExpected: expected, DaysWithData: totals.DaysWithData,
				FlaggedDays: totals.FlaggedDays, HoursMissingPrice: totals.HoursMissingPrice,
				Energy: chatEnergyOfPeriod(totals),
				Days:   make([]chatMonthDay, 0, len(stored.Days)),
			}
			if money {
				out.Money = chatMoneyOfPeriod(totals)
			}
			for _, d := range stored.Days {
				j := monthlyDayToJSON(d)
				day := chatMonthDay{
					Date: j.Date, IsFinal: j.IsFinal, PvKwh: j.PvKwh, LoadKwh: j.LoadKwh,
					GridImportKwh: j.GridImportKwh, GridExportKwh: j.GridExportKwh,
					EssChargedKwh: j.EssChargedKwh, EssDischargedKwh: j.EssDischargedKwh,
					RdnAvgUahPerKwh: j.RdnAvgUahPerKwh, QualityFlags: j.QualityFlags,
				}
				if money {
					effect, reserve := j.EffectUah, max(0, j.EssReserveUah)
					day.EffectUah, day.EssReserveUah = &effect, &reserve
				}
				out.Days = append(out.Days, day)
			}
			return out, aichat.Source{Label: fmt.Sprintf("Підсумок за %s (%d з %d днів)",
				aichat.HumanMonth(month), out.DaysWithData, out.DaysExpected)}, nil
		},
	}
}

type chatYearMonth struct {
	Month            string   `json:"month"`
	DaysWithData     int      `json:"days_with_data"`
	PvKwh            float64  `json:"pv_kwh"`
	LoadKwh          float64  `json:"load_kwh"`
	GridImportKwh    float64  `json:"grid_import_kwh"`
	GridExportKwh    float64  `json:"grid_export_kwh"`
	EssChargedKwh    float64  `json:"ess_charged_kwh"`
	EssDischargedKwh float64  `json:"ess_discharged_kwh"`
	EffectUah        *float64 `json:"effect_uah,omitempty"`
	EbitdaUah        *float64 `json:"ebitda_uah,omitempty"`
	EssReserveUah    *float64 `json:"ess_reserve_uah,omitempty"`
}

type chatEconomicsYear struct {
	Year           string           `json:"year"`
	MonthsWithData int              `json:"months_with_data"`
	Energy         chatEnergy       `json:"energy"`
	Money          *chatPeriodMoney `json:"money,omitempty"`
	Months         []chatYearMonth  `json:"months"`
}

func (h *Handlers) chatEconomicsYearTool(org string, money bool) aichat.Tool {
	desc := "Підсумок календарного року, як у річному звіті режиму «Економіка»: енергія за потоками (кВт·год) за рік і помісячно."
	if money {
		desc += " money — ефект проєкту, EBITDA, витрати, середні ціни (₴)." + chatReserveNote
	} else {
		desc += chatMoneyNote
	}
	return aichat.Tool{
		Name:        "economics_year",
		Description: desc,
		Parameters:  chatYearSchema,
		Status: func(raw json.RawMessage) string {
			var args struct {
				Year string `json:"year"`
			}
			if json.Unmarshal(raw, &args) == nil && len(strings.TrimSpace(args.Year)) == 4 {
				return "Читаю підсумок за " + strings.TrimSpace(args.Year) + " рік"
			}
			return "Читаю підсумок року"
		},
		Call: func(ctx context.Context, raw json.RawMessage) (any, aichat.Source, error) {
			var args struct {
				Year string `json:"year"`
			}
			if err := decodeChatArgs(raw, &args); err != nil {
				return nil, aichat.Source{}, err
			}
			year := strings.TrimSpace(args.Year)
			if _, err := time.Parse("2006", year); err != nil {
				return nil, aichat.Source{}, fmt.Errorf("рік %q має бути у форматі YYYY", args.Year)
			}
			stored, err := h.economics.GetYear(ctx, org, year, chatTZ)
			if err != nil {
				return nil, aichat.Source{}, h.chatReadFailed("economics_year", org, err)
			}
			if stored.MonthsWithData == 0 {
				return nil, aichat.Source{}, fmt.Errorf("за %s рік немає даних", year)
			}
			totals := monthlyTotalsToJSON(stored.Totals)
			out := chatEconomicsYear{
				Year: year, MonthsWithData: stored.MonthsWithData,
				Energy: chatEnergyOfPeriod(totals),
				Months: make([]chatYearMonth, 0, len(stored.Months)),
			}
			if money {
				out.Money = chatMoneyOfPeriod(totals)
				out.Money.BestDay, out.Money.WorstDay = nil, nil
			}
			for _, m := range stored.Months {
				t := monthlyTotalsToJSON(m.Totals)
				if t.DaysWithData == 0 {
					continue
				}
				row := chatYearMonth{
					Month: m.Month, DaysWithData: t.DaysWithData, PvKwh: t.PvKwh, LoadKwh: t.LoadKwh,
					GridImportKwh: t.GridImportKwh, GridExportKwh: t.GridExportKwh,
					EssChargedKwh: t.EssChargedKwh, EssDischargedKwh: t.EssDischargedKwh,
				}
				if money {
					effect, ebitda, reserve := t.EffectUah, t.EbitdaUah, max(0, t.EssReserveUah)
					row.EffectUah, row.EbitdaUah, row.EssReserveUah = &effect, &ebitda, &reserve
				}
				out.Months = append(out.Months, row)
			}
			return out, aichat.Source{Label: fmt.Sprintf("Підсумок за %s рік (%d міс. з даними)", year, out.MonthsWithData)}, nil
		},
	}
}

type chatPlanHour struct {
	Hour              int      `json:"hour"`
	Action            string   `json:"action"`
	RecommendedEssKw  float64  `json:"recommended_ess_kw"`
	SocPctEnd         float64  `json:"soc_pct_end"`
	RecommendedLoadKw *float64 `json:"recommended_load_kw,omitempty"`
	RdnUahPerKwh      *float64 `json:"rdn_uah_per_kwh,omitempty"`
	EffectUah         *float64 `json:"effect_uah,omitempty"`
	Reason            string   `json:"reason,omitempty"`
}

type chatPlanTotals struct {
	ChargePvKwh   float64  `json:"charge_pv_kwh"`
	ChargeGridKwh float64  `json:"charge_grid_kwh"`
	DischargeKwh  float64  `json:"discharge_kwh"`
	OptimumUah    *float64 `json:"optimum_uah,omitempty"`
	FactUah       *float64 `json:"fact_uah,omitempty"`
	ReserveUah    *float64 `json:"reserve_uah,omitempty"`
	CapturedShare *float64 `json:"captured_share,omitempty"`
}

type chatUzePlan struct {
	Date        string         `json:"date"`
	SocStartPct float64        `json:"soc_start_pct"`
	CapacityKwh float64        `json:"capacity_kwh"`
	PowerKw     float64        `json:"power_kw"`
	Totals      chatPlanTotals `json:"totals"`
	Hours       []chatPlanHour `json:"hours"`
	Warnings    []string       `json:"warnings,omitempty"`
}

func (h *Handlers) chatUzePlanTool(org string, loc *time.Location, money bool) aichat.Tool {
	desc := "Оптимальний розклад УЗЕ на київську добу заднім числом — за фактичними СЕС, навантаженням і цінами РДН цього дня " +
		"(ретроспективний орієнтир, не прогноз і не підтверджений план): щогодинна дія (charge, discharge, hold), рекомендована потужність " +
		"(плюс — розряд, мінус — заряд), SOC на кінець години, причина і рекомендований графік навантаження елеватора. hour — година початку (0–23)."
	if money {
		desc += " totals: optimum_uah — найкращий можливий результат батареї, fact_uah — фактичний, reserve_uah — різниця, captured_share — частка оптимуму, яку взято."
	} else {
		desc += chatMoneyNote
	}
	return aichat.Tool{
		Name:        "uze_plan",
		Description: desc,
		Parameters:  chatDaySchema,
		Status: func(raw json.RawMessage) string {
			return "Читаю оптимальний розклад УЗЕ" + chatDayStatus(raw)
		},
		Call: func(ctx context.Context, raw json.RawMessage) (any, aichat.Source, error) {
			var args struct {
				Date string `json:"date"`
			}
			if err := decodeChatArgs(raw, &args); err != nil {
				return nil, aichat.Source{}, err
			}
			day, err := chatDate(args.Date, loc)
			if err != nil {
				return nil, aichat.Source{}, err
			}
			date := day.Format("2006-01-02")
			plan, err := h.economics.GetDayPlan(ctx, org, date, chatTZ)
			if err != nil {
				return nil, aichat.Source{}, h.chatReadFailed("uze_plan", org, err)
			}
			if !plan.Available {
				reason := "за цей день немає придатних годин або параметрів УЗЕ"
				if len(plan.Warnings) > 0 {
					reason = strings.Join(plan.Warnings, "; ")
				}
				return nil, aichat.Source{}, fmt.Errorf("розклад УЗЕ на %s недоступний: %s", aichat.HumanDay(date), reason)
			}
			out := chatUzePlan{
				Date: date, SocStartPct: plan.SocStartPct, CapacityKwh: plan.CapacityKwh, PowerKw: plan.PowerKw,
				Totals: chatPlanTotals{
					ChargePvKwh: plan.Totals.ChargePvKwh, ChargeGridKwh: plan.Totals.ChargeGridKwh, DischargeKwh: plan.Totals.DischargeKwh,
				},
				Hours:    make([]chatPlanHour, 0, len(plan.Hours)),
				Warnings: plan.Warnings,
			}
			if money {
				t := plan.Totals
				out.Totals.OptimumUah, out.Totals.FactUah = &t.OptimumUah, &t.FactUah
				out.Totals.ReserveUah, out.Totals.CapturedShare = &t.ReserveUah, &t.CapturedShare
			}
			for _, hr := range plan.Hours {
				row := chatPlanHour{
					Hour: hr.Hour, Action: hr.Action, RecommendedEssKw: hr.RecommendedEssKw, SocPctEnd: hr.SocPct,
					RecommendedLoadKw: hr.RecommendedLoadKw, RdnUahPerKwh: hr.Rdn, Reason: hr.ReasonText,
				}
				if money {
					effect := hr.EffectUah
					row.EffectUah = &effect
				}
				out.Hours = append(out.Hours, row)
			}
			return out, aichat.Source{Label: "Оптимальний розклад УЗЕ " + aichat.HumanDay(date)}, nil
		},
	}
}

type chatPvPlan struct {
	From              string   `json:"from"`
	To                string   `json:"to"`
	PlannedKwh        float64  `json:"planned_kwh"`
	DaysCovered       int      `json:"days_covered"`
	DaysExpected      int      `json:"days_expected"`
	ActualPvKwh       *float64 `json:"actual_pv_kwh,omitempty"`
	PlanDonePct       *float64 `json:"plan_done_pct,omitempty"`
	WholeElevatorFrom string   `json:"whole_elevator_from,omitempty"`
}

func (h *Handlers) chatPvPlanTool(org string, loc *time.Location, actual bool, windowOK func(time.Duration) bool) aichat.Tool {
	desc := "План (прогноз) генерації СЕС за київські доби від from до to включно — сума денних прогнозів того самого джерела, що малює план на графіку дня " +
		"(кВт·год, план кожного дня — на всю добу). Прогноз є до завтра включно: пізніші дні відкидаються, період цілком після завтра — помилка. " +
		"days_covered менше за days_expected — для частини днів прогнозу немає. " +
		"Якщо є whole_elevator_from, з цієї дати прогноз охоплює весь Дубовʼязівський елеватор, а не лише цей об'єкт."
	if actual {
		desc += " actual_pv_kwh — фактичний виробіток за лічильником від from до зараз (для сьогодні — з початку доби), plan_done_pct — виконання плану, " +
			"коли прогноз є за кожен день і період не заходить на завтра."
	}
	return aichat.Tool{
		Name:        "pv_plan",
		Description: desc,
		Parameters:  chatSpanSchema,
		Status: func(raw json.RawMessage) string {
			return "Читаю план генерації СЕС" + chatSpanStatus(raw)
		},
		Call: func(ctx context.Context, raw json.RawMessage) (any, aichat.Source, error) {
			first, last, err := chatSpan(raw, loc, int(maxTimeseriesRange/(24*time.Hour)))
			if err != nil {
				return nil, aichat.Source{}, err
			}
			from, to := first, last.AddDate(0, 0, 1)
			plan := h.pvPlanTotal(ctx, org, from, to, loc, pvPlanLastForecastDay(loc))
			if !plan.Supported {
				return nil, aichat.Source{}, errors.New("для цього об'єкта немає прогнозу генерації")
			}
			if plan.DaysExpected == 0 {
				return nil, aichat.Source{}, errors.New("прогноз генерації є лише до завтра включно")
			}
			out := chatPvPlan{
				From: plan.FromDay, To: plan.ToDay, PlannedKwh: plan.PlannedKwh,
				DaysCovered: plan.DaysCovered, DaysExpected: plan.DaysExpected, WholeElevatorFrom: plan.WholeElevatorFrom,
			}
			now := time.Now()
			end := to
			if end.After(now) {
				end = now
			}
			// Tomorrow has no meter reading yet, and a plan that includes
			// it can't be measured against today's so far.
			measurable := out.To <= startOfCivilDay(now, loc).Format("2006-01-02")
			if actual && from.Before(now) && windowOK(end.Sub(from)) {
				resp, err := h.store.EnergySummary(ctx, org, []string{"accumulated_pv_energy_yield_kwh"}, from, end)
				if err != nil {
					h.log.Warn("api_chat_tool", "tool", "pv_plan", "organization_id", org, "err", err)
				} else {
					pv := max(resp.Totals["accumulated_pv_energy_yield_kwh"], 0)
					out.ActualPvKwh = &pv
					if measurable && plan.PlannedKwh > 0 && plan.DaysCovered == plan.DaysExpected {
						done := pv / plan.PlannedKwh * 100
						out.PlanDonePct = &done
					}
				}
			}
			label := "План генерації СЕС " + chatSpanLabel(out.From, out.To)
			if out.DaysCovered < out.DaysExpected {
				label += fmt.Sprintf(" (прогноз за %d з %d днів)", out.DaysCovered, out.DaysExpected)
			}
			return out, aichat.Source{Label: label}, nil
		},
	}
}

type chatPassport struct {
	ReadAt       string   `json:"read_at"`
	PvRatedKw    *float64 `json:"pv_rated_kw,omitempty"`
	EssRatedKw   *float64 `json:"ess_rated_kw,omitempty"`
	EssRatedKwh  *float64 `json:"ess_rated_kwh,omitempty"`
	EssCabinets  *float64 `json:"ess_cabinets,omitempty"`
	PcsCount     *float64 `json:"pcs_count,omitempty"`
	EssSohPct    *float64 `json:"ess_soh_pct,omitempty"`
	QualityFlags []string `json:"quality_flags,omitempty"`
}

func (h *Handlers) chatPassportTool(org string, loc *time.Location) aichat.Tool {
	return aichat.Tool{
		Name: "station_passport",
		Description: "Паспорт станції, як на сервісній сторінці: номінальна потужність СЕС (кВт), номінальна потужність і ємність УЗЕ (кВт, кВт·год), " +
			"кількість шаф УЗЕ й PCS, стан здоров'я батареї SOH (%). Кожне поле — останнє реальне зчитування з SmartLogger; read_at — коли паспорт оновлено.",
		Parameters: chatNoArgsSchema,
		Status:     func(json.RawMessage) string { return "Читаю паспорт станції" },
		Call: func(ctx context.Context, _ json.RawMessage) (any, aichat.Source, error) {
			snap, ok, err := h.store.LatestPlantInventory(ctx, org)
			if err != nil {
				return nil, aichat.Source{}, h.chatReadFailed("station_passport", org, err)
			}
			if !ok {
				return nil, aichat.Source{}, errors.New("паспорт станції ще не зчитано")
			}
			return chatPassport{
				ReadAt:    snap.Time.In(loc).Format("2006-01-02 15:04"),
				PvRatedKw: snap.PVRatedKw, EssRatedKw: snap.ESSRatedKw, EssRatedKwh: snap.ESSRatedKwh,
				EssCabinets: snap.ESSCount, PcsCount: snap.PCSCount, EssSohPct: snap.ESSSOHPct,
				QualityFlags: snap.QualityFlags,
			}, aichat.Source{Label: "Паспорт станції"}, nil
		},
	}
}

type chatPortfolioSite struct {
	ID                 string  `json:"id"`
	Name               string  `json:"name"`
	HasData            bool    `json:"has_data"`
	MonthsWithData     int     `json:"months_with_data"`
	EffectUah          float64 `json:"effect_uah"`
	EbitdaUah          float64 `json:"ebitda_uah"`
	ScheduleReserveUah float64 `json:"schedule_reserve_uah"`
	EssReserveUah      float64 `json:"ess_reserve_uah"`
	ActionReserveUah   float64 `json:"action_reserve_uah"`
	EssDataOk          bool    `json:"ess_data_ok"`
	PvKwh              float64 `json:"pv_kwh"`
	LoadKwh            float64 `json:"load_kwh"`
	GridImportKwh      float64 `json:"grid_import_kwh"`
	GridExportKwh      float64 `json:"grid_export_kwh"`
}

func chatPortfolioSiteOf(s EconomicsPortfolioSite) chatPortfolioSite {
	return chatPortfolioSite{
		ID: s.ID, Name: s.Name, HasData: s.HasData, MonthsWithData: s.MonthsWithData,
		EffectUah: s.EffectUah, EbitdaUah: s.EbitdaUah,
		ScheduleReserveUah: s.ScheduleReserveUah, EssReserveUah: s.BessReserveUah, ActionReserveUah: s.ActionReserveUah,
		EssDataOk: s.BessDataOk,
		PvKwh:     s.PvKwh, LoadKwh: s.LoadKwh, GridImportKwh: s.GridImportKwh, GridExportKwh: s.GridExportKwh,
	}
}

var chatPeriodSchema = json.RawMessage(`{"type":"object","properties":{"period":{"type":"string","description":"Місяць YYYY-MM або рік YYYY"}},"required":["period"],"additionalProperties":false}`)

func (h *Handlers) chatPortfolioTool(loc *time.Location, visible func(org string) bool) aichat.Tool {
	return aichat.Tool{
		Name: "portfolio",
		Description: "Зведення по всіх об'єктах, економіку яких бачить користувач (не лише поточний), як на сторінці портфеля, за місяць (YYYY-MM) " +
			"або календарний рік (YYYY): ефект проєкту й EBITDA (₴), резерв графіка робіт і резерв таймінгу УЗЕ, сумарний резерв (action_reserve_uah), " +
			"енергія (кВт·год). Об'єкти відсортовані за сумарним резервом, без даних — у кінці; total — підсумок портфеля. " +
			"ess_data_ok=false — частину годин УЗЕ виключено як аномальні.",
		Parameters: chatPeriodSchema,
		Status: func(raw json.RawMessage) string {
			var args struct {
				Period string `json:"period"`
			}
			if json.Unmarshal(raw, &args) == nil && args.Period != "" {
				return "Зводжу портфель об'єктів за " + chatPeriodLabel(strings.TrimSpace(args.Period))
			}
			return "Зводжу портфель об'єктів"
		},
		Call: func(ctx context.Context, raw json.RawMessage) (any, aichat.Source, error) {
			var args struct {
				Period string `json:"period"`
			}
			if err := decodeChatArgs(raw, &args); err != nil {
				return nil, aichat.Source{}, err
			}
			period := strings.TrimSpace(args.Period)
			q := portfolioQuery{Label: period}
			if _, err := time.Parse("2006-01", period); err == nil {
				q.Scope, q.Month = "month", period
			} else if _, err := time.Parse("2006", period); err == nil {
				q.Scope, q.Period = "year", period
			} else {
				return nil, aichat.Source{}, fmt.Errorf("період %q має бути місяцем YYYY-MM або роком YYYY", args.Period)
			}
			resp := h.portfolioRollup(ctx, q, loc, visible)
			sites := make([]chatPortfolioSite, 0, len(resp.Sites))
			withData := 0
			for _, s := range resp.Sites {
				if s.HasData {
					withData++
				}
				sites = append(sites, chatPortfolioSiteOf(s))
			}
			if withData == 0 {
				return nil, aichat.Source{}, fmt.Errorf("за %s немає даних ні по одному об'єкту", chatPeriodLabel(period))
			}
			return map[string]any{
				"period": period,
				"sites":  sites,
				"total":  chatPortfolioSiteOf(resp.Totals),
			}, aichat.Source{Label: "Портфель об'єктів, " + chatPeriodLabel(period)}, nil
		},
	}
}

// chatPeriodLabel renders a YYYY-MM month or a YYYY year.
func chatPeriodLabel(period string) string {
	if len(period) == len("2006") {
		return period + " рік"
	}
	return aichat.HumanMonth(period)
}

type chatDAMHour struct {
	Start string  `json:"start"`
	Price float64 `json:"price_uah_per_mwh"`
}

type chatDAMDay struct {
	Date  string        `json:"date"`
	Avg   float64       `json:"avg_uah_per_mwh"`
	Min   float64       `json:"min_uah_per_mwh"`
	Max   float64       `json:"max_uah_per_mwh"`
	Hours []chatDAMHour `json:"hours,omitempty"`
}

func (h *Handlers) chatDAMTool() aichat.Tool {
	return aichat.Tool{
		Name: "dam_prices",
		Description: fmt.Sprintf("Ціни РДН (ринок «на добу наперед», ОРЕЕ) за дні доставки від from до to включно, ₴/МВт·год, не довше %d днів. "+
			"Для одного дня — погодинно (start — початок години за київським часом), для кількох — середня, мінімальна й максимальна ціна по днях. "+
			"Ціни на завтра з'являються близько 15:30.", chatMaxDAMDays),
		Parameters: chatSpanSchema,
		Status:     func(raw json.RawMessage) string { return "Читаю ціни РДН" + chatSpanStatus(raw) },
		Call: func(ctx context.Context, raw json.RawMessage) (any, aichat.Source, error) {
			first, last, err := chatSpan(raw, time.UTC, chatMaxDAMDays)
			if err != nil {
				return nil, aichat.Source{}, err
			}
			zone := h.damDefaultZone
			if zone <= 0 {
				zone = 2
			}
			resp, err := h.store.DAMPrices(ctx, zone, first, last)
			if err != nil {
				return nil, aichat.Source{}, h.chatReadFailed("dam_prices", "", err)
			}
			byDate := map[string]*chatDAMDay{}
			sums := map[string]float64{}
			for _, p := range resp.Prices {
				if p.PriceUAHPerMWh == nil {
					continue
				}
				price := *p.PriceUAHPerMWh
				date := p.DeliveryDate.Format("2006-01-02")
				d := byDate[date]
				if d == nil {
					d = &chatDAMDay{Date: date, Min: price, Max: price}
					byDate[date] = d
				}
				d.Min, d.Max = min(d.Min, price), max(d.Max, price)
				sums[date] += price
				d.Hours = append(d.Hours, chatDAMHour{Start: fmt.Sprintf("%02d:00", max(p.Hour-1, 0)), Price: price})
			}
			from, to := first.Format("2006-01-02"), last.Format("2006-01-02")
			if len(byDate) == 0 {
				return nil, aichat.Source{}, fmt.Errorf("цін РДН за %s у базі немає", chatSpanLabel(from, to))
			}
			days := make([]chatDAMDay, 0, len(byDate))
			for date, d := range byDate {
				d.Avg = sums[date] / float64(len(d.Hours))
				sort.Slice(d.Hours, func(i, j int) bool { return d.Hours[i].Start < d.Hours[j].Start })
				if from != to {
					d.Hours = nil
				}
				days = append(days, *d)
			}
			sort.Slice(days, func(i, j int) bool { return days[i].Date < days[j].Date })
			return map[string]any{"zone": zone, "days": days}, aichat.Source{Label: "Ціни РДН " + chatSpanLabel(from, to)}, nil
		},
	}
}

type chatWeatherDay struct {
	Day                 string   `json:"day"`
	Sunrise             string   `json:"sunrise,omitempty"`
	Sunset              string   `json:"sunset,omitempty"`
	SunshineHours       *float64 `json:"sunshine_hours,omitempty"`
	RadiationSumMJPerM2 *float64 `json:"shortwave_radiation_sum_mj_m2,omitempty"`
}

type chatWeatherHour struct {
	Time          string   `json:"time"`
	TemperatureC  *float64 `json:"temperature_c,omitempty"`
	CloudCoverPct *float64 `json:"cloud_cover_pct,omitempty"`
	ShortwaveWm2  *float64 `json:"shortwave_wm2,omitempty"`
}

func (h *Handlers) chatWeatherTool(org string, loc *time.Location) aichat.Tool {
	return aichat.Tool{
		Name: "weather_forecast",
		Description: fmt.Sprintf("Прогноз погоди Open-Meteo для координат об'єкта за дні від from до to включно (не довше %d днів; минулі дні — "+
			"останній прогноз, що був на той день): схід і захід сонця, тривалість сонячного сяйва, сумарна сонячна радіація; для періоду до %d днів — "+
			"ще й погодинно температура, хмарність і радіація (Вт/м²). Пояснює, чому СЕС виробила менше чи більше.", chatMaxWeatherDays, chatHourlyWeatherDays),
		Parameters: chatSpanSchema,
		Status:     func(json.RawMessage) string { return "Читаю прогноз погоди" },
		Call: func(ctx context.Context, raw json.RawMessage) (any, aichat.Source, error) {
			first, last, err := chatSpan(raw, time.UTC, chatMaxWeatherDays)
			if err != nil {
				return nil, aichat.Source{}, err
			}
			resp, err := h.store.WeatherForecast(ctx, org, first, last.Add(24*time.Hour-time.Nanosecond))
			if err != nil {
				return nil, aichat.Source{}, h.chatReadFailed("weather_forecast", org, err)
			}
			if len(resp.Daily) == 0 && len(resp.Hourly) == 0 {
				return nil, aichat.Source{}, errors.New("прогнозу погоди для об'єкта немає: не задано координати або збір погоди вимкнено")
			}
			days := make([]chatWeatherDay, 0, len(resp.Daily))
			for _, d := range resp.Daily {
				row := chatWeatherDay{Day: d.Day.Format("2006-01-02"), RadiationSumMJPerM2: d.ShortwaveRadiationSum}
				if d.Sunrise != nil {
					row.Sunrise = d.Sunrise.In(loc).Format("15:04")
				}
				if d.Sunset != nil {
					row.Sunset = d.Sunset.In(loc).Format("15:04")
				}
				if d.SunshineDurationS != nil {
					hours := *d.SunshineDurationS / 3600
					row.SunshineHours = &hours
				}
				days = append(days, row)
			}
			out := map[string]any{"daily": days}
			if int(last.Sub(first)/(24*time.Hour))+1 <= chatHourlyWeatherDays {
				hours := make([]chatWeatherHour, 0, len(resp.Hourly))
				for _, hr := range resp.Hourly {
					hours = append(hours, chatWeatherHour{
						Time:         hr.Hour.In(loc).Format("2006-01-02 15:04"),
						TemperatureC: hr.Temperature2mC, CloudCoverPct: hr.CloudCoverPct, ShortwaveWm2: hr.ShortwaveWm2,
					})
				}
				out["hourly"] = hours
			}
			return out, aichat.Source{Label: "Прогноз погоди Open-Meteo"}, nil
		},
	}
}

func decodeChatArgs(raw json.RawMessage, v any) error {
	if err := json.Unmarshal(raw, v); err != nil {
		return errors.New("аргументи інструмента мають бути JSON-об'єктом")
	}
	return nil
}

func chatDate(s string, loc *time.Location) (time.Time, error) {
	t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(s), loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("дата %q має бути у форматі YYYY-MM-DD", s)
	}
	return t, nil
}

// chatSpan reads {from, to}: two civil days in loc, to inclusive, at
// most maxDays apart.
func chatSpan(raw json.RawMessage, loc *time.Location, maxDays int) (first, last time.Time, err error) {
	var args struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := decodeChatArgs(raw, &args); err != nil {
		return time.Time{}, time.Time{}, err
	}
	if first, err = chatDate(args.From, loc); err != nil {
		return time.Time{}, time.Time{}, err
	}
	if last, err = chatDate(args.To, loc); err != nil {
		return time.Time{}, time.Time{}, err
	}
	if last.Before(first) {
		return time.Time{}, time.Time{}, errors.New("to має бути не раніше за from")
	}
	if days := int(math.Round(last.Sub(first).Hours()/24)) + 1; days > maxDays {
		return time.Time{}, time.Time{}, fmt.Errorf("період задовгий: щонайбільше %d днів за один запит", maxDays)
	}
	return first, last, nil
}

func chatSpanLabel(from, to string) string {
	if from == to {
		return aichat.HumanDay(from)
	}
	return aichat.HumanDay(from) + "–" + aichat.HumanDay(to)
}

// The status helpers render a tool's arguments for its progress line;
// arguments the tool will reject just leave the period out.

func chatSpanStatus(raw json.RawMessage) string {
	var args struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if json.Unmarshal(raw, &args) != nil || args.From == "" || args.To == "" {
		return ""
	}
	return " за " + chatSpanLabel(strings.TrimSpace(args.From), strings.TrimSpace(args.To))
}

func chatDayStatus(raw json.RawMessage) string {
	var args struct {
		Date string `json:"date"`
	}
	if json.Unmarshal(raw, &args) != nil || args.Date == "" {
		return ""
	}
	return " " + aichat.HumanDay(strings.TrimSpace(args.Date))
}

func chatMonthStatus(raw json.RawMessage) string {
	var args struct {
		Month string `json:"month"`
	}
	if json.Unmarshal(raw, &args) != nil || args.Month == "" {
		return ""
	}
	return " за " + aichat.HumanMonth(strings.TrimSpace(args.Month))
}
