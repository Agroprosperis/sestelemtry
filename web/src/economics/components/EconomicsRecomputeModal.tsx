import { useCallback, useEffect, useRef, useState } from 'react'
import {
  recomputeEconomics,
  type EconomicsRecomputeResult,
  type ImportProgress,
} from '../../api'
import { OrganizationSelect } from '../../dashboard/components/OrganizationSelect'
import '../../import/import.css'

const ECON_LOCAL_TZ = 'Europe/Kyiv'

type RunState = 'idle' | 'loading' | 'done' | 'error'

// isAbortError detects a fetch cancelled by the operator's "cancel"
// button so the UI shows a neutral note rather than a red error.
function isAbortError(err: unknown): boolean {
  return err instanceof DOMException && err.name === 'AbortError'
}

// kyivDate returns YYYY-MM-DD at the given day offset in Europe/Kyiv,
// matching the economics page's local-time day grid.
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

type Props = {
  onClose: () => void
  organizationOptions: string[]
  // The org currently shown on the dashboard; the modal opens on it
  // but lets the operator recompute any station without navigating.
  initialOrganizationID: string
  // Invoked after a successful recompute so the page can reload the
  // freshly-stored economics for the viewed day.
  onDone?: () => void
}

// EconomicsRecomputeModal hosts the server-side economics recompute
// form in a dialog opened from the economics header. It mirrors the
// former import-page card (org + date range + run/cancel + progress)
// but lives next to the dashboard it feeds. The parent mounts it only
// while open, so its state (org defaulting to the viewed station)
// initializes fresh on every open.
export function EconomicsRecomputeModal({
  onClose,
  organizationOptions,
  initialOrganizationID,
  onDone,
}: Props) {
  const [organizationID, setOrganizationID] = useState(initialOrganizationID)
  const [fromDate, setFromDate] = useState<string>(() => kyivDate(-30))
  const [toDate, setToDate] = useState<string>(() => kyivDate(-1))
  const [state, setState] = useState<RunState>('idle')
  const [error, setError] = useState<string | null>(null)
  const [cancelled, setCancelled] = useState(false)
  const [progress, setProgress] = useState<ImportProgress | null>(null)
  const [result, setResult] = useState<EconomicsRecomputeResult | null>(null)
  const abortRef = useRef<AbortController | null>(null)
  const stateRef = useRef(state)
  stateRef.current = state

  // Close on Escape, unless a recompute is in flight (mirrors the
  // disabled overlay/close button during loading). Abort any in-flight
  // request if the dialog unmounts.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && stateRef.current !== 'loading') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('keydown', onKey)
      abortRef.current?.abort()
    }
  }, [onClose])

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
      onDone?.()
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
  }, [organizationID, fromDate, toDate, onDone])

  const pct = progress && progress.total > 0 ? Math.round((progress.done / progress.total) * 100) : 0

  return (
    <div
      className="economics-modal-overlay"
      role="presentation"
      onClick={() => {
        if (state !== 'loading') onClose()
      }}
    >
      <div
        className="economics-modal"
        role="dialog"
        aria-modal="true"
        aria-label="Перерахунок економіки"
        onClick={(e) => e.stopPropagation()}
      >
        <header className="economics-modal-head">
          <h2>Перерахунок економіки</h2>
          <button
            type="button"
            className="economics-modal-close"
            onClick={onClose}
            disabled={state === 'loading'}
            aria-label="Закрити"
          >
            ×
          </button>
        </header>

        <p className="import-section-sub">
          Перераховує погодинну економіку за діапазон дат і зберігає результат у базі.
          Запускайте після імпорту архіву чи зміни тарифів/цін РДН — дашборд читає
          збережені дані.
        </p>

        <div className="import-controls">
          <OrganizationSelect
            value={organizationID}
            options={organizationOptions}
            onChange={setOrganizationID}
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

        {state === 'loading' && progress && (
          <div className="import-progress" role="status" aria-live="polite">
            <div className="import-progress-head">
              <span>
                День {progress.done}/{progress.total}
                {progress.label ? ` — ${progress.label}` : ''}
              </span>
              <span className="import-progress-pct">{pct}%</span>
            </div>
            <div className="import-progress-track">
              <div className="import-progress-fill" style={{ width: `${pct}%` }} />
            </div>
          </div>
        )}

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
      </div>
    </div>
  )
}
