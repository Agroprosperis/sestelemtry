import type { ReactNode } from 'react'
import type { EconomicsMonthlyTotals } from '../../api'
import { formatMwh, formatPercent, formatPrice, formatUah } from '../monthly/format'
import { PERIOD_WORDS, reserveSplit, uahShort, type PeriodScope } from '../monthly/rollup'

// EconomicsPvPlan is the planned PV generation of the visible period
// from /api/v1/pv-plan-summary, narrowed to what the card renders.
export type EconomicsPvPlan = {
  plannedKwh: number
  daysCovered: number
  daysExpected: number
}

type Props = {
  totals: EconomicsMonthlyTotals
  scope?: PeriodScope
  // prior holds the same rollup for the preceding period of equal
  // length; null/undefined hides every "до попереднього …" delta.
  prior?: EconomicsMonthlyTotals | null
  // pvPlan hides the plan line when null (org without a forecast flow,
  // a fully-future period, or the summary request failed).
  pvPlan?: EconomicsPvPlan | null
  // capexUah is the invested capital standing at the end of the period
  // (dated tariff schedule, falling back to the flat tariff-form value).
  capexUah?: number
  // annualizeMonths scales the period EBITDA to a year for the ROCE
  // card: 1 for the month view, months_with_data for the annual view.
  annualizeMonths?: number
}

// --- Formatting -----------------------------------------------------------

const signedPctFmt = new Intl.NumberFormat('uk-UA', {
  minimumFractionDigits: 1,
  maximumFractionDigits: 1,
  signDisplay: 'exceptZero',
})

function formatSignedPct(fraction: number): string {
  return `${signedPctFmt.format(fraction * 100)}%`
}

function formatSignedPp(fraction: number): string {
  return `${signedPctFmt.format(fraction * 100)} п.п.`
}

// ratio is the relative change (curr − prev) / prev, or null when the
// previous value can't serve as a denominator.
function ratio(curr: number, prev: number | null): number | null {
  if (prev === null || !Number.isFinite(prev) || Math.abs(prev) < 1e-9) return null
  if (!Number.isFinite(curr)) return null
  return (curr - prev) / prev
}

// --- Derived shares -------------------------------------------------------

// selfSufficiency: how much of the object's consumption was served by
// its own PV + battery instead of grid import.
function selfSufficiency(t: EconomicsMonthlyTotals): number | null {
  if (t.load_kwh <= 0) return null
  return (t.pv_to_load_kwh + t.ess_to_load_kwh) / t.load_kwh
}

// pvSelfShare: how much of the PV yield stayed on site (consumed
// directly or stored) rather than being exported.
function pvSelfShare(t: EconomicsMonthlyTotals): number | null {
  if (t.pv_kwh <= 0) return null
  return (t.pv_to_load_kwh + t.pv_to_ess_kwh) / t.pv_kwh
}

// --- Small building blocks ------------------------------------------------

// Icon renders one 24×24 stroke path in the card accent colour.
function Icon({ d }: { d: string }) {
  return (
    <svg
      viewBox="0 0 24 24"
      width="16"
      height="16"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d={d} />
    </svg>
  )
}

const ICONS = {
  chart: 'M3 21h18M7 21V9m5 12V3m5 18v-8',
  zap: 'M13 2 3 14h7l-1 8 11-13h-7z',
  database:
    'M4 6c0-1.7 3.6-3 8-3s8 1.3 8 3-3.6 3-8 3-8-1.3-8-3zm0 0v12c0 1.7 3.6 3 8 3s8-1.3 8-3V6m-16 6c0 1.7 3.6 3 8 3s8-1.3 8-3',
  wallet: 'M3 7a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2zm18 3h-6a2 2 0 0 0 0 4h6',
  trendUp: 'm3 17 6-6 4 4 8-8m0 0h-5m5 0v5',
  sun: 'M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8zM12 2v2m0 16v2M4.9 4.9l1.4 1.4m11.4 11.4 1.4 1.4M2 12h2m16 0h2M4.9 19.1l1.4-1.4m11.4-11.4 1.4-1.4',
  battery: 'M3 9a2 2 0 0 1 2-2h11a2 2 0 0 1 2 2v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2zm18 2v2M7 10v4m4-4v4',
  percent: 'M19 5 5 19M7.5 5a1.8 1.8 0 1 0 0 3.6 1.8 1.8 0 0 0 0-3.6zm9 10.4a1.8 1.8 0 1 0 0 3.6 1.8 1.8 0 0 0 0-3.6z',
  target:
    'M12 12m-9 0a9 9 0 1 0 18 0a9 9 0 1 0-18 0M12 12m-5 0a5 5 0 1 0 10 0a5 5 0 1 0-10 0M12 12m-1 0a1 1 0 1 0 2 0a1 1 0 1 0-2 0',
  home: 'M3 10.5 12 3l9 7.5M5 9.5V21h14V9.5',
  importArrow: 'M12 3v10m0 0-4-4m4 4 4-4M4 17v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2',
  exportArrow: 'M12 13V3m0 0L8 7m4-4 4 4M4 17v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2',
  shield: 'M12 3l7 3v5c0 4.5-3 8.5-7 10-4-1.5-7-5.5-7-10V6zm-3 9 2 2 4-4',
  cycle: 'M21 12a9 9 0 1 1-3-6.7M21 3v6h-6',
} as const

// InfoDot is the same "i" hover bubble the rollup sections use
// (`.economics-info` + data-tip). Local copy to avoid importing a
// component from the monthly view module (circular import).
function InfoDot({ tip }: { tip: string }) {
  return (
    <span className="economics-info" data-tip={tip} role="img" aria-label={tip}>
      i
    </span>
  )
}

type DeltaPolarity = 'up-good' | 'down-good' | 'neutral'

// DeltaBadge renders one "↑ +4,2% до попереднього періоду" line. The
// tint answers "is this change good?", not "is it positive?": a
// shrinking grid import is green even though the number is negative.
function DeltaBadge({
  value,
  polarity,
  unit,
  note,
}: {
  value: number | null
  polarity: DeltaPolarity
  unit: 'pct' | 'pp'
  note?: string
}) {
  if (value === null || !Number.isFinite(value)) return null
  const improved = polarity === 'up-good' ? value > 0 : value < 0
  const tone = polarity === 'neutral' || value === 0 ? 'flat' : improved ? 'good' : 'bad'
  const arrow = value > 0 ? '↑' : value < 0 ? '↓' : '→'
  return (
    <span className="eco-delta-line">
      <span className={`eco-delta eco-delta-${tone}`}>
        {arrow} {unit === 'pp' ? formatSignedPp(value) : formatSignedPct(value)}
      </span>
      {note ? <span className="eco-delta-note">{note}</span> : null}
    </span>
  )
}

type CardTone = 'steel' | 'blue' | 'green' | 'amber' | 'violet' | 'sky' | 'teal' | 'orange'

// Card is one KPI tile: icon chip + label / big value / badge line /
// caption block. The four rows live on shared subgrid tracks so values
// and captions stay aligned across the strip however labels wrap.
function Card({
  tone,
  icon,
  label,
  tip,
  value,
  badge,
  children,
}: {
  tone: CardTone
  icon: string
  label: string
  tip?: string
  value: string
  badge?: ReactNode
  children?: ReactNode
}) {
  return (
    <div className={`eco-card eco-tone-${tone}`}>
      <div className="eco-card-head">
        <span className="eco-card-icon">
          <Icon d={icon} />
        </span>
        <span className="eco-card-label">
          {label}
          {tip ? <InfoDot tip={tip} /> : null}
        </span>
      </div>
      <div className="eco-card-value">{value}</div>
      <div className="eco-card-badges">{badge}</div>
      <div className="eco-card-sub">{children}</div>
    </div>
  )
}

// --- The section ----------------------------------------------------------

// EconomicsTopSection is the redesigned top of the month/year economics
// dashboard: the «Економічні показники» strip (financial result of the
// project) and the «Енергетичний баланс» strip (energy totals with
// prior-period deltas). Replaces the old MonthlyKpis strips.
export function EconomicsTopSection({
  totals,
  scope = 'month',
  prior = null,
  pvPlan = null,
  capexUah,
  annualizeMonths = 1,
}: Props) {
  const w = PERIOD_WORDS[scope]
  const prevNote =
    scope === 'month' ? 'до попереднього місяця' : scope === 'year' ? 'до попереднього року' : 'до попереднього періоду'

  // Row 1 money.
  const baseline = totals.baseline_cost_uah
  const effectShare = baseline > 0 ? totals.effect_uah / baseline : NaN
  const baselineUnit = totals.load_kwh > 0 ? baseline / totals.load_kwh : NaN
  const actualUnit = totals.load_kwh > 0 ? totals.actual_cost_uah / totals.load_kwh : NaN
  const pvEffect = totals.effect_uah - totals.ess_net_uah
  const pvEffectShare = totals.effect_uah > 0 ? pvEffect / totals.effect_uah : NaN
  const essEffectShare = totals.effect_uah > 0 ? totals.ess_net_uah / totals.effect_uah : NaN

  // ROCE: the period EBITDA scaled to a full year, over the capital at
  // work. A partial period annualises optimistically — the tooltip says
  // so, and the payback view keeps the precise all-time math.
  const months = Math.max(1, annualizeMonths)
  const annualEbitda = totals.ebitda_uah * (12 / months)
  const hasCapex = Number.isFinite(capexUah) && (capexUah as number) > 0
  const roce = hasCapex ? annualEbitda / (capexUah as number) : NaN

  // Realised share of the confirmed potential: fact effect vs fact +
  // the two reserve levers (work-schedule shift + УЗЕ dispatch optimum).
  const reserve = reserveSplit(totals).total
  const potential = totals.effect_uah + reserve
  const realizationShare =
    potential > 0 && totals.effect_uah >= 0 ? Math.max(0, Math.min(1, totals.effect_uah / potential)) : NaN

  // Row 2 energy.
  const importPrice = totals.grid_import_kwh > 0 ? totals.import_cost_uah / totals.grid_import_kwh : NaN
  const exportRevenue = totals.revenue_pv_export_uah + totals.revenue_ess_export_uah
  const exportPrice = totals.grid_export_kwh > 0 ? exportRevenue / totals.grid_export_kwh : NaN
  const selfSuff = selfSufficiency(totals)
  const pvSelf = pvSelfShare(totals)
  const covered = totals.pv_to_load_kwh + totals.ess_to_load_kwh
  const pvConsumed = totals.pv_to_load_kwh + totals.pv_to_ess_kwh

  // Prior-period deltas (null hides the badge).
  const priorSelfSuff = prior ? selfSufficiency(prior) : null
  const priorPvSelf = prior ? pvSelfShare(prior) : null
  const loadDelta = prior ? ratio(totals.load_kwh, prior.load_kwh) : null
  const importDelta = prior ? ratio(totals.grid_import_kwh, prior.grid_import_kwh) : null
  const exportDelta = prior ? ratio(totals.grid_export_kwh, prior.grid_export_kwh) : null
  const pvDelta = prior ? ratio(totals.pv_kwh, prior.pv_kwh) : null
  const selfSuffDelta = selfSuff !== null && priorSelfSuff !== null ? selfSuff - priorSelfSuff : null
  const pvSelfDelta = pvSelf !== null && priorPvSelf !== null ? pvSelf - priorPvSelf : null

  // PV plan comparison (mock: «План: 450,0 МВт·год (+1,6%)»).
  const planDelta = pvPlan && pvPlan.plannedKwh > 0 ? ratio(totals.pv_kwh, pvPlan.plannedKwh) : null
  const planPartial = pvPlan !== null && pvPlan.daysCovered < pvPlan.daysExpected

  return (
    <section className="economics-top" aria-label={`Ключові показники ${w.of}`}>
      <div className="eco-kpi-section">
        <header className="eco-kpi-head">
          <span className="eco-kpi-head-icon eco-tone-green">
            <Icon d={ICONS.chart} />
          </span>
          <div className="eco-kpi-head-text">
            <h3 className="eco-kpi-title">Економічні показники</h3>
            <span className="eco-kpi-subtitle">Фінансовий результат та ефективність проєкту {w.per}</span>
          </div>
        </header>
        <div className="eco-kpi-grid">
          <Card
            tone="steel"
            icon={ICONS.database}
            label="Базова вартість (без проєкту)"
            tip="Усе споживання об'єкта, оцінене повною ціною імпорту кожної години: скільки коштувала б енергія, якби СЕС та УЗЕ не було."
            value={formatUah(baseline)}
          >
            <span>100% споживання з мережі · {formatMwh(totals.load_kwh)}</span>
            <span>{formatPrice(baselineUnit)} грн/кВт·год</span>
          </Card>

          <Card
            tone="blue"
            icon={ICONS.wallet}
            label="Фактична вартість"
            tip="Фактичні витрати на енергію: імпорт мінус дохід від експорту плюс знос УЗЕ."
            value={formatUah(totals.actual_cost_uah)}
            badge={
              <DeltaBadge
                value={Number.isFinite(effectShare) ? -effectShare : null}
                polarity="down-good"
                unit="pct"
                note="до базового"
              />
            }
          >
            <span>
              {formatPrice(actualUnit)} грн/кВт·год{' '}
              {Number.isFinite(baselineUnit) ? `(без проєкту ${formatPrice(baselineUnit)})` : ''}
            </span>
          </Card>

          <Card
            tone="green"
            icon={ICONS.trendUp}
            label="Економічний ефект"
            tip="Базова вартість мінус фактична: наскільки дешевше обійшлася енергія завдяки СЕС та УЗЕ."
            value={formatUah(totals.effect_uah)}
            badge={
              <DeltaBadge
                value={Number.isFinite(effectShare) ? effectShare : null}
                polarity="up-good"
                unit="pct"
                note="від базової вартості"
              />
            }
          >
            <span>Зменшення витрат та додатковий дохід</span>
          </Card>

          <Card
            tone="amber"
            icon={ICONS.sun}
            label="Ефект СЕС"
            tip="Частина ефекту без УЗЕ: власне споживання СЕС замість імпорту та експорт у мережу."
            value={formatUah(pvEffect)}
            badge={
              Number.isFinite(pvEffectShare) ? (
                <span className="eco-share">{formatPercent(pvEffectShare)} від загального ефекту</span>
              ) : undefined
            }
          >
            <span>Власне споживання + експорт</span>
          </Card>

          <Card
            tone="violet"
            icon={ICONS.battery}
            label="Ефект УЗЕ"
            tip="Чистий ефект УЗЕ: розряд у споживання та мережу мінус вартість заряду і знос."
            value={formatUah(totals.ess_net_uah)}
            badge={
              Number.isFinite(essEffectShare) ? (
                <span className="eco-share">{formatPercent(essEffectShare)} від загального ефекту</span>
              ) : undefined
            }
          >
            <span>Арбітраж РДН, зменшення імпорту, peak shaving</span>
          </Card>

          <Card
            tone="sky"
            icon={ICONS.percent}
            label="ROCE (річний)"
            tip={
              'EBITDA періоду, приведена до року (×12 / кількість місяців із даними), поділена на інвестований капітал ' +
              '(CAPEX із тарифів, що діє на кінець періоду). Неповний період завищує оцінку — точна окупність на сторінці «Окупність».'
            }
            value={formatPercent(roce)}
          >
            {hasCapex ? (
              <span>Інвестований капітал: {uahShort(capexUah as number)}</span>
            ) : (
              <span>CAPEX не вказано в тарифах</span>
            )}
          </Card>

          <Card
            tone="green"
            icon={ICONS.target}
            label="Реалізація потенціалу"
            tip={
              'Факт — економічний ефект періоду. Потенціал — ефект плюс підтверджений резерв: перенесення гнучкого ' +
              'споживання на години надлишку СЕС та ідеальний диспетчинг УЗЕ (оптимум). Резерв — недовикористана можливість, не збиток.'
            }
            value={formatPercent(realizationShare)}
            badge={
              <span className="eco-progress" role="presentation">
                <span
                  className="eco-progress-fill"
                  style={{ width: `${Number.isFinite(realizationShare) ? realizationShare * 100 : 0}%` }}
                />
              </span>
            }
          >
            <span className="eco-fact-line">
              <span>Факт:</span>
              <b>{formatUah(totals.effect_uah)}</b>
            </span>
            <span className="eco-fact-line">
              <span>Потенціал:</span>
              <b>{formatUah(potential)}</b>
            </span>
            <span className="eco-fact-line">
              <span>Резерв:</span>
              <b>{formatUah(reserve)}</b>
            </span>
          </Card>
        </div>
      </div>

      <div className="eco-kpi-section">
        <header className="eco-kpi-head">
          <span className="eco-kpi-head-icon eco-tone-amber">
            <Icon d={ICONS.zap} />
          </span>
          <div className="eco-kpi-head-text">
            <h3 className="eco-kpi-title">Енергетичний баланс</h3>
            <span className="eco-kpi-subtitle">Основні показники енергетичного балансу об'єкта {w.per}</span>
          </div>
        </header>
        <div className="eco-kpi-grid">
          <Card
            tone="steel"
            icon={ICONS.home}
            label="Споживання об'єкта"
            value={formatMwh(totals.load_kwh)}
            badge={<DeltaBadge value={loadDelta} polarity="neutral" unit="pct" note={prevNote} />}
          />

          <Card
            tone="amber"
            icon={ICONS.sun}
            label="Генерація СЕС"
            value={formatMwh(totals.pv_kwh)}
            badge={<DeltaBadge value={planDelta} polarity="up-good" unit="pct" note="до плану" />}
          >
            {pvPlan ? (
              <span title={planPartial ? `План покриває ${pvPlan.daysCovered} з ${pvPlan.daysExpected} дн. періоду` : undefined}>
                План: {formatMwh(pvPlan.plannedKwh)}
                {planPartial ? ' *' : ''}
              </span>
            ) : (
              <DeltaBadge value={pvDelta} polarity="up-good" unit="pct" note={prevNote} />
            )}
          </Card>

          <Card
            tone="blue"
            icon={ICONS.importArrow}
            label="Імпорт з мережі"
            value={formatMwh(totals.grid_import_kwh)}
            badge={<DeltaBadge value={importDelta} polarity="down-good" unit="pct" note={prevNote} />}
          >
            <span>Середня ціна: {formatPrice(importPrice)} грн/кВт·год</span>
          </Card>

          <Card
            tone="orange"
            icon={ICONS.exportArrow}
            label="Експорт у мережу"
            value={formatMwh(totals.grid_export_kwh)}
            badge={<DeltaBadge value={exportDelta} polarity="up-good" unit="pct" note={prevNote} />}
          >
            <span>Дохід: {formatUah(exportRevenue)}</span>
            <span>Середня ціна: {formatPrice(exportPrice)} грн/кВт·год</span>
          </Card>

          <Card
            tone="teal"
            icon={ICONS.shield}
            label="Самозабезпечення"
            tip="Частка споживання об'єкта, покрита власними СЕС та УЗЕ замість імпорту."
            value={selfSuff === null ? '—' : formatPercent(selfSuff)}
            badge={<DeltaBadge value={selfSuffDelta} polarity="up-good" unit="pp" note={prevNote} />}
          >
            <span>
              {formatMwh(covered)} із {formatMwh(totals.load_kwh)}
            </span>
          </Card>

          <Card
            tone="green"
            icon={ICONS.cycle}
            label="Самоспоживання СЕС"
            tip="Частка виробітку СЕС, використана на об'єкті (споживання + заряд УЗЕ), а не експортована."
            value={pvSelf === null ? '—' : formatPercent(pvSelf)}
            badge={<DeltaBadge value={pvSelfDelta} polarity="up-good" unit="pp" note={prevNote} />}
          >
            <span>
              {formatMwh(pvConsumed)} із {formatMwh(totals.pv_kwh)}
            </span>
          </Card>
        </div>
      </div>
    </section>
  )
}
