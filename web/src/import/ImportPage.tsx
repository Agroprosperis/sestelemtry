import { useCallback, useMemo, useRef, useState } from 'react'
import {
  runFusionSolarImport,
  type FusionSolarImportResult,
  type ImportProgress,
} from '../api'
import { OrganizationSelect } from '../dashboard/components/OrganizationSelect'
import { useOrganizationParam } from '../dashboard/hooks/useOrganizationParam'
import { useOrganizations } from '../dashboard/hooks/useOrganizations'
import './import.css'
import { ImportProgressBar, isAbortError, type RunState } from './shared'

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
          <p className="import-subtitle">Архівна телеметрія FusionSolar</p>
        </div>
      </header>

      <FusionSolarImportCard />
    </main>
  )
}

// ---- FusionSolar archive telemetry --------------------------------------

function FusionSolarImportCard() {
  const { organizationID, options, change: onOrganizationChange } = useOrganizationParam()
  const { data: orgInfos } = useOrganizations()
  // archiveLastDay is the per-station upper bound (inclusive), the day
  // before that org's live-data start. Empty when the org has no
  // configured go-live date — archive import is then disabled so live
  // telemetry can never be overwritten.
  const orgInfo = useMemo(
    () => orgInfos.find((o) => o.id === organizationID),
    [orgInfos, organizationID],
  )
  const archiveLastDay = orgInfo?.archive_last_day ?? ''
  // archiveFirstDay is the per-station lower bound (operation start),
  // inclusive. Empty when no lower bound is configured.
  const archiveFirstDay = orgInfo?.archive_first_day ?? ''
  const importDisabled = archiveLastDay === ''
  const [fromDate, setFromDate] = useState<string>('')
  const [toDate, setToDate] = useState<string>('')
  // Before the operator picks a range, default both ends to the last
  // importable day so the picker never opens inside the live region.
  const fromValue = fromDate || archiveLastDay
  const toValue = toDate || archiveLastDay
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
    const from = fromDate || archiveLastDay
    const to = toDate || archiveLastDay
    if (!archiveLastDay) {
      setError('Дата початку live-даних не налаштована для цієї станції — імпорт вимкнено')
      setState('error')
      return
    }
    if (!from || !to) {
      setError('Вкажіть діапазон дат')
      setState('error')
      return
    }
    if (to < from) {
      setError('Кінцева дата раніше за початкову')
      setState('error')
      return
    }
    if (to > archiveLastDay) {
      setError(
        `Архів можна вантажити лише по ${archiveLastDay} включно — далі працюють реальні дані`,
      )
      setState('error')
      return
    }
    if (archiveFirstDay && from < archiveFirstDay) {
      setError(
        `Архів доступний лише з ${archiveFirstDay} — раніше станція ще не працювала`,
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
          from: dayStartIso(from),
          to: dayAfterIso(to),
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
  }, [organizationID, fromDate, toDate, archiveLastDay, archiveFirstDay])

  return (
    <section className="import-card">
      <span className="import-card-accent" aria-hidden="true" />
      <div className="import-card-head">
        <h2 className="import-section-title">Архів телеметрії FusionSolar</h2>
        <span className="import-pill import-pill-ok">● Підключення на сервері</span>
      </div>
      <p className="import-section-sub">
        Оберіть станцію й діапазон дат.{' '}
        {importDisabled ? (
          <>Для цієї станції не вказано дату початку live-даних — імпорт вимкнено.</>
        ) : (
          <>
            Доступно{' '}
            {archiveFirstDay ? (
              <>
                з <strong>{archiveFirstDay}</strong>{' '}
              </>
            ) : null}
            по <strong>{archiveLastDay}</strong> включно — далі працюють реальні дані цієї
            станції.
          </>
        )}
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
            value={fromValue}
            min={archiveFirstDay || undefined}
            max={toValue || archiveLastDay || undefined}
            disabled={importDisabled}
            onChange={(e) => setFromDate(e.target.value)}
          />
        </label>
        <label className="import-field">
          <span>До (включно)</span>
          <input
            type="date"
            value={toValue}
            min={fromValue || archiveFirstDay || undefined}
            max={archiveLastDay || undefined}
            disabled={importDisabled}
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
            disabled={state === 'loading' || importDisabled}
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

      {importDisabled && (
        <div className="import-banner import-banner-info" role="status">
          Дата початку live-даних не налаштована для станції «{organizationID}». Задайте{' '}
          <code>live_data_start</code> для цієї організації в конфігу, щоб увімкнути імпорт
          архіву.
        </div>
      )}

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

