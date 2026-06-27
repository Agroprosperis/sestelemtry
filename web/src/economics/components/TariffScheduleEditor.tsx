import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  deleteTariffScheduleVersion,
  fetchTariffSchedule,
  saveTariffScheduleVersion,
  type TariffScheduleVersion,
} from '../orgTariffsClient'
import type { Tariffs } from '../tariffs'

// EPOCH_EFFECTIVE_FROM is the sentinel date the backend writes for
// the initial / catch-all tariff version. The UNIX epoch shows up
// raw and confuses operators ("did I really set tariffs in 1970?"),
// so we surface it as a labelled badge in the table instead of the
// literal "1970-01-01" string.
const EPOCH_EFFECTIVE_FROM = '1970-01-01'

function formatEffectiveFrom(iso: string): { primary: string; secondary?: string } {
  if (iso === EPOCH_EFFECTIVE_FROM) {
    return {
      primary: 'Початкова версія',
      secondary: 'діє за замовчуванням',
    }
  }
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso)
  if (!m) return { primary: iso }
  return { primary: `${m[3]}.${m[2]}.${m[1]}` }
}

const UK_NUMBER = new Intl.NumberFormat('uk-UA', {
  minimumFractionDigits: 0,
  maximumFractionDigits: 5,
})

function formatNumber(value: number): string {
  if (!Number.isFinite(value)) return '—'
  return UK_NUMBER.format(value)
}

type Props = {
  organizationID: string
  // The tariff bundle currently edited in the form — saved as the
  // version body when the operator clicks "Зберегти версію".
  tariffs: Tariffs
  // Default effective-from (the day being viewed) so the common case
  // ("these tariffs apply from the day I'm looking at") is one click.
  defaultEffectiveFrom: string
  // onLoadVersion pushes a saved version's values back into the form
  // above so the operator can edit and re-save it (upsert on the same
  // effective date overwrites the version).
  onLoadVersion: (tariffs: Tariffs) => void
}

type Status = 'idle' | 'loading' | 'saving' | 'error'

// TariffScheduleEditor manages the date-versioned tariff schedule the
// server uses to compute economics. Each version is a tariff bundle
// effective from a civil date; a historical day uses the latest version
// on or before it. Saving a version takes effect on the next recompute
// (run it from the import page) or the next read of a non-final day.
export function TariffScheduleEditor({ organizationID, tariffs, defaultEffectiveFrom, onLoadVersion }: Props) {
  const [versions, setVersions] = useState<TariffScheduleVersion[]>([])
  const [effectiveFrom, setEffectiveFrom] = useState<string>(defaultEffectiveFrom)
  const [status, setStatus] = useState<Status>('loading')
  const [error, setError] = useState<string | null>(null)
  // Tracks which saved version is currently loaded into the form above
  // (for the edit-and-resave flow), so the row can show it's being edited.
  const [editingFrom, setEditingFrom] = useState<string | null>(null)

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
      setEditingFrom(null)
      await reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setStatus('error')
    }
  }, [organizationID, effectiveFrom, tariffs, reload])

  const onEdit = useCallback(
    (v: TariffScheduleVersion) => {
      onLoadVersion(v.tariffs)
      setEffectiveFrom(v.effectiveFrom)
      setEditingFrom(v.effectiveFrom)
      setError(null)
    },
    [onLoadVersion],
  )

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

  const sortedVersions = useMemo(
    () => [...versions].sort((a, b) => b.effectiveFrom.localeCompare(a.effectiveFrom)),
    [versions],
  )

  return (
    <section className="economics-tariff-schedule" aria-labelledby="tariff-schedule-title">
      <header className="economics-tariff-schedule-head">
        <h3 id="tariff-schedule-title">Версії тарифів за датами</h3>
        <p>
          Кожен історичний день використовує останню версію, що діє на цю дату.
          Збереження бере поточні значення форми вище. Зміни застосуються після
          перерахунку економіки (сторінка «Імпорт даних»).
        </p>
      </header>

      <form
        className="economics-tariff-schedule-add"
        onSubmit={(e) => {
          e.preventDefault()
          void onSaveVersion()
        }}
      >
        <label className="economics-field economics-tariff-schedule-add-field">
          <span>Діє з</span>
          <input
            type="date"
            value={effectiveFrom}
            onChange={(e) => setEffectiveFrom(e.target.value)}
          />
        </label>
        <button
          type="submit"
          className="economics-tariff-schedule-save"
          disabled={status === 'saving'}
        >
          {status === 'saving' ? 'Зберігаємо…' : 'Зберегти версію'}
        </button>
        <span className="economics-tariff-schedule-add-hint">
          {editingFrom
            ? `Редагування версії від ${formatEffectiveFrom(editingFrom).primary} — збереження перезапише її значеннями з форми вище.`
            : 'Зберігаються поточні значення з форми вище.'}
        </span>
      </form>

      {error && (
        <p className="economics-tariff-schedule-error" role="alert">
          {error}
        </p>
      )}

      {status === 'loading' ? (
        <p className="economics-tariff-schedule-empty">Завантаження…</p>
      ) : sortedVersions.length === 0 ? (
        <p className="economics-tariff-schedule-empty">Версій ще немає.</p>
      ) : (
        <div className="economics-tariff-schedule-table-wrap">
          <table className="economics-tariff-schedule-table">
            <thead>
              <tr>
                <th scope="col">Діє з</th>
                <th scope="col" className="num">
                  Розподіл
                  <small>грн/кВт·год</small>
                </th>
                <th scope="col" className="num">
                  Передача
                  <small>грн/кВт·год</small>
                </th>
                <th scope="col" className="num">
                  Деградація
                  <small>грн/кВт·год</small>
                </th>
                <th scope="col" className="num">
                  Ємність
                  <span
                    className="economics-info"
                    data-tip="Корисна ємність УЗЕ (вікно SOC 10–90%). Для пакету 645 кВт·год вводьте 516."
                    role="img"
                    aria-label="Корисна ємність УЗЕ (вікно SOC 10–90%). Для пакету 645 кВт·год вводьте 516."
                  >
                    i
                  </span>
                  <small>кВт·год</small>
                </th>
                <th scope="col" className="num">
                  Потужність
                  <small>кВт</small>
                </th>
                <th scope="col" className="num">
                  ККД
                  <small>0..1</small>
                </th>
                <th scope="col" className="actions" aria-label="Дії" />
              </tr>
            </thead>
            <tbody>
              {sortedVersions.map((v) => {
                const eff = formatEffectiveFrom(v.effectiveFrom)
                const isEpoch = v.effectiveFrom === EPOCH_EFFECTIVE_FROM
                const isEditing = editingFrom === v.effectiveFrom
                return (
                  <tr key={v.effectiveFrom} className={isEditing ? 'is-editing' : undefined}>
                    <td>
                      <span
                        className={
                          isEpoch
                            ? 'economics-tariff-schedule-eff economics-tariff-schedule-eff-epoch'
                            : 'economics-tariff-schedule-eff'
                        }
                      >
                        {eff.primary}
                      </span>
                      {eff.secondary && (
                        <span className="economics-tariff-schedule-eff-sub">
                          {eff.secondary}
                        </span>
                      )}
                    </td>
                    <td className="num">{formatNumber(v.tariffs.distributionUahPerKwh)}</td>
                    <td className="num">{formatNumber(v.tariffs.transmissionUahPerKwh)}</td>
                    <td className="num">{formatNumber(v.tariffs.degradationUahPerKwh)}</td>
                    <td className="num">{formatNumber(v.tariffs.essCapacityKwh)}</td>
                    <td className="num">
                      {v.tariffs.essPowerLimitKw > 0 ? formatNumber(v.tariffs.essPowerLimitKw) : 'авто'}
                    </td>
                    <td className="num">
                      {v.tariffs.roundtripEfficiency > 0 ? formatNumber(v.tariffs.roundtripEfficiency) : 'емпір.'}
                    </td>
                    <td className="actions">
                      <button
                        type="button"
                        className="economics-tariff-schedule-edit"
                        onClick={() => onEdit(v)}
                        disabled={status === 'saving'}
                        title="Завантажити значення цієї версії у форму вище для редагування"
                        aria-label={`Редагувати версію, що діє з ${eff.primary}`}
                      >
                        {isEditing ? 'Редагується' : 'Редагувати'}
                      </button>
                      <button
                        type="button"
                        className="economics-tariff-schedule-del"
                        onClick={() => void onDelete(v.effectiveFrom)}
                        disabled={status === 'saving'}
                        title="Видалити версію"
                        aria-label={`Видалити версію, що діє з ${eff.primary}`}
                      >
                        Видалити
                      </button>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}
