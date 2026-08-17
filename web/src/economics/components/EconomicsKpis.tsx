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

// formatEodSubtitle composes the "на завтра…" sub-line of the
// realized-profit KPI. Three branches:
//   - empty battery (residual kWh ≈ 0) → "батарея порожня на
//     завтра" so the operator doesn't read the em-dash as a
//     loading error;
//   - PV-only inventory (residual > 0, avg = 0) → just the kWh
//     count + "вільна енергія" so it's clear there's stock but no
//     cash basis to recover;
//   - normal → "X грн/кВт·год · Y кВт·год (Z грн)" surfacing the
//     deferred-profit context that explains why
//     `realized + pvLegs` may differ from `effect` on carry-over
//     days (the cash is "stuck" inside the battery until tomorrow).
function formatEodSubtitle(avgUahPerKwh: number, residualKwh: number, costUah: number): string {
  if (!Number.isFinite(residualKwh) || residualKwh <= 0.01) {
    return 'батарея порожня на завтра'
  }
  if (!Number.isFinite(avgUahPerKwh) || avgUahPerKwh <= 0) {
    return `на завтра — ${formatKwh(residualKwh)} вільної енергії`
  }
  return `на завтра — ${formatPriceUahPerKwh(avgUahPerKwh)} · ${formatKwh(residualKwh)} (${formatUah(costUah)})`
}

export function EconomicsKpis({ totals, tariffs }: Props) {
  const pvSelfConsumed = totals.pvToLoad + totals.pvToEss
  const pvSelfConsumptionShare = totals.pv > 0 ? pvSelfConsumed / totals.pv : 0
  const equivalentCycles = tariffs.essCapacityKwh > 0 ? totals.essDischarged / tariffs.essCapacityKwh : 0
  const avoidedImportKwh = totals.pvToLoad + totals.essToLoad
  const ebitdaClass = totals.ebitda >= 0 ? 'kpi-card kpi-card-success' : 'kpi-card kpi-card-danger'
  // Realized ESS profit replaces the legacy spot `essNet` here:
  // it's the cash the battery actually earned today after each
  // discharge was matched to the WAC cost basis it consumed
  // (PV→УЗЕ at 0 грн, Grid→УЗЕ at the import price of the charge
  // hour). Carries the same color semantics as before — green
  // when battery operations were profitable, amber when not.
  const essRealizedClass =
    totals.essRealizedProfitUah >= 0 ? 'kpi-card kpi-card-info' : 'kpi-card kpi-card-warning'

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
        <div className={`${ebitdaClass} kpi-card-ebitda`} tabIndex={0}>
          <span className="kpi-label">EBITDA за добу</span>
          <span className="kpi-value">{formatUah(totals.ebitda)}</span>
          <span className="kpi-sub">дохід − витрати за день · наведіть для розкладки</span>
          <EbitdaBreakdown totals={totals} />
        </div>
        <div
          className={essRealizedClass}
          title={
            'Cash-flow ESS-частини за умови що сонце безплатне.' +
            ' WAC-облік: PV→УЗЕ заходить за 0 грн/кВт·год,' +
            ' Мережа→УЗЕ — за повним імпортним стеком цієї години.' +
            ' Розряди списуються за середньою; знос УЗЕ віднімається.' +
            ' Деталі — у блоці «Як рахуємо собівартість УЗЕ?» під таблицею.'
          }
        >
          <span className="kpi-label">Реалізований ефект УЗЕ</span>
          <span className="kpi-value">{formatUah(totals.essRealizedProfitUah)}</span>
          <span className="kpi-sub">
            {formatEodSubtitle(
              totals.essAvgCostBasisUahPerKwhEod,
              totals.essResidualKwhEod,
              totals.essCostBasisUahEod,
            )}
          </span>
        </div>
        <div
          className="kpi-card"
          title={
            'Увесь виробіток СЕС за добу, оцінений ціною експорту тієї самої години' +
            ' (РДН мінус знижка, з ПДВ): скільки б заробили, продавши все в мережу —' +
            ' без УЗЕ і без власного споживання.' +
            ' Орієнтир для порівняння з EBITDA: кіловат-година, залишена на об’єкті,' +
            ' заміщає дорожчий імпорт, а УЗЕ переносить її на дорожчі години.'
          }
        >
          <span className="kpi-label">Потенціал СЕС</span>
          <span className="kpi-value">{formatUah(totals.pvExportPotential)}</span>
          <span className="kpi-sub">весь виробіток у мережу · {formatKwh(totals.pv)}</span>
        </div>
      </div>

      <div className="kpi-secondary-strip">
        <div className="kpi-card kpi-card-secondary">
          <span className="kpi-label">Уникнутий імпорт</span>
          <span className="kpi-value">{formatKwh(avoidedImportKwh)}</span>
          <span className="kpi-sub">СЕС→споживання + УЗЕ→споживання</span>
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
            розряд {formatKwh(totals.essDischarged)} / корисна ємність {tariffs.essCapacityKwh} кВт·год
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

// EbitdaBreakdown is the revenue/expense detail that used to live in a
// standalone panel; it now appears as a hover/focus popover under the
// EBITDA KPI card so the strip can stay full-width above the table.
function EbitdaBreakdown({ totals }: { totals: DailyTotals }) {
  const revenueLines = [
    { label: 'СЕС → мережа', amount: totals.revenuePvExport },
    { label: 'СЕС → споживання', amount: totals.revenuePvSelf },
    { label: 'УЗЕ → мережа', amount: totals.revenueEssExport },
    { label: 'УЗЕ → споживання', amount: totals.revenueEssSelf },
  ]
  const expenseLines = [{ label: 'Заряд УЗЕ із мережі', amount: totals.expenseGridCharge }]

  return (
    <div className="economics-ebitda-breakdown" role="tooltip">
      <div className="economics-ebitda-col">
        <div className="economics-ebitda-col-head">
          <span>Дохід</span>
          <span>{formatUah(totals.revenueTotal)}</span>
        </div>
        {revenueLines.map((line) => (
          <div className="economics-ebitda-line" key={line.label}>
            <span>{line.label}</span>
            <b>{formatUah(line.amount)}</b>
          </div>
        ))}
      </div>
      <div className="economics-ebitda-col">
        <div className="economics-ebitda-col-head">
          <span>Витрати</span>
          <span>{formatUah(totals.expenseTotal)}</span>
        </div>
        {expenseLines.map((line) => (
          <div className="economics-ebitda-line" key={line.label}>
            <span>{line.label}</span>
            <b>{formatUah(line.amount)}</b>
          </div>
        ))}
      </div>
    </div>
  )
}
