import { useMemo } from 'react'
import type {
  EconomicsAnnualMonthMargin,
  EconomicsAnnualMonthRollup,
  EconomicsAnnualQuarter,
  EconomicsAnnualResponse,
  EconomicsMonthlyTotals,
} from '../../api'
import { formatOrganizationLabel } from '../../dashboard/config'
import { useChartChrome } from '../../theme/useChartChrome'
import {
  AiLead,
  EssDataQualityNote,
  MonthlyBalance,
  MonthlyFinance,
  OptimumInfo,
} from '../monthly/EconomicsMonthlyView'
import { EconomicsTopSection, type EconomicsPvPlan } from '../components/EconomicsTopSection'
import { EconomicsOverviewRow } from '../components/EconomicsOverviewRow'
import {
  buildAiPanel,
  HEATMAP_METRIC_TIP,
  HOURS,
  heatCellTip,
  heatTier,
  type PeriodScope,
  reserveSplit,
  signClass,
  uahShort,
  unitCostUahPerKwh,
} from '../monthly/rollup'
import { annualDetailSheet, exportFileName } from '../exportSheets'
import { downloadXlsx } from '../xlsx'
import {
  formatCycles,
  formatKwhNumber,
  formatMonthName,
  formatMonthShort,
  formatMonthTitle,
  formatMwh,
  formatPercentNumber,
  formatPeriodTitle,
  formatPrice,
  formatUah,
  formatUahNumber,
  formatYearTitle,
} from '../monthly/format'

type Props = {
  data: EconomicsAnnualResponse
  organizationID: string
  // onSelectMonth drills down to the month view of the clicked YYYY-MM.
  onSelectMonth: (month: string) => void
  // prior — попереднє вікно тієї ж довжини для дельт верхніх карток.
  prior?: EconomicsMonthlyTotals | null
  // pvPlan — планова генерація СЕС за період (null ховає рядок плану).
  pvPlan?: EconomicsPvPlan | null
  // capexUah — інвестований капітал на кінець періоду (ROCE).
  capexUah?: number
}

export function EconomicsAnnualView({ data, organizationID, onSelectMonth, prior, pvPlan, capexUah }: Props) {
  const t = data.totals
  const withData = useMemo(
    () => data.months.filter((m) => m.totals.hours_with_data > 0),
    [data.months],
  )
  const periodTitle = formatPeriodTitle(data.from, data.to) || formatYearTitle(data.period)
  const exportSlug = data.from && data.to ? `${data.from}_${data.to}` : data.period
  // A full Jan→Dec span of one calendar year reads "за рік"; any other
  // sliding window reads "за період".
  const isCalendarYear =
    data.from.slice(5) === '01' &&
    data.to.slice(5) === '12' &&
    data.from.slice(0, 4) === data.to.slice(0, 4)
  const scope: PeriodScope = isCalendarYear ? 'year' : 'period'
  return (
    <>
      <EconomicsTopSection
        totals={t}
        scope={scope}
        prior={prior}
        pvPlan={pvPlan}
        capexUah={capexUah}
        annualizeMonths={data.months_with_data}
      />
      <EconomicsOverviewRow
        totals={t}
        scope={scope}
        profile={withData.map((m) => ({
          label: formatMonthShort(m.month),
          loadKwh: m.totals.load_kwh,
          pvKwh: m.totals.pv_kwh,
          gridImportKwh: m.totals.grid_import_kwh,
          essDischargeKwh: m.totals.ess_discharged_kwh,
        }))}
      />
      <QuarterCards quarters={data.quarters} />
      <div className="economics-month-grid2">
        <MonthlyFinance totals={t} scope={scope} />
        <MonthlyBalance totals={t} scope={scope} />
      </div>
      <AnnualAiAnalysis
        totals={t}
        months={withData}
        organizationID={organizationID}
        periodTitle={periodTitle}
        scope={scope}
      />
      <MonthHourHeatmap margins={data.monthly_margin} />
      <AnnualMonthlyTable
        months={withData}
        totals={t}
        organizationID={organizationID}
        exportSlug={exportSlug}
        periodLabel={periodTitle}
        onSelectMonth={onSelectMonth}
      />
    </>
  )
}

// --- Quarter cards (SPEC §3.3) ---

const Q_ROMAN = ['I', 'II', 'III', 'IV']
const Q_COLORS = ['#2f6fed', '#12b76a', '#f59e0b', '#7c3aed']

function QuarterCards({ quarters }: { quarters: EconomicsAnnualQuarter[] }) {
  return (
    <section className="economics-quarter-grid" aria-label="Квартальні підсумки">
      {quarters.map((q) => {
        const i = (q.quarter - 1) % 4
        return (
          <article
            key={`${q.year}-${q.quarter}`}
            className="economics-card economics-quarter-card"
            style={{ borderTop: `3px solid ${Q_COLORS[i]}` }}
          >
            <div className="economics-quarter-label">{Q_ROMAN[i]} кв. {q.year}</div>
            <div className={`economics-quarter-value ${signClass(q.ebitda_uah)}`}>{formatUah(q.ebitda_uah)}</div>
            <div className="economics-quarter-note">EBITDA · {formatMwh(q.pv_kwh)} СЕС</div>
            <div className="economics-quarter-sub">ефект {formatUah(q.effect_uah)}</div>
          </article>
        )
      })}
    </section>
  )
}

// --- Month x hour-of-day marginality heatmap (SPEC §3.7) ---

const HEATMAP_TIP =
  `${HEATMAP_METRIC_TIP} Клітинка підсумовує цю годину за всі дні місяця, ` +
  'тому великі дні важать більше за малі. Наведіть — покаже розрахунок.'

export function MonthHourHeatmap({ margins }: { margins: EconomicsAnnualMonthMargin[] }) {
  return (
    <section className="economics-card economics-month-section" aria-label="Маржинальність УЗЕ по місяцях">
      <div className="economics-month-section-head">
        <h3 className="economics-month-section-title">
          Heatmap: маржинальність УЗЕ (місяць × година)
          <OptimumInfo tip={HEATMAP_TIP} />
        </h3>
        <span className="economics-month-muted">грн/кВт·год розряду</span>
      </div>
      <div className="economics-heatmap-scroll">
        <table className="economics-heatmap">
          <thead>
            <tr>
              <th className="economics-heatmap-corner">Місяць</th>
              {HOURS.map((h) => (
                <th key={h}>{String(h).padStart(2, '0')}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {margins.map((row) => (
              <tr key={row.month}>
                <th scope="row">{formatMonthShort(row.month)}</th>
                {HOURS.map((h) => {
                  const c = row.hours[h] ?? null
                  return (
                    <td
                      key={h}
                      className={heatTier(c === null ? null : c.margin_uah_per_kwh)}
                      title={
                        c === null
                          ? undefined
                          : heatCellTip(
                              `${formatMonthTitle(row.month)}, ${String(h).padStart(2, '0')}:00 за всі дні`,
                              c,
                            )
                      }
                    >
                      {c === null ? '' : Math.round(c.margin_uah_per_kwh)}
                    </td>
                  )
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="economics-month-explain">
        Клітинка — скільки заробила ця година доби за весь місяць на кожній відданій кВт·год: виручка мінус
        собівартість саме тієї енергії, що була в батареї, мінус знос, поділено на сумарний розряд години.
        Наведіть на клітинку, щоб побачити цей розрахунок у гривнях. Порожньо там, де УЗЕ не розряджався.
        Колір: сірий 0–1, світло-зелений 2–5, зелений 6–11, темно-зелений понад 12 грн/кВт·год.
      </p>
    </section>
  )
}

// --- Annual AI analysis ---
//
// Same deterministic management panel as the month view (lead + result),
// with the two headline opportunities (elevator schedule, battery timing)
// rendered as accordion items that each expand into a per-month list.

// ЗАУВАЖ: chrome МАЄ братися з useChartChrome() у КОЖНОМУ компоненті,
// який його читає. Звернення до неоголошеного `chrome` компілюється
// (це глобал браузера), але в Safari/Firefox/jsdom дає ReferenceError
// і валить цілий вью (інцидент «немає даних за рік», 2026-09-17).
function AnnualAiAnalysis({
  totals,
  months,
  organizationID,
  periodTitle,
  scope,
}: {
  totals: EconomicsMonthlyTotals
  months: EconomicsAnnualMonthRollup[]
  organizationID: string
  periodTitle: string
  scope: PeriodScope
}) {
  const chrome = useChartChrome()
  const heading = `${formatOrganizationLabel(organizationID)} · ${periodTitle}`
  const panel = useMemo(
    () =>
      buildAiPanel(totals, {
        heading,
        scope,
        periodLabel: periodTitle,
        monthsCount: months.length,
      }),
    [totals, heading, periodTitle, scope, months.length],
  )

  // Per-month elevator-schedule reserve (PV exported vs grid-served load),
  // largest first — the breakdown behind the headline schedule reserve.
  const scheduleMonths = useMemo(
    () =>
      months
        .map((m) => ({ month: m.month, reserve: reserveSplit(m.totals).elevator }))
        .filter((x) => x.reserve > 0)
        .sort((a, b) => b.reserve - a.reserve),
    [months],
  )

  // Per-month ESS dispatch reserve (optimum − fact), largest first.
  const bessMonths = useMemo(
    () =>
      months
        .filter((m) => m.totals.ess_optimum_uah > 0)
        .sort((a, b) => b.totals.ess_reserve_uah - a.totals.ess_reserve_uah),
    [months],
  )

  const [plan, warn] = panel.cards

  return (
    <section className="economics-card economics-month-section economics-ai" aria-label="AI-аналіз року">
      <div className="economics-ai-head">
        <div className="economics-ai-title">
          <span className="economics-ai-mark" aria-hidden="true">AI</span>
          <h3 className="economics-month-section-title">AI-аналіз року</h3>
        </div>
        <span className="economics-ai-badge">річний управлінський висновок</span>
      </div>

      <AiLead panel={panel} />

      <EssDataQualityNote dq={totals.ess_data_quality} />

      <div className="economics-ai-accordion">
        {plan ? (
          <details className="economics-ai-acc-item plan" open>
            <summary>
              <span className="economics-ai-status">{plan.status}</span>
              <span className="economics-ai-card-title">{plan.title}</span>
              <span className="economics-ai-impact">{plan.impact}</span>
            </summary>
            <div className="economics-ai-acc-body">
              <p className="economics-ai-action">{plan.action}</p>
              {scheduleMonths.length > 0 ? (
                <>
                  <div className="economics-ai-top-months-head">Резерв переносу робіт по місяцях</div>
                  <div className="economics-ai-days">
                    {scheduleMonths.map((x) => (
                      <span key={x.month}>
                        {formatMonthShort(x.month)} · {uahShort(x.reserve)}
                      </span>
                    ))}
                  </div>
                </>
              ) : null}
            </div>
          </details>
        ) : null}

        {warn ? (
          <details className="economics-ai-acc-item warn">
            <summary>
              <span className="economics-ai-status">{warn.status}</span>
              <span className="economics-ai-card-title">{warn.title}</span>
              <span className="economics-ai-impact">{warn.impact}</span>
            </summary>
            <div className="economics-ai-acc-body">
              <p className="economics-ai-action">{warn.action}</p>
              {bessMonths.length > 0 ? (
                <div className="economics-ai-reserve">
                  <div className="economics-ai-reserve-head">
                    <span className="economics-ai-label amber">
                      Резерв таймінгу УЗЕ по місяцях
                      <OptimumInfo tip="Оптимум − Факт за місяць. Це не збиток, а оцінка недовикористаної можливості УЗЕ (модельний максимум у межах фактичних потужності, SOC, ККД та зносу)." />
                    </span>
                    <div className="economics-optimum-legend">
                      <span><i style={{ background: '#7c3aed' }} />фактичний ефект</span>
                      <span><i style={{ background: '#f59e0b' }} />недовикористано</span>
                      <span><i style={{ background: chrome.neutral }} />оптимум</span>
                    </div>
                  </div>
                  <div className="economics-optimum-list">
                    <div className="economics-optimum-row head">
                      <span>Місяць</span>
                      <span>захоплення</span>
                      <span>опт.</span>
                      <span>факт</span>
                      <span>резерв</span>
                    </div>
                    {bessMonths.map((m) => {
                      const o = m.totals
                      const factShare = o.ess_optimum_uah > 0 ? Math.max(0, Math.min(1, o.ess_fact_uah / o.ess_optimum_uah)) : 0
                      return (
                        <div key={m.month} className="economics-optimum-row">
                          <strong>{formatMonthTitle(m.month)}</strong>
                          <div className="economics-capture-bar">
                            <span className="fact" style={{ width: `${factShare * 100}%` }} />
                            <span className="missed" style={{ width: `${(1 - factShare) * 100}%` }} />
                          </div>
                          <span>{formatUah(o.ess_optimum_uah)}</span>
                          <span className="good">{formatUah(o.ess_fact_uah)}</span>
                          <span className="amber">{formatUah(o.ess_reserve_uah)}</span>
                        </div>
                      )
                    })}
                  </div>
                  <p className="economics-month-muted economics-optimum-note">
                    Оптимум — найкращий диспетчинг у межах фактично продемонстрованих
                    можливостей УЗЕ (потужність, діапазон SOC, ККД виведені з даних кожного місяця).
                  </p>
                </div>
              ) : (
                <p className="economics-month-empty-note">Недостатньо активності УЗЕ за рік для оцінки оптимуму.</p>
              )}
            </div>
          </details>
        ) : null}
      </div>
    </section>
  )
}

// --- Per-month detail table + Excel export (SPEC §3.8) ---

// As in the monthly daily table, the unit lives on a second header line
// rather than in every cell — spelling out "кВт·год"/"грн" per row costs
// more width than the numbers and pushed the last column off screen.
const COLUMNS: { label: string; unit?: string }[] = [
  { label: 'Місяць' },
  // Same pair and order as the monthly daily table: the all-in cost of
  // one consumed kWh first, the market component beside it for context.
  { label: 'Факт. ціна', unit: 'грн/кВт·год' },
  { label: 'РДН сер.', unit: 'грн/кВт·год' },
  { label: 'СЕС', unit: 'кВт·год' },
  { label: 'Споживання', unit: 'кВт·год' },
  { label: 'Імпорт', unit: 'кВт·год' },
  { label: 'Експорт', unit: 'кВт·год' },
  { label: 'Самоспож.', unit: '%' },
  { label: 'УЗЕ цикли', unit: 'екв.' },
  // EBITDA closes the row the way it closes the monthly waterfall. The
  // project effect it used to sit beside stays in the quarter cards and
  // the finance breakdown above.
  { label: 'EBITDA', unit: 'грн' },
]

// A month's equivalent cycles are its discharge divided by the pack that
// was installed that month. Adding them up spans packs of different size
// — this plant's battery more than doubled mid-year — so the sum is not
// a wear figure for any one pack and not an intensity figure either.
const CYCLES_TOTAL_TIP =
  'Цикли за період не підсумовуються: кожен місяць нормований на ємність УЗЕ, що діяла тоді, а вона змінювалась. Дивіться цикли по окремих місяцях.'

function AnnualMonthlyTable({
  months,
  totals,
  organizationID,
  exportSlug,
  periodLabel,
  onSelectMonth,
}: {
  months: EconomicsAnnualMonthRollup[]
  totals: EconomicsMonthlyTotals
  organizationID: string
  exportSlug: string
  periodLabel: string
  onSelectMonth: (month: string) => void
}) {
  const pvSelf = totals.pv_to_load_kwh + totals.pv_to_ess_kwh
  const selfShare = totals.pv_kwh > 0 ? pvSelf / totals.pv_kwh : 0
  return (
    <section id="economics-detail-table" className="economics-table-wrap" aria-label="Помісячна деталізація року">
      <div className="economics-month-section-head">
        <h3>
          Помісячна деталізація року
          <span className="economics-table-context"> · {formatOrganizationLabel(organizationID)}</span>
        </h3>
        <button
          type="button"
          className="economics-export-btn"
          onClick={() =>
            downloadXlsx(
              exportFileName(exportSlug, organizationID),
              annualDetailSheet(months, totals, periodLabel, organizationID),
            )
          }
        >
          Вивантажити в Excel
        </button>
      </div>
      <div className="economics-table-scroll">
        <table className="economics-table economics-month-table">
          <thead>
            <tr>
              {COLUMNS.map((c) => (
                <th key={c.label}>
                  {c.label}
                  {c.unit ? <small>{c.unit}</small> : null}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {months.map((m) => {
              const o = m.totals
              return (
                <tr
                  key={m.month}
                  className="economics-row-clickable"
                  onClick={() => onSelectMonth(m.month)}
                  title="Відкрити місяць"
                >
                  <td className="economics-month-table-left">
                    {formatMonthName(m.month)}
                    {o.flagged_days > 0 && (
                      <span
                        className="economics-info economics-info-warn"
                        title={`${o.flagged_days} дн. із замерзлим або відстаючим лічильником FusionSolar — потоки та ціни цих днів приблизні. Деталі — у денній таблиці місяця.`}
                        role="img"
                        aria-label="Місяць містить дні з приблизними даними"
                      >
                        !
                      </span>
                    )}
                  </td>
                  <td>{formatPrice(unitCostUahPerKwh(o.import_cost_uah, o.load_kwh))}</td>
                  <td>{formatPrice(o.rdn_avg_uah_per_kwh)}</td>
                  <td>{formatKwhNumber(o.pv_kwh)}</td>
                  <td>{formatKwhNumber(o.load_kwh)}</td>
                  <td>{formatKwhNumber(o.grid_import_kwh)}</td>
                  <td>{formatKwhNumber(o.grid_export_kwh)}</td>
                  <td>{formatPercentNumber(o.pv_kwh > 0 ? (o.pv_to_load_kwh + o.pv_to_ess_kwh) / o.pv_kwh : 0)}</td>
                  <td>{formatCycles(o.equivalent_cycles)}</td>
                  <td className={signClass(o.ebitda_uah)}>{formatUahNumber(o.ebitda_uah)}</td>
                </tr>
              )
            })}
            <tr className="economics-table-summary-row">
              <td className="economics-month-table-left">Разом</td>
              <td>{formatPrice(unitCostUahPerKwh(totals.import_cost_uah, totals.load_kwh))}</td>
              <td>{formatPrice(totals.rdn_avg_uah_per_kwh)}</td>
              <td>{formatKwhNumber(totals.pv_kwh)}</td>
              <td>{formatKwhNumber(totals.load_kwh)}</td>
              <td>{formatKwhNumber(totals.grid_import_kwh)}</td>
              <td>{formatKwhNumber(totals.grid_export_kwh)}</td>
              <td>{formatPercentNumber(selfShare)}</td>
              <td className="cell-empty" title={CYCLES_TOTAL_TIP}>
                —
              </td>
              <td className={signClass(totals.ebitda_uah)}>{formatUahNumber(totals.ebitda_uah)}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
  )
}
