import type { ReactElement } from 'react'
import type { DailyTotals } from '../compute'

type Props = {
  totals: DailyTotals
}

const uahFmt = new Intl.NumberFormat('uk-UA', {
  style: 'decimal',
  useGrouping: true,
  minimumFractionDigits: 0,
  maximumFractionDigits: 0,
})

const percentFmt = new Intl.NumberFormat('uk-UA', {
  style: 'percent',
  minimumFractionDigits: 0,
  maximumFractionDigits: 0,
})

function formatUah(v: number): string {
  if (!Number.isFinite(v)) return '—'
  return `${uahFmt.format(Math.round(v))} грн`
}

function formatShare(value: number, total: number): string {
  if (!Number.isFinite(value) || !Number.isFinite(total) || total <= 0) return '—'
  return percentFmt.format(value / total)
}

type Line = {
  label: string
  amount: number
}

export function EconomicsRevenuePanel({ totals }: Props): ReactElement {
  // The four revenue channels mirror the spreadsheet's "Дохід" rows:
  // PV→export and PV→load make up the СЕС contribution; ESS→grid and
  // ESS→load make up the УЗЕ contribution. Order is fixed (PV first,
  // then УЗЕ) so the operator can pattern-match between days.
  const revenueLines: Line[] = [
    { label: 'СЕС → мережа', amount: totals.revenuePvExport },
    { label: 'СЕС → споживання', amount: totals.revenuePvSelf },
    { label: 'УЗЕ → мережа', amount: totals.revenueEssExport },
    { label: 'УЗЕ → споживання', amount: totals.revenueEssSelf },
  ]
  const expenseLines: Line[] = [
    { label: 'Заряд УЗЕ із мережі', amount: totals.expenseGridCharge },
  ]

  const ebitdaClass =
    totals.ebitda >= 0
      ? 'economics-revenue-ebitda-card positive'
      : 'economics-revenue-ebitda-card negative'

  return (
    <section className="economics-revenue-panel" aria-label="EBITDA та розкладка ефекту">
      <div className="economics-revenue-grid">
        <div className={ebitdaClass}>
          <span className="economics-revenue-ebitda-label">EBITDA за добу</span>
          <span className="economics-revenue-ebitda-value">{formatUah(totals.ebitda)}</span>
        </div>
        <RevenueColumn
          title="Дохід"
          total={totals.revenueTotal}
          lines={revenueLines}
          showPercent
        />
        <RevenueColumn
          title="Витрати"
          total={totals.expenseTotal}
          lines={expenseLines}
          showPercent={false}
        />
      </div>
    </section>
  )
}

function RevenueColumn({
  title,
  total,
  lines,
  showPercent,
}: {
  title: string
  total: number
  lines: Line[]
  showPercent: boolean
}): ReactElement {
  return (
    <div className="economics-revenue-column">
      <div className="economics-revenue-column-header">
        <span className="economics-revenue-column-title">{title}</span>
        <span className="economics-revenue-column-total">{formatUah(total)}</span>
        <span className="economics-revenue-column-share-spacer" aria-hidden="true" />
      </div>
      <ul className="economics-revenue-list">
        {lines.map((line) => (
          <li key={line.label} className="economics-revenue-line">
            <span className="economics-revenue-line-label">{line.label}</span>
            <span className="economics-revenue-line-amount">{formatUah(line.amount)}</span>
            {showPercent ? (
              <span className="economics-revenue-line-share">{formatShare(line.amount, total)}</span>
            ) : (
              <span className="economics-revenue-line-share" aria-hidden="true" />
            )}
          </li>
        ))}
      </ul>
    </div>
  )
}
