import { useId, type ReactNode } from 'react'
import {
  FUEL_CONSUMPTION_PER_HOUR_HELP,
  FUEL_CONSUMPTION_PER_KWH_HELP,
  FUEL_COST_HELP,
  FUEL_METHOD_PER_HOUR_HELP,
  FUEL_METHOD_PER_KWH_HELP,
  FUEL_PRICE_HELP,
} from '../generatorText'
import { fuelCostUah, fuelExample, fuelSet, type Tariffs } from '../tariffs'
import { useRecentAverageLoadKw } from '../useRecentLoad'
import { HelpTip } from './HelpTip'

// exampleLoadDefaultKw is the example's load when the organization has
// no recent data (handoff §4.4.1).
const exampleLoadDefaultKw = 200

const costFmt = new Intl.NumberFormat('uk-UA', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
const uahFmt = new Intl.NumberFormat('uk-UA', { maximumFractionDigits: 0 })

function FuelField({
  label,
  value,
  step,
  max,
  suffix,
  help,
  onChange,
}: {
  label: string
  value: number
  step: number
  max: number
  suffix: string
  help: ReactNode
  onChange: (next: number) => void
}) {
  const id = useId()
  return (
    <label className="economics-field" htmlFor={id}>
      <span>
        {label}
        <HelpTip label={label}>{help}</HelpTip>
      </span>
      <span className="economics-field-input">
        <input
          id={id}
          type="number"
          step={step}
          min={0}
          max={max}
          value={value}
          onChange={(e) => {
            const next = Number(e.target.value)
            if (Number.isFinite(next)) onChange(next)
          }}
        />
        <small className="economics-field-suffix">{suffix}</small>
      </span>
    </label>
  )
}

// GeneratorFuelFields is the «Генератор» block of the tariff form: the
// litre price, the accounting method with the one consumption it uses,
// the resulting cost and an example hour without the grid.
export function GeneratorFuelFields({
  tariffs,
  onChange,
  organizationID,
  date,
}: {
  tariffs: Tariffs
  onChange: (patch: Partial<Tariffs>) => void
  organizationID: string
  date: string
}) {
  const headingId = useId()
  const perHour = tariffs.genFuelMethod === 'per_hour'
  const set = fuelSet(tariffs)
  const cost = fuelCostUah(tariffs)
  const loadKw = useRecentAverageLoadKw(organizationID, date) ?? exampleLoadDefaultKw
  const example = fuelExample(tariffs, loadKw)

  return (
    <section className="economics-tariffs-gen" aria-labelledby={headingId}>
      <h3 id={headingId}>Генератор</h3>
      <div className="economics-tariffs-grid">
        <FuelField
          label="Ціна пального"
          value={tariffs.genFuelPriceUahPerL}
          step={0.01}
          max={1000}
          suffix="грн/л без ПДВ"
          help={FUEL_PRICE_HELP}
          onChange={(genFuelPriceUahPerL) => onChange({ genFuelPriceUahPerL })}
        />
        <div className="economics-field">
          <span>
            Спосіб обліку
            <HelpTip label="Спосіб обліку">
              <p>
                <b>За кВт·год.</b> {FUEL_METHOD_PER_KWH_HELP}
              </p>
              <p>
                <b>За годину роботи.</b> {FUEL_METHOD_PER_HOUR_HELP}
              </p>
            </HelpTip>
          </span>
          <span className="economics-margin-mode" role="group" aria-label="Спосіб обліку пального">
            <button
              type="button"
              className={!perHour ? 'active' : ''}
              aria-pressed={!perHour}
              onClick={() => onChange({ genFuelMethod: 'per_kwh' })}
            >
              за кВт·год
            </button>
            <button
              type="button"
              className={perHour ? 'active' : ''}
              aria-pressed={perHour}
              onClick={() => onChange({ genFuelMethod: 'per_hour' })}
            >
              за годину роботи
            </button>
          </span>
        </div>
        {perHour ? (
          <FuelField
            label="Витрата"
            value={tariffs.genFuelConsumptionLPerH}
            step={1}
            max={500}
            suffix="л/год"
            help={FUEL_CONSUMPTION_PER_HOUR_HELP}
            onChange={(genFuelConsumptionLPerH) => onChange({ genFuelConsumptionLPerH })}
          />
        ) : (
          <FuelField
            label="Витрата"
            value={tariffs.genFuelConsumptionLPerKwh}
            step={0.01}
            max={2}
            suffix="л/кВт·год"
            help={FUEL_CONSUMPTION_PER_KWH_HELP}
            onChange={(genFuelConsumptionLPerKwh) => onChange({ genFuelConsumptionLPerKwh })}
          />
        )}
        <div className="economics-field">
          <span>
            Обчислена вартість
            <HelpTip label="Обчислена вартість">{FUEL_COST_HELP}</HelpTip>
          </span>
          <span className={set ? 'economics-tariffs-gen-cost' : 'economics-tariffs-gen-cost is-unset'}>
            {set ? `${costFmt.format(cost)} грн/${perHour ? 'год' : 'кВт·год'}` : 'не задана'}
          </span>
        </div>
      </div>
      {set ? (
        <p className="economics-tariffs-gen-example">
          Приклад: година без мережі, навантаження {uahFmt.format(loadKw)} кВт.
          <br />
          Лише генератор — {uahFmt.format(example.generatorOnly)} грн.
          <br />
          Пів години від УЗЕ, пів години від генератора — {uahFmt.format(example.halfGenerator)} грн, ефект УЗЕ{' '}
          {uahFmt.format(example.essEffect)} грн.
        </p>
      ) : (
        <p className="economics-tariffs-gen-example">
          Поки ціну й витрату не задано, години з генератором рахуються без пального, а доба має попередження
          «вартість пального не задана».
        </p>
      )}
    </section>
  )
}
