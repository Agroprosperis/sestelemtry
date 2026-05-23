// Adaptive UAH/kWh formatters for the overview cards. Big totals
// (the cumulative card spans MWh) and small per-hour numbers (the
// Sankey can dip below 1 kWh) live on the same page, so a single
// fixed-precision Intl.NumberFormat would either drown the big
// values in zeros or round the small ones to "0 кВт·год". Pulling
// the precision picker into a shared helper keeps every card honest
// about its own dynamic range.

function pickFractionDigits(maxAbs: number): number {
  if (maxAbs < 1) return 2
  if (maxAbs < 10) return 1
  return 0
}

const ENERGY_UNIT_KWH = 'кВт·год'
const ENERGY_UNIT_MWH = 'МВт·год'

// formatEnergyUk renders a kWh number with Ukrainian units. Switches
// to МВт·год above 1 MWh and adapts decimal places to the value so
// that "0,12 кВт·год" doesn't collapse to "0 кВт·год" but a 5,3 MWh
// daily total isn't padded with five trailing decimals.
export function formatEnergyUk(valueKWh: number): string {
  if (!Number.isFinite(valueKWh)) return '—'
  const abs = Math.abs(valueKWh)
  if (abs >= 1000) {
    const v = valueKWh / 1000
    const fd = pickFractionDigits(Math.abs(v))
    return `${formatWithDigits(v, fd)} ${ENERGY_UNIT_MWH}`
  }
  const fd = pickFractionDigits(abs)
  return `${formatWithDigits(valueKWh, fd)} ${ENERGY_UNIT_KWH}`
}

// formatEnergyUkCompactMWh forces МВт·год units regardless of the
// magnitude — used by the cumulative card where every row is in
// the multi-MWh range and a mixed-units column would be unreadable.
export function formatEnergyUkCompactMWh(valueKWh: number): string {
  if (!Number.isFinite(valueKWh)) return '—'
  const v = valueKWh / 1000
  const fd = pickFractionDigits(Math.abs(v))
  return `${formatWithDigits(v, fd)} ${ENERGY_UNIT_MWH}`
}

export function formatPercent(value: number): string {
  if (!Number.isFinite(value)) return '—'
  const fd = pickFractionDigits(Math.abs(value))
  return `${formatWithDigits(value, fd)} %`
}

function formatWithDigits(value: number, fractionDigits: number): string {
  return new Intl.NumberFormat('uk-UA', {
    minimumFractionDigits: fractionDigits,
    maximumFractionDigits: fractionDigits,
  }).format(value)
}
