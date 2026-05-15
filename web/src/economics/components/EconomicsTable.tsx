import type { HourEconomicsRow } from '../compute'

type Props = {
  rows: Array<HourEconomicsRow | null>
}

const numberFmt = new Intl.NumberFormat('uk-UA', {
  minimumFractionDigits: 1,
  maximumFractionDigits: 1,
})

const uahFmt = new Intl.NumberFormat('uk-UA', {
  style: 'currency',
  currency: 'UAH',
  minimumFractionDigits: 0,
  maximumFractionDigits: 0,
})

const priceFmt = new Intl.NumberFormat('uk-UA', {
  minimumFractionDigits: 3,
  maximumFractionDigits: 3,
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

export function EconomicsTable({ rows }: Props) {
  return (
    <section className="economics-table-wrap" aria-label="Погодинна деталізація">
      <h3>Погодинна деталізація</h3>
      <div className="economics-table-scroll">
        <table className="economics-table">
          <thead>
            <tr>
              <th>Год</th>
              <th>РДН<br /><small>грн/кВт·год</small></th>
              <th>Імпорт ц.<br /><small>грн/кВт·год</small></th>
              <th>Експорт ц.<br /><small>грн/кВт·год</small></th>
              <th>Навант.<br /><small>кВт·год</small></th>
              <th>PV→Навант.<br /><small>кВт·год</small></th>
              <th>PV→УЗЕ<br /><small>кВт·год</small></th>
              <th>PV→Мережа<br /><small>кВт·год</small></th>
              <th>Мережа→Навант.<br /><small>кВт·год</small></th>
              <th>Мережа→УЗЕ<br /><small>кВт·год</small></th>
              <th>УЗЕ→Навант.<br /><small>кВт·год</small></th>
              <th>УЗЕ→Мережа<br /><small>кВт·год</small></th>
              <th>Імпорт всього<br /><small>кВт·год</small></th>
              <th>Експорт всього<br /><small>кВт·год</small></th>
              <th>Базова<br /><small>грн</small></th>
              <th>Фактична<br /><small>грн</small></th>
              <th>Ефект<br /><small>грн</small></th>
              <th>УЗЕ нетто<br /><small>грн</small></th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row, idx) => {
              const hourLabel = `${String(idx).padStart(2, '0')}:00`
              if (!row) {
                return (
                  <tr key={idx} className="economics-table-empty">
                    <td>{hourLabel}</td>
                    <td colSpan={17}>—</td>
                  </tr>
                )
              }
              const noPrice = row.rdnUahPerKwh === null
              return (
                <tr key={idx} className={noPrice ? 'economics-table-no-price' : undefined}>
                  <td>{hourLabel}</td>
                  <td>{formatPrice(row.rdnUahPerKwh)}</td>
                  <td>{noPrice ? '—' : formatPrice(row.economics.importPriceUahPerKwh)}</td>
                  <td>{noPrice ? '—' : formatPrice(row.economics.exportPriceUahPerKwh)}</td>
                  <td>{formatNumber(row.economics.load)}</td>
                  <td>{formatNumber(row.economics.pvToLoad)}</td>
                  <td>{formatNumber(row.flow.pvToEss)}</td>
                  <td>{formatNumber(row.economics.pvToGrid)}</td>
                  <td>{formatNumber(row.economics.gridToLoad)}</td>
                  <td>{formatNumber(row.flow.gridToEss)}</td>
                  <td>{formatNumber(row.flow.essToLoad)}</td>
                  <td>{formatNumber(row.flow.essToGrid)}</td>
                  <td>{formatNumber(row.flow.gridImport)}</td>
                  <td>{formatNumber(row.flow.gridExport)}</td>
                  <td>{noPrice ? '—' : formatUah(row.economics.baselineCost)}</td>
                  <td>{noPrice ? '—' : formatUah(row.economics.actualCost)}</td>
                  <td className={row.economics.effect >= 0 ? 'cell-positive' : 'cell-negative'}>
                    {noPrice ? '—' : formatUah(row.economics.effect)}
                  </td>
                  <td className={row.economics.essNet >= 0 ? 'cell-positive' : 'cell-negative'}>
                    {noPrice ? '—' : formatUah(row.economics.essNet)}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    </section>
  )
}
