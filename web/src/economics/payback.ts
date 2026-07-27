// Shared CAPEX-payback math for the economics views: the annual
// dashboard panel and the standalone "Окупність проєкту" page both
// render the same model — cumulative EBITDA since the start of
// operation on a numeric month axis, extended with a linear run-rate
// forecast to the CAPEX crossing.

import type { EconomicsAnnualMonthRollup } from '../api'
import { formatMonthShort } from './monthly/format'

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
    return { unit: 'млн ₴', tick: (v) => axisDecimalFmt.format(v / 1_000_000) }
  }
  return { unit: 'тис. ₴', tick: (v) => axisIntegerFmt.format(v / 1_000) }
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
  annualEbitda: number
  monthlyPace: number
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
  months,
  ebitda,
  priorEbitda,
  priorMonthsWithData,
}: {
  capexUah: number
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
  const monthsWithData = months.filter((m) => m.totals.hours_with_data > 0)

  const allTimeEbitda = prior + ebitda
  const totalMonthsWithData = monthsWithData.length + priorMonths
  const annualEbitda = totalMonthsWithData > 0 ? (allTimeEbitda * 12) / totalMonthsWithData : 0
  const monthlyPace = annualEbitda / 12
  const paybackYears = annualEbitda > 0 ? capexUah / annualEbitda : Infinity
  const coveredShare = capexUah > 0 ? Math.max(0, Math.min(allTimeEbitda / capexUah, 1)) : 0
  const remaining = Math.max(capexUah - allTimeEbitda, 0)
  const operationYears = totalMonthsWithData / 12
  const avgAnnualRoi =
    capexUah > 0 && operationYears > 0 ? allTimeEbitda / capexUah / operationYears : NaN
  const paidOff = allTimeEbitda >= capexUah && capexUah > 0

  const rows: CapexPaybackRow[] = []
  let todayT: number | null = null
  let paybackT: number | null = null
  let paybackMonthKey: string | null = null
  const firstMonthKey = monthsWithData[0]?.month ?? null
  const lastFactMonthKey = monthsWithData[monthsWithData.length - 1]?.month ?? null
  const timeOffset = hasPrior && priorMonths > 0 ? priorMonths : 0

  if (firstMonthKey && lastFactMonthKey) {
    // Months since operation start: the prior window occupies [0,
    // priorMonths], each fact month of the visible window adds one.
    if (hasPrior && priorMonths > 0) {
      rows.push({ t: 0, monthKey: null, factCum: 0, forecastCum: null, monthEbitda: null, kind: 'start' })
      rows.push({
        t: priorMonths,
        monthKey: addMonths(firstMonthKey, -1),
        factCum: prior,
        forecastCum: null,
        monthEbitda: null,
        kind: 'prior',
      })
    } else {
      // No prior history (or the backend didn't count its months): the
      // window opens at the start of operation with the opening balance.
      rows.push({ t: 0, monthKey: null, factCum: prior, forecastCum: null, monthEbitda: null, kind: 'start' })
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
        kind: 'fact',
      })
    }
    todayT = t

    if (acc >= capexUah && capexUah > 0) {
      // Already paid off: mark the fact row where the line crossed CAPEX.
      const hit = rows.find((r) => r.kind !== 'start' && (r.factCum ?? 0) >= capexUah)
      if (hit) {
        paybackT = hit.t
        paybackMonthKey = hit.monthKey
      }
    } else if (monthlyPace > 0 && capexUah > 0) {
      // Bridge point so the dashed projection continues the fact line.
      rows[rows.length - 1].forecastCum = acc
      const exactT = todayT + (capexUah - acc) / monthlyPace
      const endT = Math.min(exactT, todayT + FORECAST_CAP_MONTHS)
      let ft = todayT
      let fAcc = acc
      while (ft + 1 < endT) {
        ft += 1
        fAcc += monthlyPace
        rows.push({
          t: ft,
          monthKey: addMonths(lastFactMonthKey, ft - todayT),
          factCum: null,
          forecastCum: fAcc,
          monthEbitda: null,
          kind: 'forecast',
        })
      }
      const reached = exactT <= todayT + FORECAST_CAP_MONTHS
      rows.push({
        t: endT,
        monthKey: addMonths(lastFactMonthKey, Math.ceil(endT - todayT)),
        factCum: null,
        forecastCum: reached ? capexUah : acc + monthlyPace * FORECAST_CAP_MONTHS,
        monthEbitda: null,
        kind: 'forecast',
      })
      if (reached) {
        paybackT = endT
        paybackMonthKey = addMonths(lastFactMonthKey, Math.ceil(exactT - todayT))
      }
    }
  }

  const tMax = rows.length > 0 ? rows[rows.length - 1].t : 0

  // Scenario range: ± one standard error of the monthly pace, taken
  // from the variability of the visible window's monthly EBITDA. Needs
  // at least 3 observed months and a strictly positive lower bound.
  let scenario: PaybackScenario | null = null
  if (!paidOff && capexUah > 0 && monthsWithData.length >= 3 && monthlyPace > 0 && lastFactMonthKey) {
    const values = monthsWithData.map((m) => m.totals.ebitda_uah)
    const mean = values.reduce((a, b) => a + b, 0) / values.length
    const variance = values.reduce((a, v) => a + (v - mean) * (v - mean), 0) / (values.length - 1)
    const sem = Math.sqrt(variance) / Math.sqrt(totalMonthsWithData)
    const consPace = monthlyPace - sem
    const optPace = monthlyPace + sem
    if (consPace > 0) {
      const consYears = capexUah / (consPace * 12)
      const optYears = capexUah / (optPace * 12)
      const monthKeyFor = (pace: number): string | null => {
        const rest = (capexUah - allTimeEbitda) / pace
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
    annualEbitda,
    monthlyPace,
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
