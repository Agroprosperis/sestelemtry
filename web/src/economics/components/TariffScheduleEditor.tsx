import { useCallback, useEffect, useState } from 'react'
import {
  deleteTariffScheduleVersion,
  fetchTariffSchedule,
  saveTariffScheduleVersion,
  type TariffScheduleVersion,
} from '../orgTariffsClient'
import type { Tariffs } from '../tariffs'

type Props = {
  organizationID: string
  // The tariff bundle currently edited in the form — saved as the
  // version body when the operator clicks "Зберегти версію".
  tariffs: Tariffs
  // Default effective-from (the day being viewed) so the common case
  // ("these tariffs apply from the day I'm looking at") is one click.
  defaultEffectiveFrom: string
}

type Status = 'idle' | 'loading' | 'saving' | 'error'

// TariffScheduleEditor manages the date-versioned tariff schedule the
// server uses to compute economics. Each version is a tariff bundle
// effective from a civil date; a historical day uses the latest version
// on or before it. Saving a version takes effect on the next recompute
// (run it from the import page) or the next read of a non-final day.
export function TariffScheduleEditor({ organizationID, tariffs, defaultEffectiveFrom }: Props) {
  const [versions, setVersions] = useState<TariffScheduleVersion[]>([])
  const [effectiveFrom, setEffectiveFrom] = useState<string>(defaultEffectiveFrom)
  const [status, setStatus] = useState<Status>('loading')
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    setEffectiveFrom(defaultEffectiveFrom)
  }, [defaultEffectiveFrom])

  const reload = useCallback(
    (signal?: AbortSignal) => {
      setStatus('loading')
      setError(null)
      return fetchTariffSchedule(organizationID, signal)
        .then((v) => {
          setVersions(v)
          setStatus('idle')
        })
        .catch((err: unknown) => {
          if ((err as DOMException)?.name === 'AbortError') return
          setError(err instanceof Error ? err.message : String(err))
          setStatus('error')
        })
    },
    [organizationID],
  )

  useEffect(() => {
    if (!organizationID) return
    const controller = new AbortController()
    void reload(controller.signal)
    return () => controller.abort()
  }, [organizationID, reload])

  const onSaveVersion = useCallback(async () => {
    if (!/^\d{4}-\d{2}-\d{2}$/.test(effectiveFrom)) {
      setError('Дата має бути у форматі РРРР-ММ-ДД')
      setStatus('error')
      return
    }
    setStatus('saving')
    setError(null)
    try {
      await saveTariffScheduleVersion(organizationID, effectiveFrom, tariffs)
      await reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setStatus('error')
    }
  }, [organizationID, effectiveFrom, tariffs, reload])

  const onDelete = useCallback(
    async (eff: string) => {
      setStatus('saving')
      setError(null)
      try {
        await deleteTariffScheduleVersion(organizationID, eff)
        await reload()
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err))
        setStatus('error')
      }
    },
    [organizationID, reload],
  )

  return (
    <div className="economics-tariff-schedule">
      <div className="economics-tariff-schedule-head">
        <h3>Версії тарифів за датами</h3>
        <p>
          Кожен історичний день використовує останню версію, що діє на цю дату.
          Збереження бере поточні значення форми вище. Зміни застосуються після
          перерахунку економіки (сторінка «Імпорт даних»).
        </p>
      </div>

      <div className="economics-tariff-schedule-add">
        <label className="economics-field">
          <span>Діє з</span>
          <input
            type="date"
            value={effectiveFrom}
            onChange={(e) => setEffectiveFrom(e.target.value)}
          />
        </label>
        <button
          type="button"
          className="economics-tariffs-reset"
          onClick={() => void onSaveVersion()}
          disabled={status === 'saving'}
        >
          {status === 'saving' ? 'Зберігаємо…' : 'Зберегти версію'}
        </button>
      </div>

      {error && (
        <p className="economics-tariff-schedule-error" role="alert">
          {error}
        </p>
      )}

      {status === 'loading' ? (
        <p className="economics-tariff-schedule-empty">Завантаження…</p>
      ) : versions.length === 0 ? (
        <p className="economics-tariff-schedule-empty">Версій ще немає.</p>
      ) : (
        <table className="economics-tariff-schedule-table">
          <thead>
            <tr>
              <th>Діє з</th>
              <th>Розподіл</th>
              <th>Передача</th>
              <th>Деградація</th>
              <th>Ємність</th>
              <th aria-label="дії" />
            </tr>
          </thead>
          <tbody>
            {versions.map((v) => (
              <tr key={v.effectiveFrom}>
                <td>{v.effectiveFrom}</td>
                <td>{v.tariffs.distributionUahPerKwh}</td>
                <td>{v.tariffs.transmissionUahPerKwh}</td>
                <td>{v.tariffs.degradationUahPerKwh}</td>
                <td>{v.tariffs.essCapacityKwh}</td>
                <td>
                  <button
                    type="button"
                    className="economics-tariff-schedule-del"
                    onClick={() => void onDelete(v.effectiveFrom)}
                    disabled={status === 'saving'}
                    title="Видалити версію"
                  >
                    Видалити
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}
