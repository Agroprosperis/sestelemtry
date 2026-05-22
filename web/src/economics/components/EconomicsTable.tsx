import type { ReactElement } from 'react'
import type { HourEconomicsRow } from '../compute'

type Props = {
  rows: Array<HourEconomicsRow | null>
}

const numberFmt = new Intl.NumberFormat('uk-UA', {
  minimumFractionDigits: 1,
  maximumFractionDigits: 1,
})

const uahFmt = new Intl.NumberFormat('uk-UA', {
  style: 'decimal',
  useGrouping: true,
  minimumFractionDigits: 0,
  maximumFractionDigits: 0,
})

const priceFmt = new Intl.NumberFormat('uk-UA', {
  minimumFractionDigits: 2,
  maximumFractionDigits: 2,
})

function formatNumber(v: number | null | undefined): string {
  if (v === null || v === undefined) return '—'
  if (!Number.isFinite(v)) return '—'
  return numberFmt.format(v)
}

function formatPrice(v: number | null | undefined): string {
  if (v === null || v === undefined) return '—'
  if (!Number.isFinite(v) || v === 0) return '—'
  return priceFmt.format(v)
}

function formatUah(v: number | null | undefined): string {
  if (v === null || v === undefined) return '—'
  if (!Number.isFinite(v)) return '—'
  return uahFmt.format(v)
}

// MetricRow describes one row in the transposed table. `kind` drives
// number formatting + sign coloring so the operator immediately sees
// "this is currency" vs "this is energy" without re-reading the
// label. `pickHourValue` returns the per-hour number and `total`
// gives the daily aggregate; both return null when the underlying
// row is missing or the metric is undefined for that hour.
type MetricKind = 'price' | 'energy' | 'uah' | 'uah_signed'

type MetricRow = {
  id: string
  label: string
  unit: string
  kind: MetricKind
  // summary rows are visually emphasised: bold label, light tint on
  // the cells, slightly heavier border. They represent the total of
  // the breakdown rows directly underneath them so the operator can
  // sanity-check the split adds up.
  summary?: boolean
  pickHourValue: (row: HourEconomicsRow | null) => number | null
  total: (rows: Array<HourEconomicsRow | null>) => number | null
  // cellClass adds a per-value modifier class to each hour cell.
  // Used by the РДН row to tint hours by price tier (cheap → blue,
  // peak → red) so the operator can scan the day's price profile
  // without reading individual numbers. Returns undefined to opt
  // out for a given cell (e.g. null/zero values).
  cellClass?: (value: number | null) => string | undefined
}

// rdnTierClass maps an RDN price (UAH/kWh) onto one of four pale
// background classes. Thresholds step in 4 UAH/kWh increments so
// the bands cover the full UA market range an operator might see
// on any given day: <4 cheap, 4–8 moderate, 8–12 elevated, ≥12
// peak. Returns undefined for null/zero so the empty-cell renderer
// keeps the muted "—" treatment instead of being painted "cheap".
function rdnTierClass(value: number | null): string | undefined {
  if (value === null || !Number.isFinite(value) || value === 0) return undefined
  if (value < 4) return 'rdn-tier-cool'
  if (value < 8) return 'rdn-tier-warm'
  if (value < 12) return 'rdn-tier-hot'
  return 'rdn-tier-peak'
}

function pickWhenPriced(
  pick: (row: HourEconomicsRow) => number,
): (row: HourEconomicsRow | null) => number | null {
  return (row) => {
    if (!row) return null
    if (row.rdnUahPerKwh === null) return null
    return pick(row)
  }
}

// pickRevenue returns the per-hour UAH value for a revenue/expense
// channel: `flow(row) · price(row)` where flow is a kWh accessor and
// price is the matching import or export price (already on each
// HourEconomicsRow). Hours with no RDN price are skipped — same
// guard the baseline/actual rows use, so a partial day stays
// honest instead of inflating revenue with zero-priced kWh.
function pickRevenue(
  flow: (row: HourEconomicsRow) => number,
  price: (row: HourEconomicsRow) => number,
): (row: HourEconomicsRow | null) => number | null {
  return (row) => {
    if (!row || row.rdnUahPerKwh === null) return null
    return flow(row) * price(row)
  }
}

function sumOver(
  rows: Array<HourEconomicsRow | null>,
  pick: (row: HourEconomicsRow | null) => number | null,
): number | null {
  let acc = 0
  let any = false
  for (const r of rows) {
    const v = pick(r)
    if (v === null) continue
    if (!Number.isFinite(v)) continue
    acc += v
    any = true
  }
  return any ? acc : null
}

const METRIC_GROUPS: Array<{ id: string; label: string; rows: MetricRow[] }> = [
  {
    id: 'prices',
    label: 'Ціни',
    rows: [
      {
        id: 'rdn',
        label: 'РДН',
        unit: 'грн/кВт·год',
        kind: 'price',
        pickHourValue: (row) => row?.rdnUahPerKwh ?? null,
        total: () => null,
        cellClass: rdnTierClass,
      },
      {
        id: 'import_price',
        label: 'Ціна імпорту',
        unit: 'грн/кВт·год',
        kind: 'price',
        pickHourValue: pickWhenPriced((r) => r.economics.importPriceUahPerKwh),
        total: () => null,
      },
      {
        id: 'export_price',
        label: 'Ціна експорту',
        unit: 'грн/кВт·год',
        kind: 'price',
        pickHourValue: pickWhenPriced((r) => r.economics.exportPriceUahPerKwh),
        total: () => null,
      },
      {
        // Імпорт/Експорт всього sit directly under the price rows
        // so the operator can read "ціна × обсяг" left-to-right
        // without bouncing between groups. Energy unit (кВт·год)
        // contrasts with the грн/кВт·год above, the visual jump is
        // intentional.
        id: 'grid_import',
        label: 'Імпорт всього',
        unit: 'кВт·год',
        kind: 'energy',
        pickHourValue: (row) => row?.flow.gridImport ?? null,
        total: (rows) => sumOver(rows, (r) => r?.flow.gridImport ?? null),
      },
      {
        id: 'grid_export',
        label: 'Експорт всього',
        unit: 'кВт·год',
        kind: 'energy',
        pickHourValue: (row) => row?.flow.gridExport ?? null,
        total: (rows) => sumOver(rows, (r) => r?.flow.gridExport ?? null),
      },
    ],
  },
  {
    // Consumption-side breakdown: total load served by each source.
    // The Σ row is just `load`; the three breakdown rows must add
    // up to it (modulo the same numerical rounding the dashboard
    // applies elsewhere).
    id: 'consumption',
    label: 'Споживання',
    rows: [
      {
        id: 'load',
        label: 'Споживання (всього)',
        unit: 'кВт·год',
        kind: 'energy',
        summary: true,
        pickHourValue: (row) => row?.economics.load ?? null,
        total: (rows) => sumOver(rows, (r) => r?.economics.load ?? null),
      },
      {
        id: 'pv_to_load',
        label: 'СЕС → Споживання',
        unit: 'кВт·год',
        kind: 'energy',
        pickHourValue: (row) => row?.economics.pvToLoad ?? null,
        total: (rows) => sumOver(rows, (r) => r?.economics.pvToLoad ?? null),
      },
      {
        id: 'grid_to_load',
        label: 'Мережа → Споживання',
        unit: 'кВт·год',
        kind: 'energy',
        pickHourValue: (row) => row?.economics.gridToLoad ?? null,
        total: (rows) => sumOver(rows, (r) => r?.economics.gridToLoad ?? null),
      },
      {
        id: 'ess_to_load',
        label: 'УЗЕ → Споживання',
        unit: 'кВт·год',
        kind: 'energy',
        pickHourValue: (row) => row?.flow.essToLoad ?? null,
        total: (rows) => sumOver(rows, (r) => r?.flow.essToLoad ?? null),
      },
    ],
  },
  {
    // PV-side breakdown: total generation routed to each sink.
    // `pv` is the SmartLogger accumulator delta; the three
    // PV→{load,ess,grid} rows must sum to it.
    id: 'pv',
    label: 'Виробіток СЕС',
    rows: [
      {
        id: 'pv_total',
        label: 'СЕС (виробіток)',
        unit: 'кВт·год',
        kind: 'energy',
        summary: true,
        pickHourValue: (row) => row?.flow.pv ?? null,
        total: (rows) => sumOver(rows, (r) => r?.flow.pv ?? null),
      },
      {
        id: 'pv_to_load_pv',
        label: 'СЕС → Споживання',
        unit: 'кВт·год',
        kind: 'energy',
        pickHourValue: (row) => row?.economics.pvToLoad ?? null,
        total: (rows) => sumOver(rows, (r) => r?.economics.pvToLoad ?? null),
      },
      {
        id: 'pv_to_grid',
        label: 'СЕС → Мережа',
        unit: 'кВт·год',
        kind: 'energy',
        pickHourValue: (row) => row?.economics.pvToGrid ?? null,
        total: (rows) => sumOver(rows, (r) => r?.economics.pvToGrid ?? null),
      },
      {
        id: 'pv_to_ess',
        label: 'СЕС → УЗЕ',
        unit: 'кВт·год',
        kind: 'energy',
        pickHourValue: (row) => row?.flow.pvToEss ?? null,
        total: (rows) => sumOver(rows, (r) => r?.flow.pvToEss ?? null),
      },
    ],
  },
  {
    // УЗЕ-flows block. Sequenced as charge-from-grid → discharge-to-
    // load → discharge-to-grid → залишок so the rows read top-down
    // as "що зайшло в УЗЕ → що вийшло → що залишилось". УЗЕ →
    // Споживання is intentionally duplicated here (it also lives in
    // the consumption breakdown above) so the operator can scan the
    // whole УЗЕ life-cycle without jumping between sections.
    id: 'ess_grid',
    label: 'УЗЕ',
    rows: [
      {
        id: 'grid_to_ess',
        label: 'Мережа → УЗЕ',
        unit: 'кВт·год',
        kind: 'energy',
        pickHourValue: (row) => row?.flow.gridToEss ?? null,
        total: (rows) => sumOver(rows, (r) => r?.flow.gridToEss ?? null),
      },
      {
        // Duplicate of the consumption-group row with the same
        // semantics. Different React key ('ess_to_load_dup') so
        // both rows can coexist without a key collision.
        id: 'ess_to_load_dup',
        label: 'УЗЕ → Споживання',
        unit: 'кВт·год',
        kind: 'energy',
        pickHourValue: (row) => row?.flow.essToLoad ?? null,
        total: (rows) => sumOver(rows, (r) => r?.flow.essToLoad ?? null),
      },
      {
        id: 'ess_to_grid',
        label: 'УЗЕ → Мережа',
        unit: 'кВт·год',
        kind: 'energy',
        pickHourValue: (row) => row?.flow.essToGrid ?? null,
        total: (rows) => sumOver(rows, (r) => r?.flow.essToGrid ?? null),
      },
      {
        // Залишок УЗЕ = SOC at the start of the hour · ємність.
        // Pre-computed in useEconomicsData (where tariffs live)
        // because the row config has no access to tariffs at render
        // time. The Σ column intentionally returns null: summing
        // residual battery levels across hours is meaningless (each
        // hour's value is an instantaneous snapshot, not a flow), so
        // we render an em-dash instead of a fake total.
        id: 'ess_remaining',
        label: 'Залишок УЗЕ',
        unit: 'кВт·год',
        kind: 'energy',
        pickHourValue: (row) => row?.essRemainingKwhStart ?? null,
        total: () => null,
      },
    ],
  },
  {
    // Revenue / expense per channel — the per-hour version of the
    // EBITDA panel above the chart. Each row is `flowₕ · priceₕ`
    // (matching `dailyTotals` revenue/expense fields exactly), so
    // the Σ column reproduces the panel's amounts to the гривня.
    // `Дохід всього` is a summary row (sum of the four below);
    // Витрати has only one breakdown line, so no separate summary.
    id: 'revenue',
    label: 'Дохід та витрати',
    rows: [
      {
        id: 'revenue_total',
        label: 'Дохід всього',
        unit: 'грн',
        kind: 'uah',
        summary: true,
        pickHourValue: (row) => {
          if (!row || row.rdnUahPerKwh === null) return null
          const imp = row.economics.importPriceUahPerKwh
          const exp = row.economics.exportPriceUahPerKwh
          return (
            row.economics.pvToGrid * exp +
            row.economics.pvToLoad * imp +
            row.flow.essToGrid * exp +
            row.flow.essToLoad * imp
          )
        },
        total: (rows) =>
          sumOver(rows, (row) => {
            if (!row || row.rdnUahPerKwh === null) return null
            const imp = row.economics.importPriceUahPerKwh
            const exp = row.economics.exportPriceUahPerKwh
            return (
              row.economics.pvToGrid * exp +
              row.economics.pvToLoad * imp +
              row.flow.essToGrid * exp +
              row.flow.essToLoad * imp
            )
          }),
      },
      {
        id: 'revenue_pv_export',
        label: 'Дохід: СЕС → мережа',
        unit: 'грн',
        kind: 'uah',
        pickHourValue: pickRevenue(
          (r) => r.economics.pvToGrid,
          (r) => r.economics.exportPriceUahPerKwh,
        ),
        total: (rows) =>
          sumOver(
            rows,
            pickRevenue((r) => r.economics.pvToGrid, (r) => r.economics.exportPriceUahPerKwh),
          ),
      },
      {
        id: 'revenue_pv_self',
        label: 'Дохід: СЕС → споживання',
        unit: 'грн',
        kind: 'uah',
        pickHourValue: pickRevenue(
          (r) => r.economics.pvToLoad,
          (r) => r.economics.importPriceUahPerKwh,
        ),
        total: (rows) =>
          sumOver(
            rows,
            pickRevenue((r) => r.economics.pvToLoad, (r) => r.economics.importPriceUahPerKwh),
          ),
      },
      {
        id: 'revenue_ess_export',
        label: 'Дохід: УЗЕ → мережа',
        unit: 'грн',
        kind: 'uah',
        pickHourValue: pickRevenue(
          (r) => r.flow.essToGrid,
          (r) => r.economics.exportPriceUahPerKwh,
        ),
        total: (rows) =>
          sumOver(
            rows,
            pickRevenue((r) => r.flow.essToGrid, (r) => r.economics.exportPriceUahPerKwh),
          ),
      },
      {
        id: 'revenue_ess_self',
        label: 'Дохід: УЗЕ → споживання',
        unit: 'грн',
        kind: 'uah',
        pickHourValue: pickRevenue(
          (r) => r.flow.essToLoad,
          (r) => r.economics.importPriceUahPerKwh,
        ),
        total: (rows) =>
          sumOver(
            rows,
            pickRevenue((r) => r.flow.essToLoad, (r) => r.economics.importPriceUahPerKwh),
          ),
      },
      {
        // Витрати summary mirrors the Дохід всього row above so the
        // panel and the table read the same way (heading row +
        // breakdown beneath). Today there's only one expense leg
        // (gridToEss · importPrice), but the summary slot is kept so
        // adding e.g. degradation cost later doesn't require
        // restructuring the table.
        id: 'expense_total',
        label: 'Витрати всього',
        unit: 'грн',
        kind: 'uah',
        summary: true,
        pickHourValue: pickRevenue(
          (r) => r.flow.gridToEss,
          (r) => r.economics.importPriceUahPerKwh,
        ),
        total: (rows) =>
          sumOver(
            rows,
            pickRevenue((r) => r.flow.gridToEss, (r) => r.economics.importPriceUahPerKwh),
          ),
      },
      {
        id: 'expense_grid_charge',
        label: 'Витрати: Заряд УЗЕ із мережі',
        unit: 'грн',
        kind: 'uah',
        pickHourValue: pickRevenue(
          (r) => r.flow.gridToEss,
          (r) => r.economics.importPriceUahPerKwh,
        ),
        total: (rows) =>
          sumOver(
            rows,
            pickRevenue((r) => r.flow.gridToEss, (r) => r.economics.importPriceUahPerKwh),
          ),
      },
      {
        // EBITDA = Дохід − Витрати per hour. Equals the Економіка
        // block's `Ефект` when `degradationUahPerKwh = 0`; the
        // difference otherwise is the УЗЕ-degradation cost the
        // Економіка block subtracts. We render with sign coloring so
        // negative-EBITDA hours (rare, charge cost > all four
        // revenue legs) stand out in red.
        id: 'ebitda',
        label: 'EBITDA',
        unit: 'грн',
        kind: 'uah_signed',
        summary: true,
        pickHourValue: (row) => {
          if (!row || row.rdnUahPerKwh === null) return null
          const imp = row.economics.importPriceUahPerKwh
          const exp = row.economics.exportPriceUahPerKwh
          const revenue =
            row.economics.pvToGrid * exp +
            row.economics.pvToLoad * imp +
            row.flow.essToGrid * exp +
            row.flow.essToLoad * imp
          const expense = row.flow.gridToEss * imp
          return revenue - expense
        },
        total: (rows) =>
          sumOver(rows, (row) => {
            if (!row || row.rdnUahPerKwh === null) return null
            const imp = row.economics.importPriceUahPerKwh
            const exp = row.economics.exportPriceUahPerKwh
            const revenue =
              row.economics.pvToGrid * exp +
              row.economics.pvToLoad * imp +
              row.flow.essToGrid * exp +
              row.flow.essToLoad * imp
            const expense = row.flow.gridToEss * imp
            return revenue - expense
          }),
      },
    ],
  },
  {
    // Економіка carries the two project-level deltas (Ефект,
    // УЗЕ нетто) — Базова / Фактична були прибрані, бо їх повна
    // розкладка вже видима у групі "Дохід та витрати" вище. Перший
    // рядок цього блоку позначений як summary щоб у місці переходу
    // від EBITDA до економіки виник чіткий візуальний роздільник.
    id: 'economics',
    label: 'Економіка',
    rows: [
      {
        id: 'effect',
        label: 'Ефект',
        unit: 'грн',
        kind: 'uah_signed',
        summary: true,
        pickHourValue: pickWhenPriced((r) => r.economics.effect),
        total: (rows) => sumOver(rows, pickWhenPriced((r) => r.economics.effect)),
      },
      {
        id: 'ess_net',
        label: 'УЗЕ нетто',
        unit: 'грн',
        kind: 'uah_signed',
        pickHourValue: pickWhenPriced((r) => r.economics.essNet),
        total: (rows) => sumOver(rows, pickWhenPriced((r) => r.economics.essNet)),
      },
    ],
  },
]

function renderCell(
  value: number | null,
  kind: MetricKind,
  extraClass?: string,
): ReactElement {
  if (value === null) return <td className="cell-empty">—</td>
  let formatted: string
  let className: string | undefined
  switch (kind) {
    case 'price':
      formatted = formatPrice(value)
      break
    case 'energy':
      formatted = formatNumber(value)
      break
    case 'uah':
      formatted = formatUah(value)
      break
    case 'uah_signed':
      formatted = formatUah(value)
      className = value >= 0 ? 'cell-positive' : 'cell-negative'
      break
  }
  const merged = [className, extraClass].filter(Boolean).join(' ') || undefined
  return <td className={merged}>{formatted}</td>
}

const HOUR_COUNT = 24

export function EconomicsTable({ rows }: Props) {
  return (
    <section className="economics-table-wrap" aria-label="Погодинна деталізація">
      <h3>Погодинна деталізація</h3>
      <div className="economics-table-scroll">
        <table className="economics-table economics-table-pivot">
          <thead>
            <tr>
              <th className="economics-table-metric-head" rowSpan={2} scope="col">
                Показник
              </th>
              <th rowSpan={2} scope="col" className="economics-table-total-head">
                Σ за добу
              </th>
              <th colSpan={HOUR_COUNT} scope="colgroup" className="economics-table-hours-head">
                Година (00…23)
              </th>
            </tr>
            <tr>
              {Array.from({ length: HOUR_COUNT }, (_, h) => (
                <th key={h} scope="col" className="economics-table-hour-cell">
                  {String(h).padStart(2, '0')}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {METRIC_GROUPS.flatMap((group) => group.rows).map((metric) => {
              const totalValue = metric.total(rows)
              const rowClass = metric.summary ? 'economics-table-summary-row' : undefined
              return (
                <tr key={metric.id} className={rowClass}>
                  <th scope="row" className="economics-table-metric">
                    {metric.label}
                    <small>, {metric.unit}</small>
                  </th>
                  <td className="economics-table-total-cell">
                    {totalValue === null ? (
                      <span className="cell-empty">—</span>
                    ) : (
                      renderTotal(totalValue, metric.kind)
                    )}
                  </td>
                  {rows.map((row, hourIdx) => {
                    const value = metric.pickHourValue(row)
                    return (
                      <FragmentCell
                        key={hourIdx}
                        value={value}
                        kind={metric.kind}
                        extraClass={metric.cellClass?.(value)}
                      />
                    )
                  })}
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    </section>
  )
}

function FragmentCell({
  value,
  kind,
  extraClass,
}: {
  value: number | null
  kind: MetricKind
  extraClass?: string
}) {
  return renderCell(value, kind, extraClass)
}

function renderTotal(value: number, kind: MetricKind): ReactElement {
  switch (kind) {
    case 'price':
      // Sum of prices is meaningless; the row's `total` returns null
      // for price metrics so this branch is never reached, but we
      // handle it defensively.
      return <span>{formatPrice(value)}</span>
    case 'energy':
      return <span>{formatNumber(value)}</span>
    case 'uah':
      return <span>{formatUah(value)}</span>
    case 'uah_signed':
      return (
        <span className={value >= 0 ? 'cell-positive' : 'cell-negative'}>
          {formatUah(value)}
        </span>
      )
  }
}
