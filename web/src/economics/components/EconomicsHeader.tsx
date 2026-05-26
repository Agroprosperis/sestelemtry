import { useId } from 'react'
import { OrganizationSelect } from '../../dashboard/components/OrganizationSelect'
import { PeriodPicker } from '../../dashboard/components/PeriodPicker'
import { formatOrganizationLabel } from '../../dashboard/config'
import { DEFAULT_TARIFFS, type Tariffs } from '../tariffs'
import type { OrgTariffsStatus } from '../useOrgTariffs'

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

type Props = {
  organizationID: string
  organizationOptions: string[]
  onOrganizationChange: (next: string) => void
  date: string
  onDateChange: (next: string) => void
  tariffs: Tariffs
  onTariffsChange: (next: Tariffs) => void
  tariffsStatus: OrgTariffsStatus
  tariffsError: string | null
  // Render a "back to main dashboard" link that drops `?view=economics`
  // and pushes a new history entry. Kept as a callback so the page
  // file can decide whether to use pushState or hard navigate.
  onBackToDashboard: () => void
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
}: {
  label: string
  value: number
  step: number
  min?: number
  max?: number
  onChange: (next: number) => void
  suffix?: string
}) {
  const id = useId()
  return (
    <label className="economics-field" htmlFor={id}>
      <span>{label}</span>
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

export function EconomicsHeader({
  organizationID,
  organizationOptions,
  onOrganizationChange,
  date,
  onDateChange,
  tariffs,
  onTariffsChange,
  tariffsStatus,
  tariffsError,
  onBackToDashboard,
}: Props) {
  const update = (patch: Partial<Tariffs>) => onTariffsChange({ ...tariffs, ...patch })
  const statusText = statusLabel(tariffsStatus)
  const statusTitle = tariffsStatus === 'error' && tariffsError ? tariffsError : undefined
  return (
    <header className="economics-header">
      <div className="economics-header-row">
        <div className="economics-header-brand">
          <img
            src="/logo_agroprosperis.png"
            alt="Агропросперіс"
            className="economics-header-logo"
          />
          <div className="economics-header-titles">
            <h1>Добова економіка (СЕС + УЗЕ)</h1>
            <p>
              {formatOrganizationLabel(organizationID)} · розрахунок ефекту за обраний день на базі цін РДН і тарифів.
            </p>
          </div>
        </div>
        <div className="economics-header-controls">
          <OrganizationSelect
            value={organizationID}
            options={organizationOptions}
            onChange={onOrganizationChange}
          />
          <PeriodPicker
            preset="day"
            anchor={parseDateString(date)}
            onChange={(next) => onDateChange(formatDateString(next))}
          />
          <button
            type="button"
            className="economics-back-link"
            onClick={onBackToDashboard}
            title="Повернутися до основного дашборду"
          >
            ← Дашборд
          </button>
        </div>
      </div>

      <details className="economics-tariffs" open>
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
          <NumericField
            label="Маржа постачальника"
            value={tariffs.supplierMarginUahPerKwh}
            step={0.0001}
            suffix="грн/кВт·год"
            onChange={(supplierMarginUahPerKwh) => update({ supplierMarginUahPerKwh })}
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
            onChange={(essCapacityKwh) => update({ essCapacityKwh })}
          />
          <NumericField
            label="Стартова собівартість УЗЕ на 00:00"
            value={tariffs.seedEssCostUahPerKwh}
            step={0.01}
            min={0}
            suffix="грн/кВт·год"
            onChange={(seedEssCostUahPerKwh) => update({ seedEssCostUahPerKwh })}
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
      </details>
    </header>
  )
}
