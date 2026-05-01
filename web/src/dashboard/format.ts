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
