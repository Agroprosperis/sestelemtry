import type { EnergyRow } from './buckets'
import type { DAMChartRow } from './dam'

export type RevenueChartRow = {
  time: string
  revenue: number | null
}

// revenueChartRows computes estimated PV generation revenue per timeline
// bucket: kWh produced in that bucket multiplied by the average DAM price
// (UAH per MWh) for the same bucket, divided by 1000 to convert MWh -> kWh.
// Returns null for buckets that have no PV value yet (future hours) or no
// published DAM price for the corresponding delivery time.
export function revenueChartRows(
  energy: EnergyRow[],
  dam: DAMChartRow[],
): RevenueChartRow[] {
  const priceByTime = new Map<string, number | null>()
  for (const r of dam) {
    priceByTime.set(String(r.time), r.price)
  }
  return energy.map((row) => {
    const time = String(row.time)
    const rawPV = row.accumulated_pv_energy_yield_kwh
    const pv = Number(rawPV)
    const price = priceByTime.get(time)
    if (
      rawPV === undefined ||
      !Number.isFinite(pv) ||
      price == null ||
      !Number.isFinite(price)
    ) {
      return { time, revenue: null }
    }
    return { time, revenue: (pv * price) / 1000 }
  })
}

export function totalRevenue(rows: RevenueChartRow[]): number {
  let sum = 0
  for (const r of rows) {
    if (r.revenue != null && Number.isFinite(r.revenue)) sum += r.revenue
  }
  return sum
}
