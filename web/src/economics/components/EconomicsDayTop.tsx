import { Cell, Line, LineChart, CartesianGrid, Pie, PieChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import type { DailyTotals, HourEconomicsRow } from '../compute'
import type { Tariffs } from '../tariffs'
import { useChartChrome } from '../../theme/useChartChrome'
import { Card, InfoDot } from './EconomicsTopSection'
import type { EconomicsPvPlan } from './EconomicsTopSection'
import { KpiIcon } from './kpiIcons'
import { KPI_ICONS as ICONS } from './kpiIconPaths'

type Props = {
  totals: DailyTotals
  rows: Array<HourEconomicsRow | null>
  tariffs: Tariffs
  // pvPlan — денний план генерації з pv-plan-summary; null ховає рядки плану.
  pvPlan?: EconomicsPvPlan | null
}

// --- Formatting (day scale: кВт·год with one decimal, грн integer) ---------

const uahFmt = new Intl.NumberFormat('uk-UA', { maximumFractionDigits: 0 })
const kwhFmt = new Intl.NumberFormat('uk-UA', { minimumFractionDigits: 1, maximumFractionDigits: 1 })
const priceFmt = new Intl.NumberFormat('uk-UA', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
const pctFmt = new Intl.NumberFormat('uk-UA', { style: 'percent', minimumFractionDigits: 1, maximumFractionDigits: 1 })
const cyclesFmt = new Intl.NumberFormat('uk-UA', { minimumFractionDigits: 2, maximumFractionDigits: 2 })

function uah(v: number): string {
  if (!Number.isFinite(v)) return '—'
  return `${uahFmt.format(Math.round(v))} грн`
}

function kwh(v: number): string {
  if (!Number.isFinite(v)) return '—'
  return `${kwhFmt.format(v)} кВт·год`
}

function kw(v: number): string {
  if (!Number.isFinite(v)) return '—'
  return `${kwhFmt.format(v)} кВт`
}

function pct(v: number): string {
  if (!Number.isFinite(v)) return '—'
  return pctFmt.format(v)
}

function price(v: number): string {
  if (!Number.isFinite(v)) return '—'
  return priceFmt.format(v)
}

// FactLine is one "label: value" row of a card's financial breakdown;
// `total` draws the «Разом» divider like in the mock.
function FactLine({ label, value, total = false }: { label: string; value: string; total?: boolean }) {
  return (
    <span className={total ? 'eco-fact-line eco-fact-total' : 'eco-fact-line'}>
      <span>{label}:</span>
      <b>{value}</b>
    </span>
  )
}

// Series palette shared with the other economics charts.
const COLOR_PV = '#12b76a'
const COLOR_IMPORT = '#2f6fed'
const COLOR_ESS = '#7c3aed'
const COLOR_TOTAL = '#f59e0b'

const chartUahFmt = new Intl.NumberFormat('uk-UA', { maximumFractionDigits: 0 })

type HourTooltipProps = {
  active?: boolean
  label?: string | number
  payload?: Array<{ name?: string; value?: number | string; color?: string }>
}

function HourTooltip({ active, payload, label }: HourTooltipProps) {
  if (!active || !payload?.length) return null
  return (
    <div className="economics-trend-tip">
      <div className="economics-trend-tip-day">{label}:00</div>
      {payload.map((p) => (
        <div className="economics-trend-tip-row" key={p.name}>
          <i style={{ background: p.color }} />
          <span>{p.name}</span>
          <b>{chartUahFmt.format(Number(p.value))}</b>
        </div>
      ))}
    </div>
  )
}

// EconomicsDayTop is the redesigned day view top per the client mock:
// a strip of financial cards with their per-component breakdowns, the
// day energy balance strip, and the coverage donut / hourly effect
// chart / «Робота УЗЕ» row. The detailed hourly table stays below.
export function EconomicsDayTop({ totals: t, rows, tariffs, pvPlan = null }: Props) {
  const chrome = useChartChrome()

  // Tariff components per kWh, VAT'd the same way hourEconomics builds
  // the import price stack, so every breakdown line below sums exactly
  // to the headline it belongs to.
  const vat = tariffs.includeVat ? 1 + tariffs.vatRate : 1
  const transPerKwh = tariffs.transmissionUahPerKwh * vat
  const distPerKwh = tariffs.distributionUahPerKwh * vat
  const netPerKwh = transPerKwh + distPerKwh

  // Базова вартість: load × full import stack. The network legs are
  // flat per-kWh tariffs, so the market/margin part is the remainder.
  const baseTrans = t.load * transPerKwh
  const baseDist = t.load * distPerKwh
  const baseEnergy = t.baselineCost - baseTrans - baseDist

  // Фактична вартість: import − export revenue + УЗЕ wear (the exact
  // actualCost identity from hourEconomics).
  const importCost = t.avgImportPriceUahPerKwh * t.gridImport
  const importNetwork = t.gridImport * netPerKwh
  const importEnergy = importCost - importNetwork
  const exportRevenue = t.revenuePvExport + t.revenueEssExport
  const wear = t.essDischarged * tariffs.degradationUahPerKwh

  // Економічний ефект = базова − фактична, розкладена на ті самі
  // складові: зекономлена енергія, зекономлена мережа, дохід від
  // експорту, мінус знос. Суми сходяться алгебраїчно.
  const effEnergy = baseEnergy - importEnergy
  const effNetwork = (t.load - t.gridImport) * netPerKwh

  // Ефекти СЕС і УЗЕ.
  const pvEffect = t.revenuePvSelf + t.revenuePvExport
  const essChargeAndWear = t.essNet - t.revenueEssSelf - t.revenueEssExport

  // Енергетичний баланс.
  const hoursWithData = Math.max(1, t.hoursWithData)
  const peakLoadKw = rows.reduce((m, r) => (r ? Math.max(m, r.economics.load) : m), 0)
  const avgLoadKw = t.load / hoursWithData
  const importShare = t.load > 0 ? t.gridImport / t.load : NaN
  const exportShare = t.pv > 0 ? t.gridExport / t.pv : NaN
  const ownEnergy = t.pvToLoad + t.essToLoad
  const selfSuff = t.load > 0 ? ownEnergy / t.load : NaN
  const importDependence = t.load > 0 ? t.gridToLoad / t.load : NaN
  const pvSelfConsumed = t.pvToLoad + t.pvToEss
  const pvSelfShare = t.pv > 0 ? pvSelfConsumed / t.pv : NaN
  const planKwh = pvPlan && pvPlan.plannedKwh > 0 ? pvPlan.plannedKwh : NaN
  const planDone = Number.isFinite(planKwh) ? t.pv / planKwh : NaN

  // Донат покриття споживання.
  const coverage = [
    { key: 'pv', name: 'СЕС — навантаження', kwhValue: t.pvToLoad, color: COLOR_PV },
    { key: 'ess', name: 'УЗЕ — навантаження', kwhValue: t.essToLoad, color: COLOR_ESS },
    { key: 'grid', name: 'Імпорт з мережі', kwhValue: t.gridToLoad, color: COLOR_IMPORT },
  ]
  const coverageTotal = coverage.reduce((acc, s) => acc + s.kwhValue, 0)

  // Погодинний економічний ефект: СЕС-нога (власне споживання +
  // експорт за цінами години), УЗЕ-нога (essNet) і сукупний effect.
  const hourly = rows.map((r, i) => {
    const label = String(i).padStart(2, '0')
    if (!r || r.rdnUahPerKwh === null) {
      return { label, 'Ефект СЕС': null, 'Ефект УЗЕ': null, 'Сукупний ефект': null }
    }
    const pvEff =
      r.economics.pvToLoad * r.economics.importPriceUahPerKwh +
      r.economics.pvToGrid * r.economics.exportPriceUahPerKwh
    return {
      label,
      'Ефект СЕС': Math.round(pvEff),
      'Ефект УЗЕ': Math.round(r.economics.essNet),
      'Сукупний ефект': Math.round(r.economics.effect),
    }
  })

  // Робота УЗЕ.
  const cycles = tariffs.essCapacityKwh > 0 ? t.essDischarged / tariffs.essCapacityKwh : NaN
  const dischargeMargin = t.essDischarged > 0 ? t.essNet / t.essDischarged : NaN
  const uzeCards = [
    {
      label: 'Коефіцієнт циклів',
      value: Number.isFinite(cycles) ? cyclesFmt.format(cycles) : '—',
      sub: `${kwhFmt.format(t.essDischarged)} / ${tariffs.essCapacityKwh} кВт·год`,
      tone: 'teal',
      icon: ICONS.cycle,
    },
    {
      label: 'Розряд УЗЕ',
      value: kwh(t.essDischarged),
      sub: `навантаження ${kwhFmt.format(t.essToLoad)} · мережа ${kwhFmt.format(t.essToGrid)}`,
      tone: 'orange',
      icon: ICONS.zap,
    },
    {
      label: 'Заряд УЗЕ',
      value: kwh(t.essCharged),
      sub: `СЕС: ${kwhFmt.format(t.pvToEss)} | Мережа: ${kwhFmt.format(t.gridToEss)}`,
      tone: 'green',
      icon: ICONS.battery,
    },
    {
      label: 'Сер. маржа розряду',
      value: `${price(dischargeMargin)} грн/кВт·год`,
      sub: `${uahFmt.format(Math.round(t.essNet))} грн / ${kwhFmt.format(t.essDischarged)} кВт·год`,
      tone: 'sky',
      icon: ICONS.chart,
    },
  ]

  return (
    <div className="economics-top">
      <div className="eco-kpi-grid eco-kpi-grid-day">
        <Card
          tone="steel"
          icon={ICONS.database}
          label="Базова вартість (без проєкту)"
          tip="Усе споживання доби, оцінене повною ціною імпорту кожної години: скільки коштувала б енергія без СЕС та УЗЕ."
          value={uah(t.baselineCost)}
        >
          <FactLine label="Енергія з мережі" value={uah(baseEnergy)} />
          <FactLine label="Передача" value={uah(baseTrans)} />
          <FactLine label="Розподіл" value={uah(baseDist)} />
        </Card>

        <Card tone="teal" icon={ICONS.briefcase} label="Фактична вартість" value={uah(t.actualCost)}>
          <FactLine label="Імпорт енергії" value={uah(importEnergy)} />
          <FactLine label="Передача + розподіл" value={uah(importNetwork)} />
          <FactLine label="Дохід від експорту" value={uah(-exportRevenue)} />
          <FactLine label="Знос УЗЕ" value={uah(wear)} />
          <FactLine label="Разом" value={uah(t.actualCost)} total />
        </Card>

        <Card
          tone="green"
          icon={ICONS.trendUp}
          label="Економічний ефект за добу"
          tip="Базова вартість мінус фактична: наскільки дешевше обійшлася доба завдяки СЕС та УЗЕ."
          value={uah(t.effect)}
          hero
        >
          <FactLine label="Економія на імпорті" value={uah(effEnergy)} />
          <FactLine label="Економія на мережах" value={uah(effNetwork)} />
          <FactLine label="Дохід від експорту" value={uah(exportRevenue)} />
          <FactLine label="Знос УЗЕ" value={uah(-wear)} />
          <FactLine label="Разом" value={uah(t.effect)} total />
        </Card>

        <Card
          tone="amber"
          icon={ICONS.sun}
          label="Ефект СЕС"
          tip="Внесок СЕС: власне споживання за ціною імпорту тієї ж години + експорт за ціною експорту."
          value={uah(pvEffect)}
          hero
        >
          <FactLine label="Власне споживання" value={uah(t.revenuePvSelf)} />
          <FactLine label="Експорт" value={uah(t.revenuePvExport)} />
        </Card>

        <Card
          tone="violet"
          icon={ICONS.battery}
          label="Ефект УЗЕ"
          tip="Чистий внесок батареї: розряд у споживання та мережу мінус вартість заряду (включно з втраченим експортом СЕС) і знос."
          value={uah(t.essNet)}
          hero
        >
          <FactLine label="Зменшення імпорту" value={uah(t.revenueEssSelf)} />
          <FactLine label="Арбітраж (експорт)" value={uah(t.revenueEssExport)} />
          <FactLine label="Заряд і знос" value={uah(essChargeAndWear)} />
        </Card>

        <div className="eco-price-stack">
          <Card
            tone="sky"
            icon={ICONS.tag}
            label="Сер. ціна імпорту"
            value={`${price(t.avgImportPriceUahPerKwh)} грн/кВт·год`}
          >
            <span>зважена за обсягом імпорту</span>
          </Card>
          <Card
            tone="blue"
            icon={ICONS.tag}
            label="Сер. ціна експорту"
            value={`${price(t.avgExportPriceUahPerKwh)} грн/кВт·год`}
          >
            <span>зважена за обсягом експорту</span>
          </Card>
        </div>
      </div>

      <div className="eco-kpi-grid">
        <Card tone="sky" icon={ICONS.home} label="Споживання об'єкта" value={kwh(t.load)}>
          <span>
            Пік: {kw(peakLoadKw)} · Сер: {kw(avgLoadKw)}
          </span>
        </Card>

        <Card tone="amber" icon={ICONS.sun} label="Генерація СЕС" value={kwh(t.pv)}>
          {Number.isFinite(planKwh) ? (
            <>
              <span>План: {kwh(planKwh)}</span>
              <span>Виконання плану: {pct(planDone)}</span>
            </>
          ) : (
            <span>план на день недоступний</span>
          )}
        </Card>

        <Card tone="blue" icon={ICONS.tower} label="Імпорт з мережі" value={kwh(t.gridImport)}>
          <span>{pct(importShare)} від споживання</span>
        </Card>

        <Card tone="orange" icon={ICONS.exportArrow} label="Експорт у мережу" value={kwh(t.gridExport)}>
          <span>{pct(exportShare)} від генерації СЕС</span>
        </Card>

        <Card
          tone="teal"
          icon={ICONS.clock}
          label="Самозабезпечення"
          tip="Частка споживання, покрита власними СЕС та УЗЕ замість імпорту."
          value={pct(selfSuff)}
        >
          <span>Власна енергія: {kwh(ownEnergy)}</span>
          <span>Імпортозалежність: {pct(importDependence)}</span>
        </Card>

        <Card
          tone="green"
          icon={ICONS.cycle}
          label="Самоспоживання СЕС"
          tip="Частка виробітку СЕС, використана на об'єкті (споживання + заряд УЗЕ), а не експортована."
          value={pct(pvSelfShare)}
        >
          <span>
            {kwhFmt.format(pvSelfConsumed)} з {kwh(t.pv)}
          </span>
        </Card>
      </div>

      <div className="eco-overview">
        <section className="economics-card eco-overview-panel" aria-label="Структура покриття споживання">
          <h3 className="eco-overview-title">Структура покриття споживання</h3>
          <div className="eco-donut-wrap">
            <div className="eco-donut">
              <ResponsiveContainer width="100%" height={170}>
                <PieChart>
                  <Pie
                    data={coverage}
                    dataKey="kwhValue"
                    nameKey="name"
                    innerRadius={52}
                    outerRadius={78}
                    strokeWidth={0}
                    paddingAngle={2}
                  >
                    {coverage.map((s) => (
                      <Cell key={s.key} fill={s.color} />
                    ))}
                  </Pie>
                </PieChart>
              </ResponsiveContainer>
              <div className="eco-donut-center">
                <b>{kwhFmt.format(coverageTotal)}</b>
                <span>кВт·год</span>
                <span>усього</span>
              </div>
            </div>
            <div className="eco-donut-legend">
              {coverage.map((s) => (
                <div className="eco-donut-legend-row" key={s.key}>
                  <i style={{ background: s.color }} />
                  <span className="eco-donut-legend-name">{s.name}</span>
                  <b>{coverageTotal > 0 ? pct(s.kwhValue / coverageTotal) : '—'}</b>
                  <span className="eco-donut-legend-mwh">{kwhFmt.format(s.kwhValue)}</span>
                </div>
              ))}
            </div>
          </div>
        </section>

        <section className="economics-card eco-overview-panel" aria-label="Погодинний економічний ефект">
          <div className="eco-overview-head">
            <h3 className="eco-overview-title">Погодинний економічний ефект</h3>
            <span className="economics-month-muted">грн/год</span>
          </div>
          <div className="eco-profile-chart">
            <ResponsiveContainer width="100%" height={190}>
              <LineChart data={hourly} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
                <CartesianGrid strokeDasharray="2 5" stroke={chrome.grid} vertical={false} />
                <XAxis dataKey="label" tick={{ fontSize: 10, fill: chrome.tick }} tickLine={false} axisLine={false} />
                <YAxis tick={{ fontSize: 11, fill: chrome.axis }} width={42} tickLine={false} axisLine={false} />
                <Tooltip content={<HourTooltip />} cursor={{ stroke: chrome.cursorStroke, strokeDasharray: '3 3' }} />
                <Line type="monotone" dataKey="Ефект СЕС" stroke={COLOR_PV} strokeWidth={2} dot={{ r: 2 }} />
                <Line type="monotone" dataKey="Ефект УЗЕ" stroke={COLOR_IMPORT} strokeWidth={2} dot={{ r: 2 }} />
                <Line type="monotone" dataKey="Сукупний ефект" stroke={COLOR_TOTAL} strokeWidth={2} dot={{ r: 2 }} />
              </LineChart>
            </ResponsiveContainer>
          </div>
          <div className="economics-trend-legend">
            <span><i style={{ background: COLOR_PV }} />ефект СЕС</span>
            <span><i style={{ background: COLOR_IMPORT }} />ефект УЗЕ</span>
            <span><i style={{ background: COLOR_TOTAL }} />сукупний ефект</span>
          </div>
        </section>

        <section className="economics-card eco-overview-panel" aria-label="Робота УЗЕ">
          <h3 className="eco-overview-title">Робота УЗЕ</h3>
          <div className="eco-uze-grid">
            {uzeCards.map((c) => (
              <div className={`eco-uze-card eco-tone-${c.tone}`} key={c.label}>
                <span className="eco-uze-icon">
                  <KpiIcon d={c.icon} />
                </span>
                <div className="eco-uze-body">
                  <span className="eco-uze-label">
                    {c.label}
                    {c.label === 'Сер. маржа розряду' ? (
                      <InfoDot tip="Чистий ефект УЗЕ, поділений на відданий розряд: скільки заробила кожна віддана кВт·год." />
                    ) : null}
                  </span>
                  <span className="eco-uze-value">{c.value}</span>
                  <span className="eco-uze-sub">{c.sub}</span>
                </div>
              </div>
            ))}
          </div>
        </section>
      </div>
    </div>
  )
}
