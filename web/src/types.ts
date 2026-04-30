export type DashboardMetric = {
  key: string
  label: string
  unit: string
}

export type DashboardConfig = {
  cards: DashboardMetric[]
  power_chart: DashboardMetric[]
  energy_chart: DashboardMetric[]
}

export type CurrentMetric = {
  metric_key: string
  value: number
  time: string
  labels?: Record<string, string>
}

export type CurrentResponse = {
  organization_id: string
  metrics: Record<string, CurrentMetric>
}

export type TimeseriesPoint = {
  time: string
  metric_key: string
  value: number
}

export type TimeseriesResponse = {
  organization_id: string
  metric_keys: string[]
  bucket: string
  from: string
  to: string
  points: TimeseriesPoint[]
}

export type DAMPrice = {
  delivery_date: string
  hour: number
  zone: number
  price_uah_per_mwh?: number | null
  sale_volume_mwh?: number | null
  purchase_volume_mwh?: number | null
  declared_sale_volume_mwh?: number | null
  declared_purchase_volume_mwh?: number | null
}

export type DAMPricesResponse = {
  zone: number
  from: string
  to: string
  prices: DAMPrice[]
}
