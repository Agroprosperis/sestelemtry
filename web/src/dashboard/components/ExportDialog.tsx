import { useEffect, useMemo, useRef, useState } from 'react'
import { fetchRegisters, rawSamplesZipURL } from '../../api'
import { downloadCsv, rowsToCsv } from '../csv'
import {
  autoBucket,
  customExportFilename,
  fetchCustomExportData,
  rawExportMetricKeys,
  RAW_SAMPLES_MAX_DAYS,
  type CustomExportBucket,
  type CustomExportColumns,
} from '../customExport'
import { elevatorCodeFor } from '../transforms/pvForecast'

// triggerBrowserDownload navigates an off-DOM anchor to a download URL
// so the browser saves the response (Content-Disposition: attachment)
// straight to disk without unloading the dashboard. Used for the raw
// zip export, where the body is too large to route through fetch.
function triggerBrowserDownload(url: string): void {
  const a = document.createElement('a')
  a.href = url
  a.rel = 'noopener'
  // download hint is ignored cross-origin (the server's
  // Content-Disposition filename wins) but helps same-origin setups.
  a.download = ''
  document.body.appendChild(a)
  a.click()
  a.remove()
}

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
    hint: 'СЕС / заряд / розряд / купівля / продаж / споживання',
  },
  {
    id: 'flow',
    label: 'Перетіки енергії (kWh)',
    hint: 'Синтетичні pv→ess / grid→ess / ess→load / ess→grid (collector energyflow)',
  },
  { id: 'price', label: 'Ціна РДН (грн/МВт·год)' },
  { id: 'soc', label: 'Рівень заряду УЗЕ (SOC %)' },
  {
    id: 'power',
    label: 'Миттєва потужність (kW)',
    hint: 'СЕС / УЗЕ / мережа / навантаження. Беремо last-sample у бакеті',
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

function isAbortError(e: unknown): boolean {
  return e instanceof DOMException && e.name === 'AbortError'
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
    flow: true,
    price: true,
    soc: false,
    power: false,
    device: false,
    forecast: false,
  })
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  // abortRef carries the controller for the in-flight export so the
  // dialog can cancel its fetches when the user closes the modal,
  // hits ESC, or unmounts the component (e.g. navigating away). The
  // raw export can take dozens of seconds and pull tens of MB — we
  // can't leave it running once the user has moved on.
  const abortRef = useRef<AbortController | null>(null)
  // mountedRef gates post-await setState calls so an aborted export
  // resolving after unmount doesn't trigger React's "set state on
  // unmounted component" warning. We refrain from a hard early
  // return inside handleDownload because we still want the catch
  // branch to swallow the AbortError quietly.
  const mountedRef = useRef(true)

  useEffect(() => {
    const dialog = dialogRef.current
    if (!dialog || dialog.open) return
    dialog.showModal()
  }, [])

  useEffect(() => {
    // Reset mountedRef on every mount, not just on initial useRef
    // construction. React 18+ StrictMode runs setup/cleanup/setup in
    // dev, which previously latched mountedRef.current = false after
    // the first cleanup and never flipped it back — the next user
    // click would silently bail out of every "if (!mountedRef.current)
    // return" gate and the CSV download would never trigger. Same
    // class of bug as 2d57111 (useOrganizations).
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      abortRef.current?.abort()
    }
  }, [])

  // closeWithAbort tears down any in-flight export before propagating
  // the dialog close. Used by the explicit close button, the cancel
  // footer button, and the native ESC handler so all three paths
  // behave the same way (no zombie fetches, no late setState).
  function closeWithAbort() {
    if (busy) abortRef.current?.abort()
    onClose()
  }

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
  // rangeInvalid drives a post-hoc inline notice rather than clamping the
  // date inputs against each other. Users start from «Від», so capping
  // its `max` by «До» (and vice versa) fought the natural order — now both
  // pickers are bounded only by "not in the future" and the from<=to
  // relationship is validated after both values exist.
  const rangeInvalid =
    !!fromDate && !!toExclusive && fromDate.getTime() >= toExclusive.getTime()
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
    // Abort any prior in-flight attempt before starting a new one.
    // The submit button is disabled while busy, so this branch is
    // mostly defensive — but it also keeps the controller fresh for
    // every retry the user kicks off after a failure.
    abortRef.current?.abort()
    const controller = new AbortController()
    abortRef.current = controller
    const { signal } = controller
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
        // Raw exports can be gigabytes (a month at 1 s polling), far
        // beyond what fetch + in-memory pivot can hold. Instead we let
        // the browser stream a server-built `.zip` straight to disk:
        // the server zips the long-format CSV and streams it through
        // (X-Accel-Buffering: no), so memory stays bounded and the
        // transfer is compressed. tz keeps the `time` column in the
        // analyst's local zone (e.g. Europe/Kyiv → "+03:00").
        const tz = Intl.DateTimeFormat().resolvedOptions().timeZone || undefined
        triggerBrowserDownload(
          rawSamplesZipURL({
            organizationID,
            metricKeys,
            from: fromDate.toISOString(),
            to: toExclusive.toISOString(),
            tz,
          }),
        )
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
        const reg = await fetchRegisters(signal)
        registerAddresses = Object.fromEntries(
          Object.entries(reg.metadata).map(([k, v]) => [k, v.address]),
        )
      } catch (e) {
        if (isAbortError(e)) throw e
        registerAddresses = undefined
      }
      if (!mountedRef.current) return
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
        signal,
      })
      if (!mountedRef.current) return
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
      // Partial-source failures (DAM, forecast) are non-fatal: the
      // CSV still downloads, but the affected column is empty. We
      // surface the warnings via the existing error slot instead of
      // adding a separate banner — and we deliberately leave the
      // dialog open so the user actually reads the notice before
      // dismissing it. A clean run (no warnings) closes immediately
      // as before.
      if (table.warnings.length > 0) {
        setError(table.warnings.join(' '))
        return
      }
      onClose()
    } catch (e) {
      if (isAbortError(e)) return
      if (!mountedRef.current) return
      setError(e instanceof Error ? e.message : 'Не вдалось підготувати експорт')
    } finally {
      if (mountedRef.current) setBusy(false)
    }
  }

  return (
    <dialog
      ref={dialogRef}
      className="export-dialog"
      onClose={closeWithAbort}
      onCancel={(e) => {
        // Prevent the native ESC behavior from leaving the dialog in an
        // odd half-open state on some browsers; we'll close explicitly.
        e.preventDefault()
        closeWithAbort()
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
            onClick={closeWithAbort}
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
                max={todayMax}
                onChange={(e) => setFromStr(e.target.value)}
              />
            </label>
            <label>
              <span>До</span>
              <input
                type="date"
                value={toStr}
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
              Завантажується як <code>.zip</code> (стрім на диск), діапазон до{' '}
              {RAW_SAMPLES_MAX_DAYS} діб, без обмеження на кількість рядків. Усередині — «довгий»
              CSV (рядок на зразок). Колонки «Ціна РДН» та «Прогноз СЕС» вимкнені — у цих джерел
              немає сирих рядків.
            </p>
          )}

          {!error && rangeInvalid && (
            <p role="alert" className="export-dialog-error">
              Дата «Від» має бути не пізніше за «До».
            </p>
          )}

          {error && (
            <p role="alert" className="export-dialog-error">
              {error}
            </p>
          )}
        </div>

        <footer className="export-dialog-foot">
          <button type="button" className="export-dialog-secondary" onClick={closeWithAbort}>
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
