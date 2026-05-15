import type { DailyTotals } from '../compute'
import type { Tariffs } from '../tariffs'

type Props = {
  totals: DailyTotals
  tariffs: Tariffs
}

const uahFormatter = new Intl.NumberFormat('uk-UA', {
  style: 'currency',
  currency: 'UAH',
  minimumFractionDigits: 0,
  maximumFractionDigits: 0,
})

const kwhFormatter = new Intl.NumberFormat('uk-UA', {
  minimumFractionDigits: 1,
  maximumFractionDigits: 1,
})

const percentFormatter = new Intl.NumberFormat('uk-UA', {
  style: 'percent',
  minimumFractionDigits: 1,
  maximumFractionDigits: 1,
})

const priceFormatter = new Intl.NumberFormat('uk-UA', {
  style: 'currency',
  currency: 'UAH',
  minimumFractionDigits: 2,
  maximumFractionDigits: 2,
})

function formatUah(v: number): string {
  if (!Number.isFinite(v)) return '—'
  return uahFormatter.format(Math.round(v))
}

function formatKwh(v: number): string {
  if (!Number.isFinite(v)) return '—'
  return `${kwhFormatter.format(v)} кВт·год`
}

function formatPercent(v: number): string {
  if (!Number.isFinite(v)) return '—'
  return percentFormatter.format(v)
}

function formatPriceUahPerKwh(v: number): string {
  if (!Number.isFinite(v) || v === 0) return '—'
  return `${priceFormatter.format(v)}/кВт·год`
}

export function EconomicsKpis({ totals, tariffs }: Props) {
  const pvSelfConsumed = totals.pvToLoad + totals.pvToEss
  const pvSelfConsumptionShare = totals.pv > 0 ? pvSelfConsumed / totals.pv : 0
  const equivalentCycles = tariffs.essCapacityKwh > 0 ? totals.essDischarged / tariffs.essCapacityKwh : 0
  const avoidedImportKwh = totals.pvToLoad + totals.essToLoad
  const effectClass = totals.effect >= 0 ? 'kpi-card kpi-card-success' : 'kpi-card kpi-card-danger'
  const essNetClass = totals.essNet >= 0 ? 'kpi-card kpi-card-info' : 'kpi-card kpi-card-warning'

  return (
    <section className="economics-kpis" aria-label="Ключові показники">
      <div className="kpi-strip">
        <div className="kpi-card">
          <span className="kpi-label">Базова вартість (без проєкту)</span>
          <span className="kpi-value">{formatUah(totals.baselineCost)}</span>
          <span className="kpi-sub">100% споживання з мережі</span>
        </div>
        <div className="kpi-card">
          <span className="kpi-label">Фактична вартість</span>
          <span className="kpi-value">{formatUah(totals.actualCost)}</span>
          <span className="kpi-sub">імпорт − експорт + знос УЗЕ</span>
        </div>
        <div className={effectClass}>
          <span className="kpi-label">Ефект проєкту</span>
          <span className="kpi-value">{formatUah(totals.effect)}</span>
          <span className="kpi-sub">{totals.effect >= 0 ? 'економія' : 'переплата'} за день</span>
        </div>
        <div className={essNetClass}>
          <span className="kpi-label">Чистий ефект УЗЕ</span>
          <span className="kpi-value">{formatUah(totals.essNet)}</span>
          <span className="kpi-sub">внесок батареї без СЕС</span>
        </div>
      </div>

      <div className="kpi-secondary-strip">
        <div className="kpi-card kpi-card-secondary">
          <span className="kpi-label">Уникнутий імпорт</span>
          <span className="kpi-value">{formatKwh(avoidedImportKwh)}</span>
          <span className="kpi-sub">PV→споживання + УЗЕ→споживання</span>
        </div>
        <div className="kpi-card kpi-card-secondary">
          <span className="kpi-label">Самоспоживання СЕС</span>
          <span className="kpi-value">{formatPercent(pvSelfConsumptionShare)}</span>
          <span className="kpi-sub">
            {formatKwh(pvSelfConsumed)} з {formatKwh(totals.pv)}
          </span>
        </div>
        <div className="kpi-card kpi-card-secondary">
          <span className="kpi-label">Еквівалентні цикли УЗЕ</span>
          <span className="kpi-value">{equivalentCycles.toFixed(2)}</span>
          <span className="kpi-sub">
            розряд {formatKwh(totals.essDischarged)} / ємність {tariffs.essCapacityKwh} кВт·год
          </span>
        </div>
        <div className="kpi-card kpi-card-secondary">
          <span className="kpi-label">Сер. ціна імпорту</span>
          <span className="kpi-value">{formatPriceUahPerKwh(totals.avgImportPriceUahPerKwh)}</span>
          <span className="kpi-sub">зважена за обсягом імпорту</span>
        </div>
      </div>
    </section>
  )
}
