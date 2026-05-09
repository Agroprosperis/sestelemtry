import { useEffect, useMemo, useRef, useState } from 'react'
import { downloadCsv, rowsToCsv } from '../csv'
import {
  autoBucket,
  customExportFilename,
  fetchCustomExportData,
  type CustomExportBucket,
  type CustomExportColumns,
} from '../customExport'
import { elevatorCodeFor } from '../transforms/pvForecast'

type Props = {
  organizationID: string
  initialAnchor: Date
  onClose: () => void
}

const BUCKET_OPTIONS: Array<{ value: CustomExportBucket | 'auto'; label: string }> = [
  { value: 'auto', label: 'Авто' },
  { value: '5 minutes', label: '5 хвилин' },
  { value: '1 hour', label: '1 година' },
  { value: '1 day', label: '1 день' },
  { value: '1 month', label: '1 місяць' },
]

const COLUMN_OPTIONS: Array<{ id: keyof CustomExportColumns; label: string; hint?: string }> = [
  {
    id: 'energy',
    label: 'Енергія (kWh)',
    hint: 'PV / заряд / розряд / купівля / продаж / споживання',
  },
  { id: 'price', label: 'Ціна РДН (грн/МВт·год)' },
  { id: 'soc', label: 'Рівень заряду УЗЕ (SOC %)' },
  {
    id: 'power',
    label: 'Миттєва потужність (kW)',
    hint: 'PV / УЗЕ / мережа / навантаження. Беремо last-sample у бакеті',
  },
  {
    id: 'forecast',
    label: 'Прогноз СЕС (kW)',
    hint: 'Лише для одноденного 5-хв експорту pe / ze',
  },
]

function pad(n: number): string {
  return n < 10 ? `0${n}` : String(n)
}

function toDateInputValue(d: Date): string {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

function parseDateInput(value: string): Date | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value)
  if (!m) return null
  const d = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]))
  return Number.isFinite(d.getTime()) ? d : null
}

function defaultFrom(anchor: Date): Date {
  const d = new Date(anchor)
  d.setHours(0, 0, 0, 0)
  d.setDate(d.getDate() - 6)
  return d
}

function defaultTo(anchor: Date): Date {
  const d = new Date(anchor)
  d.setHours(0, 0, 0, 0)
  return d
}

// ExportDialog renders the "Експорт даних" modal triggered from the
// dashboard header. It owns the form state (range, bucket, column set),
// runs the fetch when the user submits, and downloads the resulting CSV.
// The component mounts only while the dialog is open (the parent guards
// rendering with `{exportOpen && ...}`) so each open starts with fresh
// defaults — no reset effect, no setState-in-effect lint surprises.
export function ExportDialog({ organizationID, initialAnchor, onClose }: Props) {
  const dialogRef = useRef<HTMLDialogElement>(null)
  const [fromStr, setFromStr] = useState<string>(() =>
    toDateInputValue(defaultFrom(initialAnchor)),
  )
  const [toStr, setToStr] = useState<string>(() =>
    toDateInputValue(defaultTo(initialAnchor)),
  )
  const [bucketChoice, setBucketChoice] = useState<CustomExportBucket | 'auto'>('auto')
  const [columns, setColumns] = useState<CustomExportColumns>({
    energy: true,
    price: true,
    soc: false,
    power: false,
    forecast: false,
  })
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const dialog = dialogRef.current
    if (!dialog || dialog.open) return
    dialog.showModal()
  }, [])

  const fromDate = useMemo(() => parseDateInput(fromStr), [fromStr])
  // toDate is exclusive in the API (next-day-after end). The picker shows
  // an inclusive day, so the actual `to` is end-day + 1.
  const toExclusive = useMemo(() => {
    const d = parseDateInput(toStr)
    if (!d) return null
    const e = new Date(d)
    e.setDate(e.getDate() + 1)
    return e
  }, [toStr])

  const computedBucket: CustomExportBucket | null = useMemo(() => {
    if (!fromDate || !toExclusive) return null
    return bucketChoice === 'auto' ? autoBucket(fromDate, toExclusive) : bucketChoice
  }, [bucketChoice, fromDate, toExclusive])

  const hasElevator = elevatorCodeFor(organizationID) !== null
  const anyColumn = Object.values(columns).some(Boolean)
  const validRange = !!fromDate && !!toExclusive && fromDate.getTime() < toExclusive.getTime()
  const todayMax = useMemo(() => toDateInputValue(new Date()), [])
  // Forecast only makes sense for a single-day, 5-minute export of an
  // organization that has an elevator code mapping (pe / ze).
  const isSingleDay =
    !!fromDate &&
    !!toExclusive &&
    toExclusive.getTime() - fromDate.getTime() === 24 * 60 * 60 * 1000
  const forecastEnabled = computedBucket === '5 minutes' && isSingleDay && hasElevator

  function toggleColumn(id: keyof CustomExportColumns) {
    setColumns((prev) => ({ ...prev, [id]: !prev[id] }))
  }

  async function handleDownload() {
    if (!fromDate || !toExclusive || !computedBucket) return
    setBusy(true)
    setError(null)
    try {
      const table = await fetchCustomExportData({
        organizationID,
        from: fromDate,
        to: toExclusive,
        bucket: computedBucket,
        columns: {
          ...columns,
          forecast: columns.forecast && forecastEnabled,
        },
      })
      if (table.rows.length === 0) {
        setError('У вибраному діапазоні немає даних — спробуйте інший період або колонки.')
        return
      }
      const filename = customExportFilename({
        organizationID,
        from: fromDate,
        to: toExclusive,
        bucket: computedBucket,
      })
      downloadCsv(filename, rowsToCsv(table.headers, table.rows))
      onClose()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Не вдалось підготувати експорт')
    } finally {
      setBusy(false)
    }
  }

  return (
    <dialog
      ref={dialogRef}
      className="export-dialog"
      onClose={onClose}
      onCancel={(e) => {
        // Prevent the native ESC behavior from leaving the dialog in an
        // odd half-open state on some browsers; we'll close explicitly.
        e.preventDefault()
        onClose()
      }}
    >
      <form
        method="dialog"
        onSubmit={(e) => {
          e.preventDefault()
          void handleDownload()
        }}
      >
        <header className="export-dialog-head">
          <h2>Експорт даних</h2>
          <button
            type="button"
            className="export-dialog-close"
            aria-label="Закрити"
            onClick={onClose}
          >
            ×
          </button>
        </header>

        <div className="export-dialog-body">
          <div className="export-dialog-row">
            <label>
              <span>Від</span>
              <input
                type="date"
                value={fromStr}
                max={toStr || todayMax}
                onChange={(e) => setFromStr(e.target.value)}
              />
            </label>
            <label>
              <span>До</span>
              <input
                type="date"
                value={toStr}
                min={fromStr}
                max={todayMax}
                onChange={(e) => setToStr(e.target.value)}
              />
            </label>
            <label>
              <span>Розмір кошика</span>
              <select
                value={bucketChoice}
                onChange={(e) => setBucketChoice(e.target.value as CustomExportBucket | 'auto')}
              >
                {BUCKET_OPTIONS.map((opt) => (
                  <option key={opt.value} value={opt.value}>
                    {opt.label}
                    {opt.value === 'auto' && computedBucket
                      ? ` (${BUCKET_OPTIONS.find((o) => o.value === computedBucket)?.label})`
                      : ''}
                  </option>
                ))}
              </select>
            </label>
          </div>

          <fieldset className="export-dialog-columns">
            <legend>Колонки</legend>
            {COLUMN_OPTIONS.map((opt) => {
              const isForecast = opt.id === 'forecast'
              const disabled = isForecast && !forecastEnabled
              return (
                <label
                  key={opt.id}
                  className={`export-dialog-column${disabled ? ' export-dialog-column--disabled' : ''}`}
                >
                  <input
                    type="checkbox"
                    checked={columns[opt.id] && !disabled}
                    disabled={disabled}
                    onChange={() => toggleColumn(opt.id)}
                  />
                  <span>
                    {opt.label}
                    {opt.hint && <em className="export-dialog-hint">{opt.hint}</em>}
                  </span>
                </label>
              )
            })}
          </fieldset>

          {error && (
            <p role="alert" className="export-dialog-error">
              {error}
            </p>
          )}
        </div>

        <footer className="export-dialog-foot">
          <button type="button" className="export-dialog-secondary" onClick={onClose}>
            Скасувати
          </button>
          <button
            type="submit"
            className="export-dialog-primary"
            disabled={!validRange || !anyColumn || busy}
          >
            {busy ? 'Готуємо…' : 'Завантажити CSV'}
          </button>
        </footer>
      </form>
    </dialog>
  )
}
