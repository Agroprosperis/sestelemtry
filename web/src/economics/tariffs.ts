// Tariffs encapsulates every per-kWh number the daily economics
// model needs. The spec [daily_economic_model_mvp.md] uses these
// inputs to derive the hourly import/export prices that turn raw
// energy flows into UAH. All values are UAH/kWh except `vatRate`
// (fraction) and the two booleans.
//
// Defaults reflect the operator-supplied numbers for 2026-05-09:
//   - `distributionUahPerKwh`: 2-class tariff for ВІННИЦЯОБЛЕНЕРГО
//     (2752.18 грн/МВт·год без ПДВ; 1-class is 0.48810 if needed)
//   - `transmissionUahPerKwh`: НЕК "Укренерго" 2-й етап (з 01.04.2026)
//   - `exportDiscount`: 5% знижка на експортну сторону (per ТЗ)
//   - `degradationUahPerKwh`: вартість циклів УЗЕ (питомий знос)
//   - `supplierMargin` / `otherFees`: оператор-специфічні; зазвичай 0
//   - `includeVat=false`: моделюємо нетто; UI окремо показує brutto
//   - `essCapacityKwh`: КОРИСНА ємність УЗЕ (енергоємність вікна SOC
//     10–90%). Використовується і для якоря залишку УЗЕ (SOC мапиться
//     через вікно 10–90% на цю ємність), і як знаменник для equivalent
//     cycles. Для пакету 645 кВт·год вводити 516.
//
// `essCapacityKwh` is technically not a tariff but it lives in the
// same form on the page (and is persisted in the same JSONB blob), so
// we co-locate it here to keep the domain type symmetrical with the
// API DTO.
//
// Persistence: per-org tariffs live on the backend
// (`/api/v1/organization-tariffs`, see `useOrgTariffs`). DEFAULT_TARIFFS
// is the seed shown until the first GET resolves and the fallback for
// orgs that have never saved.
export type Tariffs = {
  distributionUahPerKwh: number
  transmissionUahPerKwh: number
  supplierMarginUahPerKwh: number
  // supplierMarginMode selects how the supplier margin is applied: 'abs'
  // (flat UAH/kWh, the default) or 'pct' (percent of the RDN market
  // price). supplierMarginPct is the percent used in 'pct' mode. Both
  // the abs value and the pct value may be negative (a supplier discount).
  supplierMarginMode: 'abs' | 'pct'
  supplierMarginPct: number
  otherFeesUahPerKwh: number
  exportDiscount: number
  degradationUahPerKwh: number
  includeVat: boolean
  vatRate: number
  essCapacityKwh: number
  // essPowerLimitKw is the per-object nominal charge/discharge power
  // ceiling (kW) for the УЗЕ anomaly filter. 0 derives a ~1C fallback.
  essPowerLimitKw: number
  // roundtripEfficiency pins the per-object ESS round-trip efficiency
  // (0..1). 0 keeps the empirical throughput-derived estimate.
  roundtripEfficiency: number
  // capexUah is the one-time project capital expenditure (UAH). It is
  // display-only (never feeds the economics math) and powers the annual
  // payback/ROI panel; 0 hides that panel.
  capexUah: number
}

export const DEFAULT_TARIFFS: Tariffs = {
  distributionUahPerKwh: 2.75218,
  transmissionUahPerKwh: 0.74291,
  supplierMarginUahPerKwh: 0,
  supplierMarginMode: 'abs',
  supplierMarginPct: 0,
  otherFeesUahPerKwh: 0,
  exportDiscount: 0.05,
  degradationUahPerKwh: 0.6,
  includeVat: false,
  vatRate: 0.2,
  essCapacityKwh: 215,
  essPowerLimitKw: 0,
  roundtripEfficiency: 0,
  capexUah: 0,
}
