// Shared CAPEX-payback math for the "Окупність проєкту" page:
// cumulative EBITDA since the start of operation on a numeric month
// axis, extended with a seasonal month-by-month forecast to the CAPEX
// crossing (summers climb fast, winters flatten — like the fact line).

import type { EconomicsAnnualMonthRollup } from '../api'
import { formatMonthShort } from './monthly/format'
import { EPOCH_EFFECTIVE_FROM } from './orgTariffsClient'

// paybackLabel formats fractional years as "N р. M міс." (or just
// months under a year). Non-positive / non-finite input reads "—".
export function paybackLabel(years: number): string {
  if (!Number.isFinite(years) || years <= 0) return '—'
  let whole = Math.floor(years)
  let months = Math.round((years - whole) * 12)
  if (months === 12) {
    whole += 1
    months = 0
  }
  if (whole === 0) return `${months} міс.`
  return months > 0 ? `${whole} р. ${months} міс.` : `${whole} р.`
}

const axisDecimalFmt = new Intl.NumberFormat('uk-UA', { maximumFractionDigits: 1 })
const axisIntegerFmt = new Intl.NumberFormat('uk-UA', { maximumFractionDigits: 0 })

// moneyAxis picks one unit so Y ticks stay short ("3,6"); the unit is
// named once in the chart caption.
export function moneyAxis(max: number): { unit: string; tick: (v: number) => string } {
  if (Math.abs(max) >= 1_000_000) {
    return { unit: 'млн грн', tick: (v) => axisDecimalFmt.format(v / 1_000_000) }
  }
  return { unit: 'тис. грн', tick: (v) => axisIntegerFmt.format(v / 1_000) }
}

export function addMonths(yyyyMm: string, n: number): string {
  const m = /^(\d{4})-(\d{2})$/.exec(yyyyMm)
  if (!m) return yyyyMm
  const d = new Date(Number(m[1]), Number(m[2]) - 1 + n, 1)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`
}

// formatMonthYearShort renders a compact month+year tick ("Січ ’27") so
// a multi-year payback axis stays unambiguous.
export function formatMonthYearShort(monthKey: string): string {
  const short = formatMonthShort(monthKey)
  return short === monthKey ? monthKey : `${short} ’${monthKey.slice(2, 4)}`
}

// DEFAULT_SEASONALITY is the typical monthly share of annual PV output
// for Ukraine's latitudes (Jan..Dec), scaled so the mean factor is 1.
// It seeds the forecast shape until real data covers a calendar month.
const DEFAULT_SEASONALITY = [0.42, 0.6, 1.02, 1.32, 1.5, 1.5, 1.56, 1.44, 1.14, 0.78, 0.42, 0.3]

function monthIdx(monthKey: string): number {
  return Number(monthKey.slice(5, 7)) - 1
}

// monthCoverage estimates how complete a month's telemetry is (0..1]
// from hours_with_data vs the month's calendar hours. Partial months —
// commissioning mid-month, data gaps, the open current month — then
// contribute proportionally to the EBITDA pace instead of diluting it
// (a month with 20% of data used to weigh like a full month and drag
// the payback forecast far out for freshly launched sites).
function monthCoverage(m: EconomicsAnnualMonthRollup): number {
  const match = /^(\d{4})-(\d{2})$/.exec(m.month)
  if (!match) return 1
  const hoursInMonth = new Date(Number(match[1]), Number(match[2]), 0).getDate() * 24
  return Math.min(Math.max(m.totals.hours_with_data / hoursInMonth, 0.01), 1)
}

// buildSeasonalFactors blends the default insolation profile with the
// observed EBITDA of each calendar month (weighted by how many times we
// saw that month, capped at 3), then renormalizes the mean back to 1.
// Observed months may push a factor negative (e.g. opex-heavy winters);
// that is kept — the forecast should dip where the facts dip.
function buildSeasonalFactors(monthsWithData: EconomicsAnnualMonthRollup[]): number[] {
  const sum = Array<number>(12).fill(0)
  const cov = Array<number>(12).fill(0)
  let total = 0
  let weightTotal = 0
  for (const m of monthsWithData) {
    const i = monthIdx(m.month)
    const coverage = monthCoverage(m)
    sum[i] += m.totals.ebitda_uah
    cov[i] += coverage
    total += m.totals.ebitda_uah
    weightTotal += DEFAULT_SEASONALITY[i] * coverage
  }
  const base = weightTotal > 0 ? total / weightTotal : 0
  if (!(base > 0)) return DEFAULT_SEASONALITY

  const blended = DEFAULT_SEASONALITY.map((w, i) => {
    if (cov[i] < 0.05) return w
    // Observed factor per full-month equivalent of data.
    const observed = sum[i] / cov[i] / base
    const n = Math.min(cov[i], 3)
    return (observed * n + w) / (n + 1)
  })
  const mean = blended.reduce((a, b) => a + b, 0) / 12
  return mean > 0.05 ? blended.map((v) => v / mean) : DEFAULT_SEASONALITY
}

// CapexStep is one dated CAPEX value from the org's tariff schedule:
// the project's TOTAL investment in effect from `effectiveFrom`
// (YYYY-MM-DD), not the amount paid on that date. A staged project
// (an extra УЗЕ pack, more panels) therefore reads as a rising step
// line, the same way capacity and power do in the schedule table.
export type CapexStep = { effectiveFrom: string; capexUah: number }

// fundedCapexSteps keeps the versions that describe an actual investment
// stage, oldest first.
//
// The catch-all version at EPOCH_EFFECTIVE_FROM is skipped: the backend
// seeds (and re-seeds) it by copying whatever the tariff form holds, so
// it is a machine-made snapshot rather than a stage the operator dated.
// Its CAPEX can even exceed the real stages — an operator who puts the
// full project cost in the form while the dated versions still carry the
// first stage — which would draw a step *down* in 1970 and count one
// investment stage too many.
export function fundedCapexSteps(steps: CapexStep[]): CapexStep[] {
  return steps
    .filter(
      (s) =>
        s.effectiveFrom !== EPOCH_EFFECTIVE_FROM &&
        /^\d{4}-\d{2}-\d{2}$/.test(s.effectiveFrom) &&
        Number.isFinite(s.capexUah) &&
        s.capexUah > 0,
    )
    .sort((a, b) => a.effectiveFrom.localeCompare(b.effectiveFrom))
}

// capexResolver turns the dated schedule into a per-month lookup.
//
// Two rules make the numbers behave for real data: a version that takes
// effect mid-month counts for that whole month (the money left the
// account inside it), and months before the first funded version
// inherit that first value — CAPEX is spent before the plant produces
// anything, so an early version with CAPEX left at 0 means "not filled
// in", not "a free project" (which would read as instant payback).
export function capexResolver(
  steps: CapexStep[],
  fallbackUah: number,
): (monthKey: string | null) => number {
  const flat = Number.isFinite(fallbackUah) ? fallbackUah : 0
  const funded = fundedCapexSteps(steps)
  if (funded.length === 0) return () => flat
  return (monthKey) => {
    if (!monthKey) return funded[0].capexUah
    const monthEnd = `${monthKey}-31`
    let value = funded[0].capexUah
    for (const s of funded) {
      if (s.effectiveFrom > monthEnd) break
      value = s.capexUah
    }
    return value
  }
}

// CapexPaybackRow is one point of the payback chart. t is months elapsed
// since the start of operation (fractional for the exact CAPEX crossing)
// and drives a numeric X axis, so multi-year forecasts keep an honest
// time scale and reference markers cannot hit a duplicated month label.
export type CapexPaybackRow = {
  t: number
  monthKey: string | null
  // factCum carries the solid fact line (and the bridge point where the
  // forecast takes over); forecastCum carries the dashed projection.
  factCum: number | null
  forecastCum: number | null
  monthEbitda: number | null
  // capex is the investment target in effect for this month — a flat
  // line for a single-stage project, a rising staircase for a staged one.
  capex: number
  kind: 'start' | 'prior' | 'fact' | 'forecast'
}

// The forecast is drawn at most this far past today; a slower project
// still shows the dashed trajectory, just without a payback marker.
export const FORECAST_CAP_MONTHS = 180

// PaybackScenario is the conservative–optimistic payback range derived
// from the observed month-to-month EBITDA variability (± one standard
// error of the mean monthly pace). It narrows as data accumulates.
export type PaybackScenario = {
  consYears: number
  optYears: number
  consMonthKey: string | null
  optMonthKey: string | null
}

export type PaybackModel = {
  monthsWithData: EconomicsAnnualMonthRollup[]
  prior: number
  hasPrior: boolean
  priorMonths: number
  allTimeEbitda: number
  totalMonthsWithData: number
  // effectiveMonths is the data-coverage-weighted month count (a month
  // with half its hours covered counts as 0.5), used for the pace and
  // fact averages.
  effectiveMonths: number
  // annualEbitda / monthlyPace are the seasonally-adjusted run rate: the
  // observed EBITDA divided by the seasonal weight of the observed
  // months, so a summer-only window does not overstate the year.
  annualEbitda: number
  monthlyPace: number
  // seasonalFactors are the Jan..Dec multipliers (mean 1) applied to
  // monthlyPace when projecting individual future months.
  seasonalFactors: number[]
  // capexNow is the investment in effect at the end of the fact window:
  // everything put into the project so far, and the denominator for
  // "повернуто / залишилось". capexStages counts the distinct funded
  // stages, capexAvg is the time-weighted capital at work (see below).
  capexNow: number
  capexAvg: number
  capexStages: number
  paybackYears: number
  coveredShare: number
  remaining: number
  operationYears: number
  // avgAnnualRoi is the average factual annual ROI over the whole
  // operating period: allTimeEbitda / CAPEX / years. NaN when unknown.
  avgAnnualRoi: number
  paidOff: boolean
  rows: CapexPaybackRow[]
  todayT: number | null
  paybackT: number | null
  paybackMonthKey: string | null
  tMax: number
  timeOffset: number
  firstMonthKey: string | null
  lastFactMonthKey: string | null
  scenario: PaybackScenario | null
}

export function buildPaybackModel({
  capexUah,
  capexSteps,
  months,
  ebitda,
  priorEbitda,
  priorMonthsWithData,
}: {
  // capexUah is the single value from the tariff form, used when the
  // schedule carries no CAPEX at all (the pre-staging setup).
  capexUah: number
  capexSteps?: CapexStep[]
  months: EconomicsAnnualMonthRollup[]
  // ebitda is the window total (may differ from the row sum by partial
  // months); prior* describe the history before the window start.
  ebitda: number
  priorEbitda: number
  priorMonthsWithData: number
}): PaybackModel {
  const prior = Number.isFinite(priorEbitda) ? priorEbitda : 0
  const hasPrior = Math.abs(prior) > 0.5
  const priorMonths = Number.isFinite(priorMonthsWithData) ? priorMonthsWithData : 0
  // Months before the plant actually started operating can carry
  // telemetry (imported meters, consumption) with zero computed
  // economics; they'd show a flat head on the chart and dilute the
  // pace. Operation starts at the first month with real activity, so
  // trim the leading inactive months (mid-series zero months stay).
  const withHours = months.filter((m) => m.totals.hours_with_data > 0)
  const firstActive = withHours.findIndex(
    (m) => Math.abs(m.totals.ebitda_uah) > 0.5 || m.totals.pv_kwh > 0.5,
  )
  const monthsWithData = firstActive >= 0 ? withHours.slice(firstActive) : []
  const firstMonthKey = monthsWithData[0]?.month ?? null
  const lastFactMonthKey = monthsWithData[monthsWithData.length - 1]?.month ?? null

  // CAPEX is a step function of time (staged investments), so every
  // comparison below asks for the CAPEX of a specific month instead of
  // one global number. `capexNow` is what is invested as of the last
  // fact month; with no fact months at all it falls back to the latest
  // known stage.
  const capexAt = capexResolver(capexSteps ?? [], capexUah)
  const capexNow = capexAt(lastFactMonthKey ?? '9999-12')
  const capexStages = new Set(fundedCapexSteps(capexSteps ?? []).map((s) => s.capexUah)).size

  const allTimeEbitda = prior + ebitda
  const totalMonthsWithData = monthsWithData.length + priorMonths
  const seasonalFactors = buildSeasonalFactors(monthsWithData)

  // Seasonally-adjusted pace: divide the all-time EBITDA by the seasonal
  // weight of every month that produced it (prior months sit directly
  // before the first visible month), instead of the plain month count.
  // Each window month weighs by its data coverage, so a half-covered
  // month contributes half a month to the denominator.
  const windowFirstMonth = firstMonthKey
  let seasonalWeight = 0
  let effectiveMonths = priorMonths
  // capexWeighted accumulates the CAPEX in effect during each month of
  // operation, so the ROI denominator is the capital that was actually
  // at work — EBITDA earned before an expansion is not judged against
  // the enlarged investment.
  let capexWeighted = 0
  if (windowFirstMonth) {
    for (let k = 1; k <= priorMonths; k += 1) {
      const key = addMonths(windowFirstMonth, -k)
      seasonalWeight += seasonalFactors[monthIdx(key)]
      capexWeighted += capexAt(key)
    }
    for (const m of monthsWithData) {
      const coverage = monthCoverage(m)
      seasonalWeight += seasonalFactors[monthIdx(m.month)] * coverage
      effectiveMonths += coverage
      capexWeighted += capexAt(m.month) * coverage
    }
  }
  const monthlyPace = seasonalWeight > 0 ? allTimeEbitda / seasonalWeight : 0
  const annualEbitda = monthlyPace * 12
  const capexAvg = effectiveMonths > 0 ? capexWeighted / effectiveMonths : capexNow
  const coveredShare = capexNow > 0 ? Math.max(0, Math.min(allTimeEbitda / capexNow, 1)) : 0
  const remaining = Math.max(capexNow - allTimeEbitda, 0)
  // Linear estimate as a fallback; refined below to the exact seasonal
  // crossing when the forecast walk reaches CAPEX.
  let paybackYears = annualEbitda > 0 ? capexNow / annualEbitda : Infinity
  const operationYears = totalMonthsWithData / 12
  // Annualize the fact ROI over data-covered time, so partial months do
  // not understate it.
  const avgAnnualRoi =
    capexAvg > 0 && effectiveMonths > 0 ? (allTimeEbitda / capexAvg) * (12 / effectiveMonths) : NaN
  const paidOff = allTimeEbitda >= capexNow && capexNow > 0

  const rows: CapexPaybackRow[] = []
  let todayT: number | null = null
  let paybackT: number | null = null
  let paybackMonthKey: string | null = null
  const timeOffset = hasPrior && priorMonths > 0 ? priorMonths : 0

  if (firstMonthKey && lastFactMonthKey) {
    // Months since operation start: the prior window occupies [0,
    // priorMonths], each fact month of the visible window adds one.
    if (hasPrior && priorMonths > 0) {
      rows.push({
        t: 0,
        monthKey: null,
        factCum: 0,
        forecastCum: null,
        monthEbitda: null,
        capex: capexAt(addMonths(firstMonthKey, -priorMonths)),
        kind: 'start',
      })
      rows.push({
        t: priorMonths,
        monthKey: addMonths(firstMonthKey, -1),
        factCum: prior,
        forecastCum: null,
        monthEbitda: null,
        capex: capexAt(addMonths(firstMonthKey, -1)),
        kind: 'prior',
      })
    } else {
      // No prior history (or the backend didn't count its months): the
      // window opens at the start of operation with the opening balance.
      rows.push({
        t: 0,
        monthKey: null,
        factCum: prior,
        forecastCum: null,
        monthEbitda: null,
        capex: capexAt(firstMonthKey),
        kind: 'start',
      })
    }

    let t = timeOffset
    let acc = prior
    for (const m of monthsWithData) {
      acc += m.totals.ebitda_uah
      t += 1
      rows.push({
        t,
        monthKey: m.month,
        factCum: acc,
        forecastCum: null,
        monthEbitda: m.totals.ebitda_uah,
        capex: capexAt(m.month),
        kind: 'fact',
      })
    }
    todayT = t

    if (acc >= capexNow && capexNow > 0) {
      // Already paid off: mark the fact row where the line first covered
      // the CAPEX standing at that time.
      const hit = rows.find((r) => r.kind !== 'start' && (r.factCum ?? 0) >= r.capex)
      if (hit) {
        paybackT = hit.t
        paybackMonthKey = hit.monthKey
      }
    } else if (annualEbitda > 0 && capexNow > 0) {
      // Bridge point so the dashed projection continues the fact line,
      // then walk future months applying the seasonal factor of each one:
      // the projection climbs steeply through summers and flattens (or
      // dips) through winters, like the fact line does.
      rows[rows.length - 1].forecastCum = acc
      let fAcc = acc
      for (let k = 1; k <= FORECAST_CAP_MONTHS; k += 1) {
        const key = addMonths(lastFactMonthKey, k)
        const monthE = monthlyPace * seasonalFactors[monthIdx(key)]
        const target = capexAt(key)
        if (paybackT === null && monthE > 0 && fAcc < target && fAcc + monthE >= target) {
          // Interpolate the exact crossing inside this month.
          paybackT = todayT + k - 1 + (target - fAcc) / monthE
          paybackMonthKey = key
          paybackYears = paybackT / 12
        }
        fAcc += monthE
        rows.push({
          t: todayT + k,
          monthKey: key,
          factCum: null,
          forecastCum: fAcc,
          monthEbitda: monthE,
          capex: target,
          kind: 'forecast',
        })
        // Stop at the end of the crossing month so the dashed line ends
        // just past the payback marker.
        if (paybackT !== null) break
      }
    }
  }

  const tMax = rows.length > 0 ? rows[rows.length - 1].t : 0

  // Scenario range: ± one standard error of the monthly pace. The
  // spread comes from the residuals against the seasonal expectation
  // (not raw month-to-month swings, which are mostly seasonality), so
  // the range reflects genuine uncertainty and narrows with data.
  let scenario: PaybackScenario | null = null
  if (!paidOff && capexNow > 0 && monthsWithData.length >= 3 && monthlyPace > 0 && lastFactMonthKey) {
    const residuals = monthsWithData.map(
      (m) => m.totals.ebitda_uah - monthlyPace * seasonalFactors[monthIdx(m.month)] * monthCoverage(m),
    )
    const mean = residuals.reduce((a, b) => a + b, 0) / residuals.length
    const variance = residuals.reduce((a, v) => a + (v - mean) * (v - mean), 0) / (residuals.length - 1)
    const sem = Math.sqrt(variance) / Math.sqrt(totalMonthsWithData)
    const consPace = monthlyPace - sem
    const optPace = monthlyPace + sem
    if (consPace > 0) {
      const consYears = capexNow / (consPace * 12)
      const optYears = capexNow / (optPace * 12)
      const monthKeyFor = (pace: number): string | null => {
        const rest = (capexNow - allTimeEbitda) / pace
        return rest <= FORECAST_CAP_MONTHS ? addMonths(lastFactMonthKey, Math.ceil(rest)) : null
      }
      scenario = {
        consYears,
        optYears,
        consMonthKey: monthKeyFor(consPace),
        optMonthKey: monthKeyFor(optPace),
      }
    }
  }

  return {
    monthsWithData,
    prior,
    hasPrior,
    priorMonths,
    allTimeEbitda,
    totalMonthsWithData,
    effectiveMonths,
    annualEbitda,
    monthlyPace,
    seasonalFactors,
    capexNow,
    capexAvg,
    capexStages,
    paybackYears,
    coveredShare,
    remaining,
    operationYears,
    avgAnnualRoi,
    paidOff,
    rows,
    todayT,
    paybackT,
    paybackMonthKey,
    tMax,
    timeOffset,
    firstMonthKey,
    lastFactMonthKey,
    scenario,
  }
}

// monthKeyAt maps a numeric month offset t (months since operation
// start, t >= 1) back to a YYYY-MM key given the model's window anchor.
export function monthKeyAt(t: number, firstMonthKey: string, timeOffset: number): string {
  return addMonths(firstMonthKey, t - timeOffset - 1)
}

// paybackAxis picks numeric X ticks for the payback chart. Short spans
// tick every month / quarter; anything beyond 3.5 years switches to
// calendar-year ticks at each January (like the mockup's 2024…2036
// axis) so a decade-long forecast stays readable.
export function paybackAxis(
  tMax: number,
  firstMonthKey: string | null,
  timeOffset: number,
): { ticks: number[]; format: (t: number) => string } {
  if (!firstMonthKey) return { ticks: [0], format: () => 'старт' }

  if (tMax <= 42) {
    const step = tMax <= 14 ? 1 : 3
    const ticks: number[] = []
    for (let x = 0; x <= tMax; x += step) ticks.push(x)
    return {
      ticks,
      format: (t) => {
        if (t <= 0) return 'старт'
        const key = monthKeyAt(t, firstMonthKey, timeOffset)
        return step === 1 ? formatMonthShort(key) : formatMonthYearShort(key)
      },
    }
  }

  // Year ticks: the first January at or after t=1, then every 12 months.
  const firstMonth = Number(monthKeyAt(1, firstMonthKey, timeOffset).slice(5, 7))
  const firstJan = firstMonth === 1 ? 1 : 1 + (13 - firstMonth)
  const ticks: number[] = [0]
  for (let x = firstJan; x <= tMax; x += 12) ticks.push(x)
  return {
    ticks,
    format: (t) => (t <= 0 ? 'старт' : monthKeyAt(t, firstMonthKey, timeOffset).slice(0, 4)),
  }
}
