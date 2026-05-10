import type { RangePreset } from './range'

export function formatNumber(value: number, unit: string): string {
  const decimals = unit === '%' ? 1 : 2
  const factor = 10 ** decimals
  const rounded = Math.round((value + Number.EPSILON) * factor) / factor
  return new Intl.NumberFormat(undefined, {
    minimumFractionDigits: decimals,
    maximumFractionDigits: decimals,
  }).format(rounded)
}

export function formatChartNumber(value: number): string {
  const rounded = Math.round((value + Number.EPSILON) * 100) / 100
  return new Intl.NumberFormat(undefined, {
    minimumFractionDigits: 0,
    maximumFractionDigits: 2,
  }).format(rounded)
}

export function formatEnergyCompactKWh(valueKWh: number): string {
  if (!Number.isFinite(valueKWh)) return '--'
  if (valueKWh >= 1000) {
    return `${formatChartNumber(valueKWh / 1000)} MWh`
  }
  return `${formatChartNumber(valueKWh)} kWh`
}

// formatEnergyCompactKWhUk renders a kWh total in Ukrainian units. Mirrors
// formatEnergyCompactKWh but uses кВт·год / МВт·год — used by the
// narrative panels where rows read as plain Ukrainian sentences.
export function formatEnergyCompactKWhUk(valueKWh: number): string {
  if (!Number.isFinite(valueKWh)) return '--'
  if (valueKWh >= 1000) {
    return `${formatChartNumber(valueKWh / 1000)} МВт·год`
  }
  return `${formatChartNumber(valueKWh)} кВт·год`
}

// formatPeriodLabel renders the concrete period a Підсумок / Перетік
// card is currently showing — handy beside the static "за день /
// місяць / рік" titles so the operator can tell at a glance whether
// they're looking at today, last month, or 2025 without bouncing to
// the date picker. The output is locale-stable (`uk-UA`) because the
// rest of the dashboard mixes Ukrainian copy and English numerics
// and we want the period label to read as Ukrainian prose:
//   day   → "10 травня 2026"   (родовий відмінок місяця)
//   month → "травень 2026"     (називний)
//   year  → "2026"
export function formatPeriodLabel(preset: RangePreset, anchor: Date): string {
  if (preset === 'year') {
    return new Intl.DateTimeFormat('uk-UA', { year: 'numeric' }).format(anchor)
  }
  if (preset === 'month') {
    return new Intl.DateTimeFormat('uk-UA', {
      month: 'long',
      year: 'numeric',
    }).format(anchor)
  }
  return new Intl.DateTimeFormat('uk-UA', {
    day: 'numeric',
    month: 'long',
    year: 'numeric',
  }).format(anchor)
}

export function formatTimeLabel(date: Date, preset: RangePreset): string {
  if (preset === 'year') {
    return date.toLocaleDateString(undefined, { month: 'short' })
  }
  if (preset === 'month') {
    return date.toLocaleDateString(undefined, { day: '2-digit' })
  }
  return date.toLocaleTimeString(undefined, {
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  })
}
