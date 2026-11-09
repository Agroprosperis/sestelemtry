import { useEffect, useId, useState } from 'react'
import { formatOrganizationLabel } from '../../dashboard/config'
import { DEFAULT_TARIFFS, type Tariffs } from '../tariffs'
import { useOrgTariffs, type OrgTariffsStatus } from '../useOrgTariffs'
import { TariffScheduleEditor } from './TariffScheduleEditor'

type Props = {
  organizationID: string
  date: string
  tariffs: Tariffs
  onTariffsChange: (next: Tariffs) => void
  tariffsStatus: OrgTariffsStatus
  tariffsError: string | null
  readOnly?: boolean
  onClose: () => void
  // members switches the form between the sites of an economic group,
  // organizationID being the one shown.
  members?: readonly string[]
  onMemberChange?: (next: string) => void
}

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

// EconomicsTariffsModal is the tariff form and the dated schedule,
// opened from the «Сервіс» menu instead of the page header.
export function EconomicsTariffsModal({
  organizationID,
  date,
  tariffs,
  onTariffsChange,
  tariffsStatus,
  tariffsError,
  readOnly = false,
  onClose,
  members,
  onMemberChange,
}: Props) {
  const update = (patch: Partial<Tariffs>) => onTariffsChange({ ...tariffs, ...patch })
  const statusText = statusLabel(tariffsStatus)
  const statusTitle = tariffsStatus === 'error' && tariffsError ? tariffsError : undefined

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  return (
    <div className="economics-modal-overlay" role="presentation" onClick={onClose}>
      <div
        className="economics-modal economics-modal-wide"
        role="dialog"
        aria-modal="true"
        aria-label="Параметри тарифів"
        onClick={(e) => e.stopPropagation()}
      >
        <header className="economics-modal-head">
          <h2>
            Параметри тарифів
            {statusText && (
              <span
                className={`economics-tariffs-status economics-tariffs-status-${tariffsStatus}`}
                title={statusTitle}
                role={tariffsStatus === 'error' ? 'alert' : 'status'}
              >
                {statusText}
              </span>
            )}
          </h2>
          <button type="button" className="economics-modal-close" onClick={onClose} aria-label="Закрити">
            ×
          </button>
        </header>

        {members && onMemberChange && (
          <div className="economics-tariffs-members">
            <div className="economics-range-switch" role="group" aria-label="Об'єкт тарифів">
              {members.map((id) => (
                <button
                  key={id}
                  type="button"
                  className={id === organizationID ? 'active' : ''}
                  aria-pressed={id === organizationID}
                  onClick={() => onMemberChange(id)}
                >
                  {formatOrganizationLabel(id)}
                </button>
              ))}
            </div>
            <p>
              Тарифи вводяться для кожного об'єкта окремо. Елеватор разом бере ціни першого, а ємність і
              потужність УЗЕ та CAPEX додає.
            </p>
          </div>
        )}

        <fieldset className="economics-tariffs-body" disabled={readOnly}>
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
            {!readOnly && (
              <button
                type="button"
                className="economics-tariffs-reset"
                onClick={() => onTariffsChange(DEFAULT_TARIFFS)}
                title="Скинути тарифи до дефолтних значень"
              >
                Скинути
              </button>
            )}
          </div>
          <TariffScheduleEditor
            key={organizationID}
            organizationID={organizationID}
            tariffs={tariffs}
            defaultEffectiveFrom={date}
            onLoadVersion={onTariffsChange}
            readOnly={readOnly}
          />
        </fieldset>
      </div>
    </div>
  )
}

// GroupTariffsModal edits an economic group's tariffs where they live: on
// one member at a time. The group's own tariffs are only ever its members'
// combined, and the API refuses to write them.
export function GroupTariffsModal({
  members,
  date,
  readOnly = false,
  onClose,
}: {
  members: readonly string[]
  date: string
  readOnly?: boolean
  onClose: () => void
}) {
  const [member, setMember] = useState(members[0])
  const { tariffs, status, error, setTariffs } = useOrgTariffs(member)
  return (
    <EconomicsTariffsModal
      organizationID={member}
      date={date}
      tariffs={tariffs}
      onTariffsChange={setTariffs}
      tariffsStatus={status}
      tariffsError={error}
      readOnly={readOnly}
      onClose={onClose}
      members={members}
      onMemberChange={setMember}
    />
  )
}
