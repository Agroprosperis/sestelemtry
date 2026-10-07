import type { ReactElement } from 'react'
import type { SeriesToggles } from './chartSvg'
import { flowMeta, fmt, hasValue, priceFmt, splitBessFlow, timeLabel, type ChartPoint, type Clock, type PlanOverlay } from './model'

type InspectorProps = {
  clock: Clock
  i: number
  point: ChartPoint
  pv: number | null
  load: number | null
  price: number | null
  buy: number | null
  sell: number | null
  plan: PlanOverlay | null
  state: { label: string; detail: string }
  series: SeriesToggles
}

// Inspector — exact values of the hovered / clicked hour (renderInspector).
export function Inspector({ clock, i, point: d, pv, load, price, buy, sell, plan, state, series }: InspectorProps): ReactElement {
  const knownPower = hasValue(d.p)
  const knownGrid = hasValue(d.import) && hasValue(d.export)
  const knownSoc = hasValue(d.before) && hasValue(d.soc)
  const flows = knownPower && hasValue(load) && hasValue(pv) ? splitBessFlow(d.p as number, pv, load) : []
  const gridCharge = flows.find((f) => f.key === 'grid')
  const gridChargeKw = gridCharge ? Math.abs(gridCharge.power) : 0
  return (
    <div className="d-inspect" aria-label="Точні значення вибраної години">
      <strong className="d-inspect-time">
        {timeLabel(clock, i)}–{timeLabel(clock, i + 1)} · {state.label}
      </strong>
      {i >= 0 && <span className="d-flow-detail">{state.detail}</span>}
      {series.pv && (
        <span>
          {i < 0 ? 'СЕС' : 'СЕС (прогноз)'} <strong>{fmt(pv)} кВт</strong>
        </span>
      )}
      {series.load && (
        <span>
          Споживання <strong>{hasValue(load) ? fmt(load) + ' кВт' : 'Не задано'}</strong>
        </span>
      )}
      {series.price && (
        <span>
          РДН <strong>{hasValue(price) ? fmt(price) + ' грн/кВт·год' : 'не опублікована'}</strong>
        </span>
      )}
      {series.bess && (
        <span>
          УЗЕ{knownPower ? ' · ' + ((d.p as number) < 0 ? 'заряд' : (d.p as number) > 0 ? 'розряд' : 'утримання') : ''}{' '}
          <strong>{knownPower ? fmt(Math.abs(d.p as number)) + ' кВт' : 'Немає прогнозу'}</strong>
        </span>
      )}
      <span>
        {knownGrid ? ((d.export as number) > 0 ? 'Експорт PCC' : 'Імпорт PCC') : 'Мережа PCC'}{' '}
        <strong>{knownGrid ? fmt((d.import as number) || (d.export as number)) + ' кВт' : 'Немає прогнозу'}</strong>
      </span>
      {series.soc && (
        <span>
          SOC <strong>{knownSoc ? fmt(d.before) + ' → ' + fmt(d.soc) + '%' : hasValue(d.soc) ? fmt(d.soc) + '%' : 'Немає прогнозу'}</strong>
        </span>
      )}
      {series.bess && knownPower && hasValue(load) && (
        <span className="d-flow-detail">
          {flows.length
            ? ((d.p as number) < 0 ? 'Заряд УЗЕ' : 'Розряд УЗЕ') +
              ' · ' +
              flows.map((part) => flowMeta[part.key].detail + ' ' + fmt(Math.abs(part.power)) + ' кВт').join(' · ')
            : 'УЗЕ · утримання, 0 кВт'}
        </span>
      )}
      {knownGrid && (d.import as number) > 0.05 && (
        <span className="d-flow-detail">
          Імпорт із мережі · на споживання <strong>{fmt(Math.max(0, (d.import as number) - gridChargeKw))} кВт</strong> · на заряд УЗЕ{' '}
          <strong>{fmt(gridChargeKw)} кВт</strong>
        </span>
      )}
      {hasValue(d.curtailed) && d.curtailed > 0.05 && (
        <span className="d-flow-detail">
          Обмеження СЕС <strong>{fmt(d.curtailed)} кВт</strong> · надлишок не приймають батарея або мережа
        </span>
      )}
      {plan && (
        <span className="d-flow-detail">
          План на цю годину · УЗЕ <strong>{fmt(plan.ess)} кВт</strong> · SOC <strong>{fmt(plan.soc)}%</strong> · мережа{' '}
          <strong>{fmt(plan.grid)} кВт</strong>
          {plan.command && (
            <>
              {' '}
              · команда <strong>{plan.command}</strong>
            </>
          )}
        </span>
      )}
      {series.price && i >= 0 && hasValue(buy) && hasValue(sell) && (
        <span className="d-flow-detail">
          Тарифи AUTO · купівля <strong>{priceFmt(buy)}</strong> · продаж <strong>{priceFmt(sell)}</strong> грн/кВт·год
        </span>
      )}
    </div>
  )
}
