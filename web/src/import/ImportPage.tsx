import { useCallback, useRef, useState } from 'react'
import {
  recomputeEconomics,
  refreshDAMPricesRange,
  runFusionSolarImport,
  type DAMRefreshRangeResult,
  type EconomicsRecomputeResult,
  type FusionSolarImportResult,
  type ImportProgress,
} from '../api'
import { OrganizationSelect } from '../dashboard/components/OrganizationSelect'
import { useOrganizationParam } from '../dashboard/hooks/useOrganizationParam'
import './import.css'

// isAbortError detects a fetch cancelled by the operator's "cancel"
// button (AbortController.abort), so the UI shows a neutral "скасовано"
// note instead of a red error banner.
function isAbortError(err: unknown): boolean {
  return err instanceof DOMException && err.name === 'AbortError'
}

// ImportProgressBar renders the live "done / total" feed streamed by the
// backend during a long import, plus an optional label (e.g. the date).
function ImportProgressBar({ progress, unit }: { progress: ImportProgress; unit: string }) {
  const pct = progress.total > 0 ? Math.round((progress.done / progress.total) * 100) : 0
  return (
    <div className="import-progress" role="status" aria-live="polite">
      <div className="import-progress-head">
        <span>
          {unit} {progress.done}/{progress.total}
          {progress.label ? ` — ${progress.label}` : ''}
        </span>
        <span className="import-progress-pct">{pct}%</span>
      </div>
      <div className="import-progress-track">
        <div className="import-progress-fill" style={{ width: `${pct}%` }} />
      </div>
    </div>
  )
}

// today / yesterday in Europe/Kyiv (YYYY-MM-DD). The dashboard and
// economics views anchor to local Ukraine time, so the import range
// pickers use the same convention to avoid an off-by-one day at the
// timezone seam. The archive is historical, so the default window is
// "yesterday → yesterday" (a single completed day).
function kyivDate(offsetDays: number): string {
  const fmt = new Intl.DateTimeFormat('en-CA', {
    timeZone: 'Europe/Kyiv',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  })
  const d = new Date()
  d.setUTCDate(d.getUTCDate() + offsetDays)
  return fmt.format(d)
}

// dayToIso converts a YYYY-MM-DD local day into an RFC3339 UTC instant
// at the given day offset's midnight UTC. We send the importer a
// half-open [from, to) window where `to` is the day *after* the
// selected end date so the whole end day is included.
function dayStartIso(date: string): string {
  return new Date(`${date}T00:00:00Z`).toISOString()
}

function dayAfterIso(date: string): string {
  const d = new Date(`${date}T00:00:00Z`)
  d.setUTCDate(d.getUTCDate() + 1)
  return d.toISOString()
}

// ARCHIVE_LAST_DAY is the last day an archive import may cover. Live
// telemetry runs from 2026-05-01 onward, so the importer (backend
// fusionsolar.ArchiveCutoff) refuses any window reaching it. The
// pickers cap at the day before so a "to-inclusive" selection lands on
// the half-open boundary (dayAfter == cutoff), which the backend
// allows. Keep this in sync with internal/fusionsolar.ArchiveCutoff.
const ARCHIVE_LAST_DAY = '2026-04-30'

type RunState = 'idle' | 'loading' | 'done' | 'error'

function backToDashboard() {
  if (typeof window === 'undefined') return
  const url = new URL(window.location.href)
  url.searchParams.delete('view')
  window.history.pushState({}, '', url.toString())
  window.dispatchEvent(new PopStateEvent('popstate'))
}

export function ImportPage() {
  return (
    <main className="import-page">
      <header className="import-header">
        <button type="button" className="import-back" onClick={backToDashboard}>
          ← Дашборд
        </button>
        <div className="import-heading">
          <h1>Імпорт даних</h1>
          <p className="import-subtitle">
            Архівна телеметрія FusionSolar і ціни РДН (OREE)
          </p>
        </div>
      </header>

      <FusionSolarImportCard />
      <DamPricesImportCard />
      <EconomicsRecomputeCard />
    </main>
  )
}

const ECON_LOCAL_TZ = 'Europe/Kyiv'

// ---- FusionSolar archive telemetry --------------------------------------

function FusionSolarImportCard() {
  const { organizationID, options, change: onOrganizationChange } = useOrganizationParam()
  // Default to the last importable archive day so the picker never
  // opens inside the live-data region (which the backend rejects).
  const defaultDay = kyivDate(-1) > ARCHIVE_LAST_DAY ? ARCHIVE_LAST_DAY : kyivDate(-1)
  const [fromDate, setFromDate] = useState<string>(() => defaultDay)
  const [toDate, setToDate] = useState<string>(() => defaultDay)
  const [state, setState] = useState<RunState>('idle')
  const [error, setError] = useState<string | null>(null)
  const [cancelled, setCancelled] = useState(false)
  const [progress, setProgress] = useState<ImportProgress | null>(null)
  const [result, setResult] = useState<FusionSolarImportResult | null>(null)
  const abortRef = useRef<AbortController | null>(null)

  const onCancel = useCallback(() => {
    abortRef.current?.abort()
  }, [])

  const onRun = useCallback(async () => {
    if (!fromDate || !toDate) {
      setError('Вкажіть діапазон дат')
      setState('error')
      return
    }
    if (toDate < fromDate) {
      setError('Кінцева дата раніше за початкову')
      setState('error')
      return
    }
    if (toDate > ARCHIVE_LAST_DAY) {
      setError(
        `Архів можна вантажити лише по ${ARCHIVE_LAST_DAY} включно — з 01.05.2026 працюють реальні дані`,
      )
      setState('error')
      return
    }
    const controller = new AbortController()
    abortRef.current = controller
    setState('loading')
    setError(null)
    setCancelled(false)
    setProgress(null)
    setResult(null)
    try {
      const res = await runFusionSolarImport(
        {
          organizationID,
          from: dayStartIso(fromDate),
          to: dayAfterIso(toDate),
        },
        { signal: controller.signal, onProgress: setProgress },
      )
      setResult(res)
      setState('done')
    } catch (err) {
      if (isAbortError(err)) {
        setCancelled(true)
        setState('idle')
      } else {
        setError(err instanceof Error ? err.message : String(err))
        setState('error')
      }
    } finally {
      abortRef.current = null
      setProgress(null)
    }
  }, [organizationID, fromDate, toDate])

  return (
    <section className="import-card">
      <span className="import-card-accent" aria-hidden="true" />
      <div className="import-card-head">
        <h2 className="import-section-title">Архів телеметрії FusionSolar</h2>
        <span className="import-pill import-pill-ok">● Підключення на сервері</span>
      </div>
      <p className="import-section-sub">
        Оберіть станцію й діапазон дат. Доступно лише по{' '}
        <strong>{ARCHIVE_LAST_DAY}</strong> включно — з 01.05.2026 працюють реальні дані.
      </p>
      <div className="import-controls">
        <OrganizationSelect
          value={organizationID}
          options={options}
          onChange={onOrganizationChange}
        />
        <label className="import-field">
          <span>Від</span>
          <input
            type="date"
            value={fromDate}
            max={toDate || ARCHIVE_LAST_DAY}
            onChange={(e) => setFromDate(e.target.value)}
          />
        </label>
        <label className="import-field">
          <span>До (включно)</span>
          <input
            type="date"
            value={toDate}
            min={fromDate || undefined}
            max={ARCHIVE_LAST_DAY}
            onChange={(e) => setToDate(e.target.value)}
          />
        </label>
        <div className="import-actions">
          {state === 'loading' && (
            <button type="button" className="import-cancel" onClick={onCancel}>
              Скасувати
            </button>
          )}
          <button
            type="button"
            className="import-run"
            onClick={onRun}
            disabled={state === 'loading'}
          >
            {state === 'loading' ? (
              <>
                <span className="import-spinner" aria-hidden="true" />
                Імпортуємо…
              </>
            ) : (
              'Запустити імпорт'
            )}
          </button>
        </div>
      </div>

      {state === 'loading' && progress && <ImportProgressBar progress={progress} unit="Вікно" />}

      <p className="import-hint">
        Завантажує 5-хвилинні архівні дані зі SmartLogger / УЗЕ через FusionSolar
        Northbound API і записує накопичувальні лічильники в базу так, щоб дашборд і
        сторінка економіки читали їх як звичайні дані. Повторний запуск того ж
        діапазону перезаписує раніше імпортовані дані (ідемпотентно) — перезапис
        зачіпає <strong>лише</strong> рядки з позначкою архіву (source=fusionsolar),
        тож реальні дані ніколи не видаляються. Скасування перериває процес одразу —
        нічого не записується (дані вносяться лише після повного завантаження).
      </p>

      {cancelled && (
        <div className="import-banner import-banner-info" role="status">
          Імпорт скасовано — дані не змінювалися.
        </div>
      )}

      {state === 'error' && error && (
        <div className="import-banner import-banner-error" role="alert">
          Помилка імпорту: {error}
        </div>
      )}

      {state === 'done' && result && (
        <div className="import-result" role="status">
          <h3>Готово</h3>
          <dl className="import-summary">
            <div>
              <dt>Станція</dt>
              <dd>
                {result.organization_id} ({result.plant_code})
              </dd>
            </div>
            <div>
              <dt>Період</dt>
              <dd>
                {new Date(result.from).toISOString()} → {new Date(result.to).toISOString()}
              </dd>
            </div>
            <div>
              <dt>Вікон (по 24 год)</dt>
              <dd>{result.windows}</dd>
            </div>
            <div>
              <dt>Записано рядків</dt>
              <dd>{result.rows_written.toLocaleString('uk-UA')}</dd>
            </div>
            <div>
              <dt>Видалено перед записом</dt>
              <dd>{result.deleted_rows.toLocaleString('uk-UA')}</dd>
            </div>
          </dl>

          {Object.keys(result.per_metric).length > 0 && (
            <table className="import-metrics">
              <thead>
                <tr>
                  <th>metric_key</th>
                  <th>точок</th>
                </tr>
              </thead>
              <tbody>
                {Object.entries(result.per_metric)
                  .sort(([a], [b]) => a.localeCompare(b))
                  .map(([key, count]) => (
                    <tr key={key}>
                      <td>{key}</td>
                      <td>{count.toLocaleString('uk-UA')}</td>
                    </tr>
                  ))}
              </tbody>
            </table>
          )}

          {result.warnings && result.warnings.length > 0 && (
            <div className="import-warnings">
              <h4>Попередження</h4>
              <ul>
                {result.warnings.map((w, i) => (
                  <li key={i}>{w}</li>
                ))}
              </ul>
            </div>
          )}
        </div>
      )}
    </section>
  )
}

// ---- Economics recompute ------------------------------------------------

function EconomicsRecomputeCard() {
  const { organizationID, options, change: onOrganizationChange } = useOrganizationParam()
  const [fromDate, setFromDate] = useState<string>(() => kyivDate(-30))
  const [toDate, setToDate] = useState<string>(() => kyivDate(-1))
  const [state, setState] = useState<RunState>('idle')
  const [error, setError] = useState<string | null>(null)
  const [cancelled, setCancelled] = useState(false)
  const [progress, setProgress] = useState<ImportProgress | null>(null)
  const [result, setResult] = useState<EconomicsRecomputeResult | null>(null)
  const abortRef = useRef<AbortController | null>(null)

  const onCancel = useCallback(() => {
    abortRef.current?.abort()
  }, [])

  const onRun = useCallback(async () => {
    if (!fromDate || !toDate) {
      setError('Вкажіть діапазон дат')
      setState('error')
      return
    }
    if (toDate < fromDate) {
      setError('Кінцева дата раніше за початкову')
      setState('error')
      return
    }
    const controller = new AbortController()
    abortRef.current = controller
    setState('loading')
    setError(null)
    setCancelled(false)
    setProgress(null)
    setResult(null)
    try {
      const res = await recomputeEconomics(
        { organizationID, from: fromDate, to: toDate, tz: ECON_LOCAL_TZ },
        { signal: controller.signal, onProgress: setProgress },
      )
      setResult(res)
      setState('done')
    } catch (err) {
      if (isAbortError(err)) {
        setCancelled(true)
        setState('idle')
      } else {
        setError(err instanceof Error ? err.message : String(err))
        setState('error')
      }
    } finally {
      abortRef.current = null
      setProgress(null)
    }
  }, [organizationID, fromDate, toDate])

  return (
    <section className="import-card">
      <span className="import-card-accent import-card-accent-violet" aria-hidden="true" />
      <div className="import-card-head">
        <h2 className="import-section-title">Економіка (перерахунок)</h2>
        <span className="import-pill import-pill-violet">∑ Розрахунок на сервері</span>
      </div>
      <p className="import-section-sub">
        Перераховує погодинну економіку за діапазон дат і зберігає результат у базі.
        Запускайте після імпорту архіву чи зміни тарифів/цін РДН — дашборд економіки
        читає збережені дані.
      </p>
      <div className="import-controls">
        <OrganizationSelect
          value={organizationID}
          options={options}
          onChange={onOrganizationChange}
        />
        <label className="import-field">
          <span>Від</span>
          <input
            type="date"
            value={fromDate}
            max={toDate || undefined}
            onChange={(e) => setFromDate(e.target.value)}
          />
        </label>
        <label className="import-field">
          <span>До (включно)</span>
          <input
            type="date"
            value={toDate}
            min={fromDate || undefined}
            onChange={(e) => setToDate(e.target.value)}
          />
        </label>
        <div className="import-actions">
          {state === 'loading' && (
            <button type="button" className="import-cancel" onClick={onCancel}>
              Скасувати
            </button>
          )}
          <button
            type="button"
            className="import-run"
            onClick={onRun}
            disabled={state === 'loading'}
          >
            {state === 'loading' ? (
              <>
                <span className="import-spinner" aria-hidden="true" />
                Рахуємо…
              </>
            ) : (
              'Перерахувати'
            )}
          </button>
        </div>
      </div>

      {state === 'loading' && progress && <ImportProgressBar progress={progress} unit="День" />}

      {cancelled && (
        <div className="import-banner import-banner-info" role="status">
          Перерахунок скасовано. Уже пораховані дні збережено.
        </div>
      )}

      {state === 'error' && error && (
        <div className="import-banner import-banner-error" role="alert">
          Помилка перерахунку: {error}
        </div>
      )}

      {state === 'done' && result && (
        <div className="import-result" role="status">
          <h3>Готово</h3>
          <dl className="import-summary">
            <div>
              <dt>Період</dt>
              <dd>
                {result.from} → {result.to}
              </dd>
            </div>
            <div>
              <dt>Днів оброблено</dt>
              <dd>{result.days}</dd>
            </div>
            <div>
              <dt>Успішно / з помилкою</dt>
              <dd>
                {result.days_ok} / {result.days_failed}
              </dd>
            </div>
          </dl>

          {result.errors && result.errors.length > 0 && (
            <div className="import-warnings">
              <h4>Дні з помилкою ({result.days_failed})</h4>
              <ul>
                {result.errors.map((e) => (
                  <li key={e.date}>
                    {e.date}: {e.error}
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
      )}
    </section>
  )
}

// ---- DAM (РДН) market prices --------------------------------------------

function DamPricesImportCard() {
  const [fromDate, setFromDate] = useState<string>(() => kyivDate(-30))
  // DAM is published a day ahead, so "tomorrow" is a valid delivery date.
  const [toDate, setToDate] = useState<string>(() => kyivDate(1))
  const [zone, setZone] = useState<string>('')
  const [state, setState] = useState<RunState>('idle')
  const [error, setError] = useState<string | null>(null)
  const [cancelled, setCancelled] = useState(false)
  const [progress, setProgress] = useState<ImportProgress | null>(null)
  const [result, setResult] = useState<DAMRefreshRangeResult | null>(null)
  const abortRef = useRef<AbortController | null>(null)

  const onCancel = useCallback(() => {
    abortRef.current?.abort()
  }, [])

  const onRun = useCallback(async () => {
    if (!fromDate || !toDate) {
      setError('Вкажіть діапазон дат')
      setState('error')
      return
    }
    if (toDate < fromDate) {
      setError('Кінцева дата раніше за початкову')
      setState('error')
      return
    }
    const zoneNum = zone.trim() === '' ? undefined : Number(zone.trim())
    if (zoneNum !== undefined && (!Number.isInteger(zoneNum) || zoneNum < 1 || zoneNum > 99)) {
      setError('Зона має бути цілим числом 1–99')
      setState('error')
      return
    }
    const controller = new AbortController()
    abortRef.current = controller
    setState('loading')
    setError(null)
    setCancelled(false)
    setProgress(null)
    setResult(null)
    try {
      const res = await refreshDAMPricesRange(
        { from: fromDate, to: toDate, zone: zoneNum },
        { signal: controller.signal, onProgress: setProgress },
      )
      setResult(res)
      setState('done')
    } catch (err) {
      if (isAbortError(err)) {
        setCancelled(true)
        setState('idle')
      } else {
        setError(err instanceof Error ? err.message : String(err))
        setState('error')
      }
    } finally {
      abortRef.current = null
      setProgress(null)
    }
  }, [fromDate, toDate, zone])

  return (
    <section className="import-card">
      <span className="import-card-accent import-card-accent-amber" aria-hidden="true" />
      <div className="import-card-head">
        <h2 className="import-section-title">Ціни РДН (OREE)</h2>
        <span className="import-pill import-pill-amber">₴ Ринок на добу наперед</span>
      </div>
      <p className="import-section-sub">
        Завантаження цін РДН за діапазон дат (можна за місяць чи рік). День за днем
        тягне XLS з OREE й оновлює <code>market_dam_prices</code>. Дні без публікації
        пропускаються без помилки.
      </p>
      <div className="import-controls import-controls-dam">
        <label className="import-field">
          <span>Від</span>
          <input
            type="date"
            value={fromDate}
            max={toDate || undefined}
            onChange={(e) => setFromDate(e.target.value)}
          />
        </label>
        <label className="import-field">
          <span>До (включно)</span>
          <input
            type="date"
            value={toDate}
            min={fromDate || undefined}
            onChange={(e) => setToDate(e.target.value)}
          />
        </label>
        <label className="import-field">
          <span>Зона</span>
          <input
            type="number"
            min={1}
            max={99}
            value={zone}
            placeholder="за замовч."
            onChange={(e) => setZone(e.target.value)}
          />
        </label>
        <div className="import-actions">
          {state === 'loading' && (
            <button type="button" className="import-cancel" onClick={onCancel}>
              Скасувати
            </button>
          )}
          <button
            type="button"
            className="import-run"
            onClick={onRun}
            disabled={state === 'loading'}
          >
            {state === 'loading' ? (
              <>
                <span className="import-spinner" aria-hidden="true" />
                Завантажуємо…
              </>
            ) : (
              'Завантажити ціни'
            )}
          </button>
        </div>
      </div>

      {state === 'loading' && progress && <ImportProgressBar progress={progress} unit="День" />}

      {cancelled && (
        <div className="import-banner import-banner-info" role="status">
          Завантаження скасовано. Уже завантажені дні залишилися в базі.
        </div>
      )}

      {state === 'error' && error && (
        <div className="import-banner import-banner-error" role="alert">
          Помилка завантаження: {error}
        </div>
      )}

      {state === 'done' && result && (
        <div className="import-result" role="status">
          <h3>Готово</h3>
          <dl className="import-summary">
            <div>
              <dt>Період</dt>
              <dd>
                {result.from} → {result.to}
              </dd>
            </div>
            <div>
              <dt>Зона</dt>
              <dd>{result.zone}</dd>
            </div>
            <div>
              <dt>Днів оброблено</dt>
              <dd>{result.days}</dd>
            </div>
            <div>
              <dt>Успішно / без даних</dt>
              <dd>
                {result.days_ok} / {result.days_failed}
              </dd>
            </div>
            <div>
              <dt>Записано рядків</dt>
              <dd>{result.rows_written.toLocaleString('uk-UA')}</dd>
            </div>
          </dl>

          {result.errors && result.errors.length > 0 && (
            <div className="import-warnings">
              <h4>Дні без даних ({result.days_failed})</h4>
              <ul>
                {result.errors.map((e) => (
                  <li key={e.date}>
                    {e.date}: {e.error}
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
      )}
    </section>
  )
}
