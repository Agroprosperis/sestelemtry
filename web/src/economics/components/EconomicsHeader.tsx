import { useId } from 'react'
import { PeriodPicker } from '../../dashboard/components/PeriodPicker'
import { EconomicsPeriodPicker } from './EconomicsPeriodPicker'
import { DEFAULT_TARIFFS, type Tariffs } from '../tariffs'
import type { OrgTariffsStatus } from '../useOrgTariffs'
import { TariffScheduleEditor } from './TariffScheduleEditor'

// parseDateString and formatDateString translate between the
// "YYYY-MM-DD" string the economics page already keeps in state
// (and on the URL) and the Date object PeriodPicker expects. We
// construct local-time Dates so the picker's day index matches the
// operator's wall clock — using `new Date('YYYY-MM-DD')` instead
// would parse as UTC midnight and shift back a day in negative
// offsets relative to UTC.
function parseDateString(value: string): Date {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value)
  if (!m) return new Date()
  return new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]))
}

function formatDateString(d: Date): string {
  const y = d.getFullYear()
  const mo = String(d.getMonth() + 1).padStart(2, '0')
  const da = String(d.getDate()).padStart(2, '0')
  return `${y}-${mo}-${da}`
}

// EconomicsRange is the period granularity toggle: a single day, a whole
// calendar month, a calendar year, the all-time payback page, or the
// all-objects portfolio rollup.
export type EconomicsRange = 'day' | 'month' | 'year' | 'payback' | 'portfolio'

type Props = {
  organizationID: string
  range: EconomicsRange
  onRangeChange: (next: EconomicsRange) => void
  // Portfolio granularity (month / year / custom window). Only meaningful
  // when range is 'portfolio'; it drives whether the period picker is a
  // month picker, a year picker, or the sliding-window picker so it tracks
  // the portfolio's own Місяць/Рік/Період toggle.
  portfolioScope?: 'month' | 'year' | 'period'
  // Sliding-period window (year view only), both YYYY-MM. onWindowChange
  // with empty strings resets to the calendar year of the anchor.
  windowFrom: string
  windowTo: string
  onWindowChange: (from: string, to: string) => void
  date: string
  onDateChange: (next: string) => void
  tariffs: Tariffs
  onTariffsChange: (next: Tariffs) => void
  tariffsStatus: OrgTariffsStatus
  tariffsError: string | null
}

// statusLabel maps the hook's coarse state machine to the inline
// indicator next to "Параметри тарифів". We keep the strings short
// because the indicator sits inline with the <summary> chevron — a
// long label there would break the click target on narrow screens.
function statusLabel(status: OrgTariffsStatus): string {
  switch (status) {
    case 'loading':
      return 'Завантаження…'
    case 'saving':
      return 'Зберігаємо…'
    case 'saved':
      return 'Збережено'
    case 'error':
      return 'Помилка збереження'
    default:
      return ''
  }
}

// numericInput is a small wrapper around a labelled <input type=number>
// that stays controlled even when the user temporarily types an empty
// string mid-edit. We commit on every keystroke that parses to a
// finite number so the KPIs reflow live; non-numeric input keeps the
// previous value so the caller never sees NaN.
function NumericField({
  label,
  value,
  step,
  min,
  max,
  onChange,
  suffix,
  hint,
}: {
  label: string
  value: number
  step: number
  min?: number
  max?: number
  onChange: (next: number) => void
  suffix?: string
  hint?: string
}) {
  const id = useId()
  return (
    <label className="economics-field" htmlFor={id}>
      <span>
        {label}
        {hint && (
          <span className="economics-info" data-tip={hint} role="img" aria-label={hint}>
            i
          </span>
        )}
      </span>
      <span className="economics-field-input">
        <input
          id={id}
          type="number"
          step={step}
          min={min}
          max={max}
          value={value}
          onChange={(e) => {
            const next = Number(e.target.value)
            if (Number.isFinite(next)) onChange(next)
          }}
        />
        {suffix && <small className="economics-field-suffix">{suffix}</small>}
      </span>
    </label>
  )
}

// SupplierMarginField is the supplier-margin input with a грн/кВт·год ⇄ %
// mode toggle. In 'abs' mode it edits the flat UAH/kWh adder; in 'pct'
// mode it edits a percent of the RDN price. Both values are kept in the
// tariffs so switching modes never loses the other entry. Either may be
// negative (a supplier discount).
function SupplierMarginField({
  mode,
  abs,
  pct,
  onChange,
}: {
  mode: 'abs' | 'pct'
  abs: number
  pct: number
  onChange: (patch: Partial<Tariffs>) => void
}) {
  const id = useId()
  const isPct = mode === 'pct'
  const value = isPct ? pct : abs
  const hint = isPct
    ? 'Маржа постачальника як % від ціни РДН (застосовується щогодини). Відʼємне значення = знижка постачальника, що зменшує ціну імпорту.'
    : 'Надбавка постачальника до ціни імпорту (грн/кВт·год). Може бути відʼємною, якщо постачальник дає знижку.'
  return (
    <label className="economics-field" htmlFor={id}>
      <span>
        Маржа постачальника
        <span className="economics-info" data-tip={hint} role="img" aria-label={hint}>
          i
        </span>
      </span>
      <span className="economics-field-input">
        <input
          id={id}
          type="number"
          step={isPct ? 0.1 : 0.0001}
          value={value}
          onChange={(e) => {
            const next = Number(e.target.value)
            if (!Number.isFinite(next)) return
            onChange(isPct ? { supplierMarginPct: next } : { supplierMarginUahPerKwh: next })
          }}
        />
        <span className="economics-margin-mode" role="group" aria-label="Одиниця маржі постачальника">
          <button
            type="button"
            className={!isPct ? 'active' : ''}
            aria-pressed={!isPct}
            onClick={() => onChange({ supplierMarginMode: 'abs' })}
          >
            грн
          </button>
          <button
            type="button"
            className={isPct ? 'active' : ''}
            aria-pressed={isPct}
            onClick={() => onChange({ supplierMarginMode: 'pct' })}
          >
            %
          </button>
        </span>
      </span>
    </label>
  )
}

export function EconomicsHeader({
  organizationID,
  range,
  onRangeChange,
  portfolioScope = 'month',
  windowFrom,
  windowTo,
  onWindowChange,
  date,
  onDateChange,
  tariffs,
  onTariffsChange,
  tariffsStatus,
  tariffsError,
}: Props) {
  const update = (patch: Partial<Tariffs>) => onTariffsChange({ ...tariffs, ...patch })
  const statusText = statusLabel(tariffsStatus)
  const statusTitle = tariffsStatus === 'error' && tariffsError ? tariffsError : undefined
  return (
    <header className="economics-header">
      <div className="economics-header-row">
        <div className="economics-range-switch" role="group" aria-label="Гранулярність періоду">
          <button
            type="button"
            className={range === 'day' ? 'active' : ''}
            aria-pressed={range === 'day'}
            onClick={() => onRangeChange('day')}
          >
            День
          </button>
          <button
            type="button"
            className={range === 'month' ? 'active' : ''}
            aria-pressed={range === 'month'}
            onClick={() => onRangeChange('month')}
          >
            Місяць
          </button>
          <button
            type="button"
            className={range === 'year' ? 'active' : ''}
            aria-pressed={range === 'year'}
            onClick={() => onRangeChange('year')}
          >
            Рік
          </button>
          <button
            type="button"
            className={range === 'payback' ? 'active' : ''}
            aria-pressed={range === 'payback'}
            onClick={() => onRangeChange('payback')}
          >
            Окупність
          </button>
          <button
            type="button"
            className={range === 'portfolio' ? 'active' : ''}
            aria-pressed={range === 'portfolio'}
            onClick={() => onRangeChange('portfolio')}
          >
            Портфель
          </button>
        </div>
        {range === 'payback' ? null : range === 'year' ||
          (range === 'portfolio' && portfolioScope === 'period') ? (
          <EconomicsPeriodPicker from={windowFrom} to={windowTo} onChange={onWindowChange} />
        ) : (
          <PeriodPicker
            preset={range === 'portfolio' ? (portfolioScope === 'year' ? 'year' : 'month') : range}
            anchor={parseDateString(date)}
            onChange={(next) => onDateChange(formatDateString(next))}
          />
        )}
      </div>

      {/* Tariffs belong to one organization, but the portfolio compares
          several at once — editing an object's prices from that view would
          be misleading, so the panel (and the version fetch behind it)
          stays out of the tree there. */}
      {range !== 'portfolio' && (
      <details className="economics-tariffs">
        <summary>
          <span>Параметри тарифів</span>
          {statusText && (
            <span
              className={`economics-tariffs-status economics-tariffs-status-${tariffsStatus}`}
              title={statusTitle}
              role={tariffsStatus === 'error' ? 'alert' : 'status'}
            >
              {statusText}
            </span>
          )}
        </summary>
        <div className="economics-tariffs-grid">
          <NumericField
            label="Розподіл (Distribution)"
            value={tariffs.distributionUahPerKwh}
            step={0.0001}
            min={0}
            suffix="грн/кВт·год"
            onChange={(distributionUahPerKwh) => update({ distributionUahPerKwh })}
          />
          <NumericField
            label="Передача (Transmission)"
            value={tariffs.transmissionUahPerKwh}
            step={0.0001}
            min={0}
            suffix="грн/кВт·год"
            onChange={(transmissionUahPerKwh) => update({ transmissionUahPerKwh })}
          />
          <SupplierMarginField
            mode={tariffs.supplierMarginMode}
            abs={tariffs.supplierMarginUahPerKwh}
            pct={tariffs.supplierMarginPct}
            onChange={update}
          />
          <NumericField
            label="Інші збори"
            value={tariffs.otherFeesUahPerKwh}
            step={0.0001}
            min={0}
            suffix="грн/кВт·год"
            onChange={(otherFeesUahPerKwh) => update({ otherFeesUahPerKwh })}
          />
          <NumericField
            label="Знижка експорту"
            value={tariffs.exportDiscount}
            step={0.01}
            min={0}
            max={1}
            suffix="(0..1)"
            onChange={(exportDiscount) => update({ exportDiscount })}
          />
          <NumericField
            label="Деградація УЗЕ"
            value={tariffs.degradationUahPerKwh}
            step={0.01}
            min={0}
            suffix="грн/кВт·год"
            onChange={(degradationUahPerKwh) => update({ degradationUahPerKwh })}
          />
          <NumericField
            label="Ставка ПДВ"
            value={tariffs.vatRate}
            step={0.01}
            min={0}
            max={1}
            suffix="(0..1)"
            onChange={(vatRate) => update({ vatRate })}
          />
          <NumericField
            label="Ємність УЗЕ"
            value={tariffs.essCapacityKwh}
            step={1}
            min={1}
            suffix="кВт·год"
            hint="Корисна ємність УЗЕ — енергоємність робочого вікна SOC 10–90%. Для пакету 645 кВт·год вводьте 516. SOC 10% = порожньо, 90% = повна корисна. Використовується для залишку УЗЕ і еквівалентних циклів."
            onChange={(essCapacityKwh) => update({ essCapacityKwh })}
          />
          <NumericField
            label="Потужність УЗЕ"
            value={tariffs.essPowerLimitKw}
            step={1}
            min={0}
            suffix="кВт"
            hint="Номінальна потужність заряду/розряду УЗЕ (кВт) для фільтра аномалій телеметрії. 0 = автоматичний запас ≈ 1C від ємності."
            onChange={(essPowerLimitKw) => update({ essPowerLimitKw })}
          />
          <NumericField
            label="ККД циклу УЗЕ"
            value={tariffs.roundtripEfficiency}
            step={0.01}
            min={0}
            max={1}
            suffix="(0..1)"
            hint="Round-trip ККД батареї (0..1). 0 = оцінити емпірично з фактичного обороту місяця. Типово 0.85–0.92."
            onChange={(roundtripEfficiency) => update({ roundtripEfficiency })}
          />
          <NumericField
            label="CAPEX проєкту"
            value={tariffs.capexUah}
            step={1000}
            min={0}
            suffix="грн"
            hint="Разові капітальні інвестиції в проєкт (СЕС+УЗЕ), грн. Використовується лише для розрахунку окупності та ROI у річному дашборді. 0 = не показувати панель окупності."
            onChange={(capexUah) => update({ capexUah })}
          />
          <NumericField
            label="Бізнес-план окупності"
            value={tariffs.plannedPaybackMonths}
            step={1}
            min={0}
            suffix="міс."
            hint="Плановий термін окупності проєкту з бізнес-плану (місяців від початку експлуатації). Сторінка «Окупність» порівнює фактичний прогноз із цим планом. 0 = не задано."
            onChange={(plannedPaybackMonths) => update({ plannedPaybackMonths })}
          />
          <label className="economics-field economics-field-checkbox">
            <input
              type="checkbox"
              checked={tariffs.includeVat}
              onChange={(e) => update({ includeVat: e.target.checked })}
            />
            <span>Враховувати ПДВ у цінах</span>
          </label>
          <button
            type="button"
            className="economics-tariffs-reset"
            onClick={() => onTariffsChange(DEFAULT_TARIFFS)}
            title="Скинути тарифи до дефолтних значень"
          >
            Скинути
          </button>
        </div>
        <TariffScheduleEditor
          organizationID={organizationID}
          tariffs={tariffs}
          defaultEffectiveFrom={date}
          onLoadVersion={onTariffsChange}
        />
      </details>
      )}
    </header>
  )
}
