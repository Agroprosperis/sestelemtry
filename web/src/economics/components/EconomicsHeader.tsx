import { useId } from 'react'
import { OrganizationSelect } from '../../dashboard/components/OrganizationSelect'
import { PeriodPicker } from '../../dashboard/components/PeriodPicker'
import { formatOrganizationLabel } from '../../dashboard/config'
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

// DamRefreshState mirrors the parent page's small state machine for
// the "Оновити ціни РДН" button (see EconomicsPage.tsx). Kept as a
// local type so the prop signature is self-documenting without
// pulling the page in as a dependency cycle.
export type DamRefreshState = 'idle' | 'loading' | 'error'

// EconomicsRange is the period granularity toggle: a single day, a whole
// calendar month, a calendar year, or the all-objects portfolio rollup.
export type EconomicsRange = 'day' | 'month' | 'year' | 'portfolio'

// rangeTitle / rangeSubtitle pick the header copy for the active period
// granularity so the day/month/year/portfolio views read consistently.
function rangeTitle(range: EconomicsRange): string {
  switch (range) {
    case 'portfolio':
      return 'Зведений дашборд СЕС + УЗЕ'
    case 'year':
      return 'Річний дашборд СЕС + УЗЕ'
    case 'month':
      return 'Місячний дашборд СЕС + УЗЕ'
    default:
      return 'Добовий дашборд СЕС + УЗЕ'
  }
}

function rangeSubtitle(range: EconomicsRange, monthsWithData?: number): string {
  switch (range) {
    case 'portfolio':
      return 'порівняння резерву по всіх об\'єктах на базі цін РДН і тарифів.'
    case 'year':
      return monthsWithData && monthsWithData > 0
        ? `управлінський звіт за рік · ${monthsWithData} міс. телеметрії`
        : 'управлінський звіт за рік на базі цін РДН і тарифів.'
    case 'month':
      return 'управлінський звіт за місяць на базі цін РДН і тарифів.'
    default:
      return 'розрахунок ефекту за обраний день на базі цін РДН і тарифів.'
  }
}

type Props = {
  organizationID: string
  organizationOptions: string[]
  onOrganizationChange: (next: string) => void
  range: EconomicsRange
  onRangeChange: (next: EconomicsRange) => void
  // Portfolio granularity (month / year / custom window). Only meaningful
  // when range is 'portfolio'; it drives whether the period picker is a
  // month picker, a year picker, or the sliding-window picker so it tracks
  // the portfolio's own Місяць/Рік/Період toggle.
  portfolioScope?: 'month' | 'year' | 'period'
  // Months of telemetry in the active year (year view only), surfaced
  // in the subtitle per SPEC §3.1.
  monthsWithData?: number
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
  // Render a "back to main dashboard" link that drops `?view=economics`
  // and pushes a new history entry. Kept as a callback so the page
  // file can decide whether to use pushState or hard navigate.
  onBackToDashboard: () => void
  // Operator-driven "fetch DAM prices from OREE right now" hook.
  // The handler is async; the button reflects the supplied state
  // (loading / idle / error) so the page owns the lifecycle. When
  // the request fails, `damRefreshError` carries the upstream
  // message and the button shows it in its tooltip — clearer than
  // a toast that's already gone by the time the operator clicks
  // back.
  onRefreshDam: () => void | Promise<void>
  damRefreshState: DamRefreshState
  damRefreshError: string | null
  // Opens the server-side economics recompute dialog.
  onOpenRecompute: () => void
  // Opens the bulk OREE DAM-price import dialog. The single-day
  // `onRefreshDam` button stays for the common "refresh today"
  // shortcut; this hook is the date-range backfill.
  onOpenDamImport: () => void
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

// damRefreshLabel maps the small state machine to the button's
// visible text. Kept stable across renders so the operator can scan
// the button without it flickering label-to-label on every state
// transition — the icon-less form is intentional, the surrounding
// title attribute carries the detail.
function damRefreshLabel(state: DamRefreshState): string {
  switch (state) {
    case 'loading':
      return 'Оновлюємо…'
    case 'error':
      return 'Спробувати ще'
    default:
      return 'Оновити ціни РДН'
  }
}

export function EconomicsHeader({
  organizationID,
  organizationOptions,
  onOrganizationChange,
  range,
  onRangeChange,
  portfolioScope = 'month',
  monthsWithData,
  windowFrom,
  windowTo,
  onWindowChange,
  date,
  onDateChange,
  tariffs,
  onTariffsChange,
  tariffsStatus,
  tariffsError,
  onBackToDashboard,
  onRefreshDam,
  damRefreshState,
  damRefreshError,
  onOpenRecompute,
  onOpenDamImport,
}: Props) {
  const update = (patch: Partial<Tariffs>) => onTariffsChange({ ...tariffs, ...patch })
  const statusText = statusLabel(tariffsStatus)
  const statusTitle = tariffsStatus === 'error' && tariffsError ? tariffsError : undefined
  const damButtonTitle =
    damRefreshState === 'error' && damRefreshError
      ? damRefreshError
      : 'Завантажити свіжі ціни РДН з OREE для обраного дня'
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
            <h1>{rangeTitle(range)}</h1>
            <p>
              {range === 'portfolio'
                ? rangeSubtitle(range, monthsWithData)
                : `${formatOrganizationLabel(organizationID)} · ${rangeSubtitle(range, monthsWithData)}`}
            </p>
          </div>
        </div>
        <div className="economics-header-controls">
          {range !== 'portfolio' && (
            <OrganizationSelect
              value={organizationID}
              options={organizationOptions}
              onChange={onOrganizationChange}
            />
          )}
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
              className={range === 'portfolio' ? 'active' : ''}
              aria-pressed={range === 'portfolio'}
              onClick={() => onRangeChange('portfolio')}
            >
              Портфель
            </button>
          </div>
          {range === 'year' || (range === 'portfolio' && portfolioScope === 'period') ? (
            <EconomicsPeriodPicker from={windowFrom} to={windowTo} onChange={onWindowChange} />
          ) : (
            <PeriodPicker
              preset={range === 'portfolio' ? (portfolioScope === 'year' ? 'year' : 'month') : range}
              anchor={parseDateString(date)}
              onChange={(next) => onDateChange(formatDateString(next))}
            />
          )}
          <button
            type="button"
            className={
              damRefreshState === 'error'
                ? 'economics-refresh-dam economics-refresh-dam-error'
                : 'economics-refresh-dam'
            }
            onClick={() => {
              void onRefreshDam()
            }}
            disabled={damRefreshState === 'loading'}
            title={damButtonTitle}
            aria-live="polite"
          >
            {damRefreshLabel(damRefreshState)}
          </button>
          <button
            type="button"
            className="economics-recompute-btn"
            onClick={onOpenDamImport}
            title="Завантажити архів цін РДН з OREE за діапазон дат"
          >
            Імпорт цін РДН
          </button>
          <button
            type="button"
            className="economics-recompute-btn"
            onClick={onOpenRecompute}
            title="Перерахувати погодинну економіку за діапазон дат і зберегти в базі"
          >
            Перерахунок економіки
          </button>
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
          <NumericField
            label="Маржа постачальника"
            value={tariffs.supplierMarginUahPerKwh}
            step={0.0001}
            suffix="грн/кВт·год"
            hint="Надбавка постачальника до ціни імпорту (грн/кВт·год). Може бути відʼємною, якщо постачальник дає знижку — тоді ціна імпорту зменшується."
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
    </header>
  )
}
