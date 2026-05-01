import type { DAMPrice } from '../../types'
import type { RangePreset } from '../range'
import { timelineBuckets } from '../timeline'

export type DAMChartRow = {
  time: string
  bucketStart: number
  price: number | null
}

function parseDeliveryDate(value: string): Date | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(value)
  if (!m) return null
  const d = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]))
  return Number.isFinite(d.getTime()) ? d : null
}

function bucketKey(preset: RangePreset, deliveryDate: Date, hour: number): number {
  const d = new Date(deliveryDate)
  if (preset === 'year') {
    d.setMonth(d.getMonth(), 1)
    d.setHours(0, 0, 0, 0)
  } else if (preset === 'month') {
    d.setHours(0, 0, 0, 0)
  } else {
    d.setHours(Math.max(0, hour - 1), 0, 0, 0)
  }
  return d.getTime()
}

// lookupKey maps a timeline bucket start to the aggregation key used above.
// For day preset the timeline is 5-minute while DAM prices are hourly, so we
// round each sub-hour bucket down to its containing hour start.
function lookupKey(preset: RangePreset, t: number): number {
  if (preset !== 'day') return t
  const d = new Date(t)
  d.setMinutes(0, 0, 0)
  return d.getTime()
}

export function damChartRows(prices: DAMPrice[], preset: RangePreset, anchor: Date): DAMChartRow[] {
  type Acc = { sum: number; count: number }
  const buckets = new Map<number, Acc>()

  for (const p of prices) {
    if (p.price_uah_per_mwh == null || !Number.isFinite(p.price_uah_per_mwh)) continue
    const deliveryDate = parseDeliveryDate(p.delivery_date)
    if (!deliveryDate) continue
    const key = bucketKey(preset, deliveryDate, p.hour)
    const acc = buckets.get(key)
    if (acc) {
      acc.sum += p.price_uah_per_mwh
      acc.count += 1
    } else {
      buckets.set(key, { sum: p.price_uah_per_mwh, count: 1 })
    }
  }

  const timeline = timelineBuckets(preset, anchor)
  return timeline.map(({ t, label }) => {
    const acc = buckets.get(lookupKey(preset, t))
    return {
      time: label,
      bucketStart: t,
      price: acc && acc.count > 0 ? acc.sum / acc.count : null,
    }
  })
}

export function averagePrice(rows: DAMChartRow[]): number | null {
  let sum = 0
  let count = 0
  for (const r of rows) {
    if (r.price != null && Number.isFinite(r.price)) {
      sum += r.price
      count += 1
    }
  }
  return count > 0 ? sum / count : null
}
