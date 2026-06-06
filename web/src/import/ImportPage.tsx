import { useCallback, useState } from 'react'
import { runFusionSolarImport, type FusionSolarImportResult } from '../api'
import { OrganizationSelect } from '../dashboard/components/OrganizationSelect'
import { useOrganizationParam } from '../dashboard/hooks/useOrganizationParam'
import './import.css'

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
  const { organizationID, options, change: onOrganizationChange } = useOrganizationParam()
  // Default to the last importable archive day so the picker never
  // opens inside the live-data region (which the backend rejects).
  const defaultDay = kyivDate(-1) > ARCHIVE_LAST_DAY ? ARCHIVE_LAST_DAY : kyivDate(-1)
  const [fromDate, setFromDate] = useState<string>(() => defaultDay)
  const [toDate, setToDate] = useState<string>(() => defaultDay)
  const [accessToken, setAccessToken] = useState<string>('')
  const [apiBase, setApiBase] = useState<string>('')
  const [state, setState] = useState<RunState>('idle')
  const [error, setError] = useState<string | null>(null)
  const [result, setResult] = useState<FusionSolarImportResult | null>(null)

  const onRun = useCallback(async () => {
    if (!accessToken.trim()) {
      setError('Вкажіть access token FusionSolar')
      setState('error')
      return
    }
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
    setState('loading')
    setError(null)
    setResult(null)
    try {
      const res = await runFusionSolarImport({
        organizationID,
        from: dayStartIso(fromDate),
        to: dayAfterIso(toDate),
        accessToken: accessToken.trim(),
        apiBase: apiBase.trim() || undefined,
      })
      setResult(res)
      setState('done')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setState('error')
    }
  }, [organizationID, fromDate, toDate, accessToken, apiBase])

  return (
    <main className="import-page">
      <header className="import-header">
        <button type="button" className="import-back" onClick={backToDashboard}>
          ← Дашборд
        </button>
        <h1>Імпорт архіву FusionSolar</h1>
      </header>

      <section className="import-connection">
        <label className="import-field import-field-wide">
          <span>FusionSolar access token</span>
          <input
            type="password"
            value={accessToken}
            autoComplete="off"
            placeholder="Bearer-токен Northbound API"
            onChange={(e) => setAccessToken(e.target.value)}
          />
        </label>
        <label className="import-field import-field-wide">
          <span>API base (необов'язково)</span>
          <input
            type="text"
            value={apiBase}
            placeholder="https://eu5.fusionsolar.huawei.com"
            onChange={(e) => setApiBase(e.target.value)}
          />
        </label>
      </section>

      <section className="import-controls">
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
        <button
          type="button"
          className="import-run"
          onClick={onRun}
          disabled={state === 'loading'}
        >
          {state === 'loading' ? 'Імпортуємо…' : 'Запустити імпорт'}
        </button>
      </section>

      <p className="import-hint">
        Завантажує 5-хвилинні архівні дані зі SmartLogger / УЗЕ через FusionSolar
        Northbound API і записує накопичувальні лічильники в базу так, щоб дашборд і
        сторінка економіки читали їх як звичайні дані. Повторний запуск того ж
        діапазону перезаписує раніше імпортовані дані (ідемпотентно) — перезапис
        зачіпає <strong>лише</strong> рядки з позначкою архіву (source=fusionsolar),
        тож реальні дані ніколи не видаляються.
        {' '}
        Завантаження можливе тільки по <strong>{ARCHIVE_LAST_DAY}</strong> включно:
        з 01.05.2026 працюють реальні дані, і затерти їх не можна.
      </p>

      {state === 'error' && error && (
        <section className="import-banner import-banner-error" role="alert">
          Помилка імпорту: {error}
        </section>
      )}

      {state === 'done' && result && (
        <section className="import-result" role="status">
          <h2>Готово</h2>
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
              <h3>Попередження</h3>
              <ul>
                {result.warnings.map((w, i) => (
                  <li key={i}>{w}</li>
                ))}
              </ul>
            </div>
          )}
        </section>
      )}
    </main>
  )
}
