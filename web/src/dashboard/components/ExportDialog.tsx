import { useEffect, useMemo, useRef, useState } from 'react'
import { fetchRawSamplesCsv, fetchRegisters } from '../../api'
import { downloadCsv, rowsToCsv } from '../csv'
import {
  autoBucket,
  customExportFilename,
  fetchCustomExportData,
  rawExportMetricKeys,
  RAW_SAMPLES_LIMIT,
  RAW_SAMPLES_MAX_DAYS,
  type CustomExportBucket,
  type CustomExportColumns,
} from '../customExport'
import { pivotRawCsvToWide } from '../pivotRaw'
import { elevatorCodeFor } from '../transforms/pvForecast'

type Props = {
  organizationID: string
  initialAnchor: Date
  onClose: () => void
}

const BUCKET_OPTIONS: Array<{ value: CustomExportBucket | 'auto'; label: string }> = [
  { value: 'auto', label: 'Авто' },
  { value: 'raw', label: 'Сирі дані (telemetry_samples)' },
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
    id: 'device',
    label: 'Час пристрою (epoch s)',
    hint: 'Локальний годинник SmartLogger — для діагностики дрейфу',
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
    device: false,
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
  const isRaw = computedBucket === 'raw'
  // Raw mode only exposes columns that map to actual `telemetry_samples`
  // metrics (energy / soc / power). The DAM price comes from the
  // market-data table and the PV forecast comes from n8n, so neither
  // has raw rows to stream.
  const rawAllowedColumns = isRaw
    ? { ...columns, price: false, forecast: false }
    : columns
  const anyColumn = Object.values(rawAllowedColumns).some(Boolean)
  const validRange = !!fromDate && !!toExclusive && fromDate.getTime() < toExclusive.getTime()
  const todayMax = useMemo(() => toDateInputValue(new Date()), [])
  // Forecast only makes sense for a single-day, 5-minute export of an
  // organization that has an elevator code mapping (pe / ze).
  const isSingleDay =
    !!fromDate &&
    !!toExclusive &&
    toExclusive.getTime() - fromDate.getTime() === 24 * 60 * 60 * 1000
  const forecastEnabled = !isRaw && computedBucket === '5 minutes' && isSingleDay && hasElevator
  const priceEnabled = !isRaw

  // Raw mode is rate-limited on the server (range <= 31 days). The
  // dropdown lets users pick `raw` regardless of range so they get an
  // explicit explanation rather than a silently disabled option, but
  // we surface the constraint as an inline error to gate submission.
  const rangeDays =
    !!fromDate && !!toExclusive
      ? (toExclusive.getTime() - fromDate.getTime()) / (24 * 60 * 60 * 1000)
      : 0
  const rawRangeOk = !isRaw || rangeDays <= RAW_SAMPLES_MAX_DAYS

  function toggleColumn(id: keyof CustomExportColumns) {
    setColumns((prev) => ({ ...prev, [id]: !prev[id] }))
  }

  async function handleDownload() {
    if (!fromDate || !toExclusive || !computedBucket) return
    setBusy(true)
    setError(null)
    try {
      if (computedBucket === 'raw') {
        if (!rawRangeOk) {
          setError(
            `Сирі дані обмежені діапазоном ${RAW_SAMPLES_MAX_DAYS} діб — звузьте період.`,
          )
          return
        }
        const metricKeys = rawExportMetricKeys(rawAllowedColumns)
        if (metricKeys.length === 0) {
          setError('Виберіть принаймні одну метрику з telemetry_samples.')
          return
        }
        // Send the browser's IANA tz so the CSV `time` column renders
        // in the analyst's local zone (e.g. Europe/Kyiv → "+03:00")
        // instead of UTC. Without this the day picker says "9 May"
        // but the CSV shows "8 May 21:00 .. 9 May 20:59".
        const tz = Intl.DateTimeFormat().resolvedOptions().timeZone || undefined
        const result = await fetchRawSamplesCsv({
          organizationID,
          metricKeys,
          from: fromDate.toISOString(),
          to: toExclusive.toISOString(),
          limit: RAW_SAMPLES_LIMIT,
          tz,
        })
        if (result.rows === 0 && !result.truncated) {
          setError('У вибраному діапазоні немає сирих даних — спробуйте інший період або метрики.')
          return
        }
        // Pull the metric_key → register address map for header
        // annotation parity with the bucketed wide export. Failures
        // are non-fatal: we just fall back to plain headers.
        let registerAddresses: Record<string, number> | undefined
        try {
          const reg = await fetchRegisters()
          registerAddresses = Object.fromEntries(
            Object.entries(reg.metadata).map(([k, v]) => [k, v.address]),
          )
        } catch {
          registerAddresses = undefined
        }
        // Pivot long → wide on the client. The user explicitly
        // asked for the "one row per moment, metrics as columns"
        // layout (matches the spreadsheet they ship into) instead
        // of the long-format the API streams.
        const pivot = pivotRawCsvToWide({
          longCsv: result.text,
          metricKeys,
          registerAddresses,
        })
        downloadCsv(result.filename, pivot.csv)
        if (result.truncated) {
          setError(
            `Експорт обмежено ${RAW_SAMPLES_LIMIT.toLocaleString('uk-UA')} рядками — звузьте діапазон або зменште кількість метрик.`,
          )
          return
        }
        onClose()
        return
      }
      // Pull the static metric_key → register map so the wide CSV
      // headers can be annotated with `_<address>`. We swallow fetch
      // failures here (and downgrade to plain headers) because
      // missing register annotation is strictly cosmetic — the user
      // still gets the data they asked for.
      let registerAddresses: Record<string, number> | undefined
      try {
        const reg = await fetchRegisters()
        registerAddresses = Object.fromEntries(
          Object.entries(reg.metadata).map(([k, v]) => [k, v.address]),
        )
      } catch {
        registerAddresses = undefined
      }
      const table = await fetchCustomExportData({
        organizationID,
        from: fromDate,
        to: toExclusive,
        bucket: computedBucket,
        columns: {
          ...columns,
          forecast: columns.forecast && forecastEnabled,
        },
        registerAddresses,
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
              let disabled = false
              if (opt.id === 'forecast') disabled = !forecastEnabled
              if (opt.id === 'price') disabled = !priceEnabled
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

          {isRaw && (
            <p className="export-dialog-note">
              Сирі дані — кожен зразок із <code>telemetry_samples</code> (крок ~1с/15с/30с).
              Експорт обмежений {RAW_SAMPLES_MAX_DAYS} добами та{' '}
              {RAW_SAMPLES_LIMIT.toLocaleString('uk-UA')} рядками. Колонки «Ціна РДН» та «Прогноз
              СЕС» вимкнені — у цих джерел немає сирих рядків.
            </p>
          )}

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
            disabled={!validRange || !anyColumn || !rawRangeOk || busy}
          >
            {busy ? 'Готуємо…' : 'Завантажити CSV'}
          </button>
        </footer>
      </form>
    </dialog>
  )
}
