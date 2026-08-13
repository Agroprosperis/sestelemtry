import { useCallback, useRef, useState } from 'react'
import {
  runAskoeImport,
  runFusionSolarImport,
  type AskoeImportResult,
  type FusionSolarImportResult,
  type ImportProgress,
} from '../api'
import { OrganizationSelect } from '../dashboard/components/OrganizationSelect'
import { useOrganizationParam } from '../dashboard/hooks/useOrganizationParam'
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

// kyivDate returns the YYYY-MM-DD civil day in Europe/Kyiv, offset by
// `offsetDays`. Used only to seed sensible default picker values.
function kyivDate(offsetDays = 0): string {
  const now = new Date()
  const kyiv = new Date(now.toLocaleString('en-US', { timeZone: 'Europe/Kyiv' }))
  kyiv.setDate(kyiv.getDate() + offsetDays)
  const y = kyiv.getFullYear()
  const m = String(kyiv.getMonth() + 1).padStart(2, '0')
  const d = String(kyiv.getDate()).padStart(2, '0')
  return `${y}-${m}-${d}`
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
          <p className="import-subtitle">Архів FusionSolar і комерційний облік АСКОЕ</p>
        </div>
      </header>

      <FusionSolarImportCard />
      <AskoeImportCard />
    </main>
  )
}

// ---- FusionSolar archive telemetry --------------------------------------

function FusionSolarImportCard() {
  const { organizationID, options, change: onOrganizationChange } = useOrganizationParam()
  const [fromDate, setFromDate] = useState<string>(() => kyivDate(-1))
  const [toDate, setToDate] = useState<string>(() => kyivDate(-1))
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
    const from = fromDate
    const to = toDate
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
  }, [organizationID, fromDate, toDate])

  return (
    <section className="import-card">
      <span className="import-card-accent" aria-hidden="true" />
      <div className="import-card-head">
        <h2 className="import-section-title">Архів телеметрії FusionSolar</h2>
        <span className="import-pill import-pill-ok">● Підключення на сервері</span>
      </div>
      <p className="import-section-sub">
        Оберіть станцію й діапазон дат. Дні, за які вже є реальні (live) дані,
        пропускаються автоматично — архів заповнює лише дні без live-даних.
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
        сторінка економіки читали їх як звичайні дані. Перед записом кожного дня
        перевіряється, чи є за цей день реальні (live) дані: якщо є — день
        пропускається й не змінюється; якщо ні — записується (повторний запуск
        оновлює лише раніше імпортовані архівні рядки з позначкою source=fusionsolar).
        Реальні дані ніколи не перезаписуються. Скасування перериває процес одразу.
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
              <dt>Пропущено днів (повністю live)</dt>
              <dd>{(result.skipped_live_windows ?? 0).toLocaleString('uk-UA')}</dd>
            </div>
            <div>
              <dt>Пропущено семплів (слот зайнятий live)</dt>
              <dd>{(result.skipped_live_samples ?? 0).toLocaleString('uk-UA')}</dd>
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

function AskoeImportCard() {
  const { organizationID, options, change: onOrganizationChange } = useOrganizationParam()
  const [file, setFile] = useState<File | null>(null)
  const [state, setState] = useState<RunState>('idle')
  const [error, setError] = useState<string | null>(null)
  const [cancelled, setCancelled] = useState(false)
  const [progress, setProgress] = useState<ImportProgress | null>(null)
  const [result, setResult] = useState<AskoeImportResult | null>(null)
  const abortRef = useRef<AbortController | null>(null)

  const onCancel = useCallback(() => {
    abortRef.current?.abort()
  }, [])

  const onRun = useCallback(async () => {
    if (!file) {
      setError('Оберіть файл .xls, .zip або .7z')
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
      const res = await runAskoeImport(
        { organizationID, file },
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
  }, [organizationID, file])

  return (
    <section className="import-card">
      <span className="import-card-accent import-card-accent-amber" aria-hidden="true" />
      <div className="import-card-head">
        <h2 className="import-section-title">Архів АСКОЕ (комерційний облік)</h2>
        <span className="import-pill import-pill-ok">● Файл з лічильників</span>
      </div>
      <p className="import-section-sub">
        Погодинні вивантаження А+/А− РУ-10 і А− СЕС. Дні з live Modbus або FusionSolar
        не змінюються — записуються лише порожні дні. Після запису перераховується економіка.
      </p>
      <div className="import-controls import-controls-askoe">
        <OrganizationSelect
          value={organizationID}
          options={options}
          onChange={onOrganizationChange}
        />
        <label className="import-field import-field-file">
          <span>Файл</span>
          <input
            type="file"
            accept=".xls,.zip,.7z,application/vnd.ms-excel,application/zip,application/x-7z-compressed"
            onChange={(e) => setFile(e.target.files?.[0] ?? null)}
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
            disabled={state === 'loading' || !file}
          >
            {state === 'loading' ? (
              <>
                <span className="import-spinner" aria-hidden="true" />
                Імпортуємо…
              </>
            ) : (
              'Імпортувати АСКОЕ'
            )}
          </button>
        </div>
      </div>

      {state === 'loading' && progress && <ImportProgressBar progress={progress} unit="День" />}

      <p className="import-hint">
        Кожна година розкладається на 12 пʼятихвилинних сходинок накопичувальних лічильників
        (ті самі metric_key, що й FusionSolar), тож денний графік потужності читає їх як
        звичайні дані. Повторний запуск перезаписує лише рядки source=askoe. Live і FusionSolar
        ніколи не затираються. Неповні дні (немає експорту або СЕС) пропускаються.
      </p>

      {cancelled && (
        <div className="import-banner import-banner-info" role="status">
          Імпорт скасовано.
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
              <dd>{result.organization_id}</dd>
            </div>
            <div>
              <dt>Період</dt>
              <dd>
                {result.from || '—'} → {result.to || '—'}
              </dd>
            </div>
            <div>
              <dt>Файлів</dt>
              <dd>{result.files_read.toLocaleString('uk-UA')}</dd>
            </div>
            <div>
              <dt>Повних днів</dt>
              <dd>{result.days_complete.toLocaleString('uk-UA')}</dd>
            </div>
            <div>
              <dt>Записано днів</dt>
              <dd>{result.days_written.toLocaleString('uk-UA')}</dd>
            </div>
            <div>
              <dt>Пропущено (live / FusionSolar)</dt>
              <dd>{result.days_skipped_occupied.toLocaleString('uk-UA')}</dd>
            </div>
            <div>
              <dt>Пропущено (неповні)</dt>
              <dd>{result.days_skipped_incomplete.toLocaleString('uk-UA')}</dd>
            </div>
            <div>
              <dt>Записано рядків</dt>
              <dd>{result.rows_written.toLocaleString('uk-UA')}</dd>
            </div>
            <div>
              <dt>Економіка (днів ок)</dt>
              <dd>{(result.economics_days_ok ?? 0).toLocaleString('uk-UA')}</dd>
            </div>
          </dl>

          {Object.keys(result.per_metric || {}).length > 0 && (
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


