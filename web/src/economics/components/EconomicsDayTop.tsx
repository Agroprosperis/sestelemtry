import { Line, LineChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { offgridMinutes, type DailyTotals, type HourEconomicsRow } from '../compute'
import { useMoney } from '../currency'
import { formatDuration, fuelMethodLabel } from '../generatorText'
import type { Tariffs } from '../tariffs'
import { useChartChrome } from '../../theme/useChartChrome'
import { PV_FORECAST_WHOLE_ELEVATOR_NOTE, pvPlanCaption } from '../../dashboard/transforms/pvForecast'
import { cycles, dayFigures, dayFormulas, kw, kwh, kwhNumber, pct } from './dayFormulas'
import { Card } from './EconomicsTopSection'
import type { EconomicsPvPlan } from './EconomicsTopSection'
import { FormulaTip } from './FormulaTip'
import { KPI_ICONS as ICONS } from './kpiIconPaths'
import { CoverageDonutPanel, UzeWorkPanel, type CoverageSegment, type UzeWorkCard } from './OverviewPanels'

type Props = {
  totals: DailyTotals
  rows: Array<HourEconomicsRow | null>
  // tariffs is the tariff version the day was priced with (the form's
  // when the day did not say).
  tariffs: Tariffs
  // pvPlan — денний план генерації з pv-plan-summary; null ховає рядки плану.
  pvPlan?: EconomicsPvPlan | null
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
const COLOR_GEN = '#c2410c'

type HourTooltipProps = {
  active?: boolean
  label?: string | number
  payload?: Array<{ name?: string; value?: number | string; color?: string }>
}

function HourTooltip({ active, payload, label }: HourTooltipProps) {
  const money = useMoney()
  if (!active || !payload?.length) return null
  return (
    <div className="economics-trend-tip">
      <div className="economics-trend-tip-day">{label}:00</div>
      {payload.map((p) => (
        <div className="economics-trend-tip-row" key={p.name}>
          <i style={{ background: p.color }} />
          <span>{p.name}</span>
          <b>{money.formatNumber(Number(p.value))}</b>
        </div>
      ))}
    </div>
  )
}

// EconomicsDayTop is the redesigned day view top per the client mock:
// a strip of financial cards with their per-component breakdowns, the
// day energy balance strip, and the coverage donut / hourly effect
// chart / «Робота УЗЕ» row. The detailed hourly table stays below.
// Every figure's «i» opens how it is computed, with the day's numbers.
export function EconomicsDayTop({ totals: t, rows, tariffs, pvPlan = null }: Props) {
  const chrome = useChartChrome()
  const money = useMoney()
  const uah = money.format
  const price = money.formatPriceAllowZero
  // Hourly chart points keep cents outside UAH, where a whole unit is
  // forty hryvnias.
  const point = (v: number) => (money.currency === 'UAH' ? Math.round(v) : Math.round(v * 100) / 100)

  const f = dayFigures(t, rows, tariffs, money, pvPlan)
  const fx = dayFormulas(t, f, rows, tariffs, money)

  // Донат покриття споживання.
  const coverage: CoverageSegment[] = [
    { key: 'pv', name: 'СЕС — навантаження', kwh: t.pvToLoad, color: COLOR_PV },
    { key: 'ess', name: 'УЗЕ — навантаження', kwh: t.essToLoad, color: COLOR_ESS },
    { key: 'grid', name: 'Імпорт з мережі', kwh: t.gridToLoad, color: COLOR_IMPORT },
  ]
  if (t.genToLoad > 0) {
    coverage.push({ key: 'gen', name: 'Генератор — навантаження', kwh: t.genToLoad, color: COLOR_GEN })
  }

  // Погодинний економічний ефект: СЕС-нога (effect − essNet, як на
  // картці), УЗЕ-нога (essNet) і сукупний effect.
  const hourly = rows.map((r, i) => {
    const label = String(i).padStart(2, '0')
    if (!r || r.rdnUahPerKwh === null) {
      return { label, 'Ефект СЕС': null, 'Ефект УЗЕ': null, 'Сукупний ефект': null }
    }
    return {
      label,
      'Ефект СЕС': point(r.economics.effect - r.economics.essNet),
      'Ефект УЗЕ': point(r.economics.essNet),
      'Сукупний ефект': point(r.economics.effect),
    }
  })

  // Робота УЗЕ.
  const uzeCards: UzeWorkCard[] = [
    {
      label: 'Коефіцієнт циклів',
      value: cycles(f.cycleRatio),
      sub: `${kwhNumber(t.essDischarged)} / ${tariffs.essCapacityKwh} кВт·год`,
      tone: 'teal',
      icon: ICONS.cycle,
      formula: fx.cycles,
    },
    {
      label: 'Розряд УЗЕ',
      value: kwhNumber(t.essDischarged),
      unit: 'кВт·год',
      sub: `навантаження ${kwhNumber(t.essToLoad)} · мережа ${kwhNumber(t.essToGrid)}`,
      tone: 'orange',
      icon: ICONS.zap,
      formula: fx.discharge,
    },
    {
      label: 'Заряд УЗЕ',
      value: kwhNumber(t.essCharged),
      unit: 'кВт·год',
      sub: `СЕС: ${kwhNumber(t.pvToEss)} | Мережа: ${kwhNumber(t.gridToEss)}`,
      tone: 'green',
      icon: ICONS.battery,
      formula: fx.charge,
    },
    {
      label: 'Сер. маржа розряду',
      value: price(f.dischargeMargin),
      unit: money.priceUnit,
      sub: `${uah(t.essNet)} / ${kwhNumber(t.essDischarged)} кВт·год`,
      tone: 'sky',
      icon: ICONS.chart,
      formula: fx.margin,
    },
  ]

  return (
    <div className="economics-top">
      <div className="eco-kpi-grid eco-kpi-grid-day">
        <Card
          tone="steel"
          icon={ICONS.database}
          label="Базова вартість (без проєкту)"
          formula={fx.baseline}
          value={uah(t.baselineCost)}
        >
          <FactLine label="Енергія з мережі" value={uah(f.baseEnergy)} />
          <FactLine label="Передача" value={uah(f.baseTrans)} />
          <FactLine label="Розподіл" value={uah(f.baseDist)} />
          {f.genAccounted && <FactLine label="Без мережі — генератором" value={uah(t.baselineOffgrid)} />}
        </Card>

        <Card tone="teal" icon={ICONS.briefcase} label="Фактична вартість" formula={fx.actual} value={uah(t.actualCost)}>
          <FactLine label="Імпорт енергії" value={uah(f.importEnergy)} />
          <FactLine label="Передача + розподіл" value={uah(f.importNetwork)} />
          <FactLine label="Дохід від експорту" value={uah(-f.exportRevenue)} />
          <FactLine label="Знос УЗЕ" value={uah(f.wear)} />
          {f.genAccounted && <FactLine label="Пальне генератора" value={uah(t.genFuelCost)} />}
          <FactLine label="Разом" value={uah(t.actualCost)} total />
        </Card>

        <Card
          tone="green"
          icon={ICONS.trendUp}
          label="Економічний ефект за добу"
          formula={fx.effect}
          value={uah(t.effect)}
          hero
        >
          <FactLine label="Економія на імпорті" value={uah(f.effEnergy)} />
          <FactLine label="Економія на мережах" value={uah(f.effNetwork)} />
          <FactLine label="Дохід від експорту" value={uah(f.exportRevenue)} />
          <FactLine label="Знос УЗЕ" value={uah(-f.wear)} />
          {f.genAccounted && <FactLine label="Без мережі: зекономлене пальне" value={uah(f.effOffgrid)} />}
          <FactLine label="Разом" value={uah(t.effect)} total />
        </Card>

        <Card tone="amber" icon={ICONS.sun} label="Ефект СЕС" formula={fx.pvEffect} value={uah(f.pvEffect)} hero>
          <FactLine label="Власне споживання" value={uah(t.revenuePvSelf)} />
          <FactLine label="Експорт" value={uah(t.revenuePvExport)} />
          {Math.abs(f.pvToEssValue) >= money.fromUah(0.5) && (
            <FactLine label="Заряд УЗЕ від СЕС" value={uah(f.pvToEssValue)} />
          )}
        </Card>

        <Card tone="violet" icon={ICONS.battery} label="Ефект УЗЕ" formula={fx.essEffect} value={uah(t.essNet)} hero>
          <FactLine label="Зменшення імпорту" value={uah(t.revenueEssSelf)} />
          <FactLine label="Арбітраж (експорт)" value={uah(t.revenueEssExport)} />
          <FactLine label="Заряд і знос" value={uah(f.essChargeAndWear)} />
        </Card>

        <div className="eco-price-stack">
          <Card
            tone="sky"
            icon={ICONS.tag}
            label="Сер. ціна імпорту"
            formula={fx.importPrice}
            value={price(t.avgImportPriceUahPerKwh)}
            unit={money.priceUnit}
          />
          <Card
            tone="blue"
            icon={ICONS.tag}
            label="Сер. ціна експорту"
            formula={fx.exportPrice}
            value={price(t.avgExportPriceUahPerKwh)}
            unit={money.priceUnit}
          />
        </div>
      </div>

      <div className="eco-kpi-grid eco-kpi-grid-6">
        <Card tone="sky" icon={ICONS.home} label="Споживання об'єкта" formula={fx.load} value={kwh(t.load)}>
          <span>
            Пік: {kw(f.peakLoadKw)} · Сер: {kw(f.avgLoadKw)}
          </span>
        </Card>

        <Card tone="amber" icon={ICONS.sun} label="Генерація СЕС" formula={fx.pv} value={kwh(t.pv)}>
          {pvPlan && Number.isFinite(f.planKwh) ? (
            <>
              <span title={pvPlan.wholeElevator ? PV_FORECAST_WHOLE_ELEVATOR_NOTE : undefined}>
                {pvPlanCaption(pvPlan.wholeElevator)}: {kwh(f.planKwh)}
              </span>
              <span>Виконання плану: {pct(f.planDone)}</span>
            </>
          ) : (
            <span>план на день недоступний</span>
          )}
        </Card>

        <Card tone="blue" icon={ICONS.tower} label="Імпорт з мережі" formula={fx.gridImport} value={kwh(t.gridImport)}>
          <span>{pct(f.importShare)} від споживання</span>
        </Card>

        <Card tone="orange" icon={ICONS.exportArrow} label="Експорт у мережу" formula={fx.gridExport} value={kwh(t.gridExport)}>
          <span>{pct(f.exportShare)} від генерації СЕС</span>
        </Card>

        <Card tone="teal" icon={ICONS.clock} label="Самозабезпечення" formula={fx.selfSuff} value={pct(f.selfSuff)}>
          <span>Власна енергія: {kwh(f.ownEnergy)}</span>
          <span>Імпортозалежність: {pct(f.importDependence)}</span>
        </Card>

        <Card tone="green" icon={ICONS.cycle} label="Самоспоживання СЕС" formula={fx.pvSelf} value={pct(f.pvSelfShare)}>
          <span>
            {kwhNumber(f.pvSelfConsumed)} з {kwh(t.pv)}
          </span>
        </Card>
      </div>

      {f.hasGen && (
        <div className="eco-kpi-grid eco-kpi-grid-4" aria-label="Генератор за добу">
          <Card tone="orange" icon={ICONS.zap} label="Генератор" formula={fx.gen} value={kwh(t.genKwh)}>
            <span>У роботі: {formatDuration(t.supplyMinutes.generator)}</span>
            {f.genAccounted && (
              <span>
                Навантаженню {kwhNumber(t.genToLoad)} · УЗЕ {kwhNumber(t.genToEss)} кВт·год
              </span>
            )}
          </Card>
          <Card
            tone="amber"
            icon={ICONS.tag}
            label="Пальне"
            formula={fx.fuel}
            value={f.genAccounted ? uah(t.genFuelCost) : '—'}
          >
            {f.genAccounted ? (
              <span>
                {kwhNumber(t.genFuelL)} л · {fuelMethodLabel(f.fuelMethod)}
              </span>
            ) : (
              <span>вартість пального не задана</span>
            )}
          </Card>
          <Card
            tone="steel"
            icon={ICONS.clock}
            label="Без мережі"
            formula={fx.offgrid}
            value={formatDuration(offgridMinutes(t.supplyMinutes))}
          >
            <span>
              Генератор {formatDuration(t.supplyMinutes.generator)} · острів {formatDuration(t.supplyMinutes.island)} ·
              без живлення {formatDuration(t.supplyMinutes.blackout)}
            </span>
            {f.genAccounted && <span>Навантаження без мережі: {kwh(t.loadOffgrid)}</span>}
          </Card>
          <Card
            tone="green"
            icon={ICONS.trendUp}
            label="Ефект годин без мережі"
            formula={fx.effectOffgrid}
            value={f.genAccounted ? uah(t.effectOffgrid) : '—'}
          />
        </div>
      )}

      <div className="eco-overview">
        <CoverageDonutPanel
          segments={coverage}
          unit="кВт·год"
          formatValue={(v) => kwhNumber(v)}
          formula={fx.coverage}
        />

        <section
          className="economics-card eco-overview-panel eco-overview-chart"
          aria-label="Погодинний економічний ефект"
        >
          <div className="eco-overview-head">
            <h3 className="eco-overview-title">
              Погодинний економічний ефект
              <FormulaTip label="Погодинний економічний ефект" formula={fx.hourly} />
            </h3>
            <span className="economics-month-muted">{money.unit}/год</span>
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

        <UzeWorkPanel cards={uzeCards} />
      </div>
    </div>
  )
}
