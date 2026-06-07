import { useCallback, useEffect, useRef, useState } from 'react'
import {
  refreshDAMPricesRange,
  type DAMRefreshRangeResult,
  type ImportProgress,
} from '../../api'
import '../../import/import.css'
import { ImportProgressBar, isAbortError, kyivDate, type RunState } from '../../import/shared'

type Props = {
  onClose: () => void
  // Invoked after a successful bulk DAM fetch so the page can
  // re-pull the underlying /api/v1/dam-prices reads without a hard
  // refresh.
  onDone?: (res: DAMRefreshRangeResult) => void
}

// EconomicsDamPricesModal opens the OREE bulk-loader in a dialog
// from the economics header, mirroring `EconomicsRecomputeModal`'s
// shell so the operator gets a consistent "tool launches here"
// experience. The form (from / to / zone + run/cancel + progress +
// result panel) is identical to the standalone Import page card —
// the modal just trades the `.import-card` chrome for an
// `.economics-modal` overlay so the page underneath stays in
// context. The card itself is kept around for callers that prefer
// inline placement; this is the on-demand variant.
export function EconomicsDamPricesModal({ onClose, onDone }: Props) {
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
  const stateRef = useRef(state)
  stateRef.current = state

  // Close on Escape, unless a fetch is in flight (so an accidental
  // key press doesn't abandon the in-progress import). Abort the
  // request on unmount as a safety net.
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
      onDone?.(res)
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
  }, [fromDate, toDate, zone, onDone])

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
        aria-label="Імпорт цін РДН"
        onClick={(e) => e.stopPropagation()}
      >
        <header className="economics-modal-head">
          <h2>Імпорт цін РДН (OREE)</h2>
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
      </div>
    </div>
  )
}
