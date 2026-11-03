import { useEffect, useState } from 'react'
import { ChangeJournal } from './ChangeJournal'
import { OrientationTable } from './OrientationTable'
import { PlanHistoryCard } from './PlanHistoryCard'
import {
  fetchPvPlanHistoryStatus,
  fetchPvStation,
  fetchPvStationHistory,
  savePvStation,
  startPvPlanHistory,
  type PvPlanHistoryStatus,
  type PvStationHistoryEntry,
  type PvStationResponse,
} from './pvPlanClient'
import { formFromStation, formModelled, parsePvPlanForm, type PvPlanForm } from './pvPlanForm'
import { SourceSwitch } from './SourceSwitch'
import { StationFields } from './StationFields'

type Props = {
  organizationID: string
  // canEdit is technical.write on the station; without it the block is
  // read-only.
  canEdit: boolean
  // ratedKw is FusionSolar's rated PV power, to cross-check the planes.
  ratedKw: number | null
}

// HISTORY_POLL_MS paces the status poll while a plan-history rebuild
// runs (a year of archive takes a few minutes).
const HISTORY_POLL_MS = 3000

// PvPlanSection is the «Генерація СЕС» block of the station passport:
// which source plans the station — the n8n flow or the in-house model —
// and the passport the model runs on. It owns the passport's state; the
// parts it renders only show it. The parent keys it by station, so a
// station switch starts it afresh.
export function PvPlanSection({ organizationID, canEdit, ratedKw }: Props) {
  const [resp, setResp] = useState<PvStationResponse | null>(null)
  const [saved, setSaved] = useState<PvPlanForm | null>(null)
  const [form, setForm] = useState<PvPlanForm | null>(null)
  const [loadError, setLoadError] = useState('')
  const [entries, setEntries] = useState<PvStationHistoryEntry[]>([])
  const [job, setJob] = useState<PvPlanHistoryStatus | null>(null)
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState('')
  const [error, setError] = useState('')

  useEffect(() => {
    const ac = new AbortController()
    fetchPvStation(organizationID, ac.signal)
      .then((r) => {
        const f = formFromStation(r.station)
        setResp(r)
        setSaved(f)
        setForm(f)
        setJob(r.history_job)
      })
      .catch((e: unknown) => {
        if (ac.signal.aborted) return
        setLoadError(e instanceof Error ? e.message : String(e))
      })
    fetchPvStationHistory(organizationID, ac.signal)
      .then(setEntries)
      .catch(() => {
        // The journal is a convenience; the passport works without it.
      })
    return () => ac.abort()
  }, [organizationID])

  const running = job?.running === true
  useEffect(() => {
    if (!running) return
    const timer = window.setInterval(() => {
      fetchPvPlanHistoryStatus(organizationID)
        .then(setJob)
        .catch(() => {
          // A missed poll just waits for the next one.
        })
    }, HISTORY_POLL_MS)
    return () => window.clearInterval(timer)
  }, [organizationID, running])

  if (loadError) {
    return (
      <section className="station-section pvplan">
        <h2>Генерація СЕС</h2>
        <div className="station-error" role="alert">
          Не вдалося завантажити паспорт генерації: {loadError}
        </div>
      </section>
    )
  }
  if (!resp || !form || !saved) {
    return (
      <section className="station-section pvplan">
        <h2>Генерація СЕС</h2>
        <div className="station-loading">Завантаження…</div>
      </section>
    )
  }

  const disabled = !canEdit || busy
  const dirty = JSON.stringify(form) !== JSON.stringify(saved)
  const update = (patch: Partial<PvPlanForm>) => setForm({ ...form, ...patch })

  const save = async () => {
    const parsed = parsePvPlanForm(organizationID, form)
    if (parsed.error !== null) {
      setError(parsed.error)
      setNotice('')
      return
    }
    setBusy(true)
    setError('')
    setNotice('')
    try {
      const r = await savePvStation(organizationID, parsed.station)
      const f = formFromStation(r.station)
      setResp(r)
      setSaved(f)
      setForm(f)
      setNotice(
        'Збережено. Прогноз на сьогодні й завтра перераховується у фоні; зафіксований план на сьогодні не змінюється.',
      )
      fetchPvStationHistory(organizationID).then(setEntries).catch(() => {})
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const rebuild = async () => {
    setError('')
    setNotice('')
    try {
      setJob(await startPvPlanHistory(organizationID))
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  return (
    <section className="station-section pvplan">
      <h2>Генерація СЕС</h2>
      <p className="pvplan-lead">
        Звідки береться план генерації для дашборда, економіки й керування. Власна модель рахує його о
        06:00 і 18:00 з прогнозу Open-Meteo для кожної орієнтації панелей; план/факт порівнює прогноз,
        зафіксований напередодні.
      </p>

      <SourceSwitch
        source={form.source}
        savedSource={saved.source}
        n8nAvailable={resp.n8n_available}
        modelled={formModelled(form)}
        today={resp.today}
        disabled={disabled}
        onChange={(source) => update({ source })}
      />
      <StationFields form={form} defaults={resp.defaults} disabled={disabled} onChange={update} />
      <OrientationTable
        rows={form.rows}
        pacLimitKw={form.pacLimitKw}
        ratedKw={ratedKw}
        canEdit={canEdit}
        disabled={disabled}
        busy={busy}
        onChange={(rows) => update({ rows })}
      />

      {canEdit ? (
        <div className="pvplan-actions">
          <button
            type="button"
            className="pvplan-btn primary"
            disabled={busy || !dirty}
            onClick={() => void save()}
          >
            {busy ? 'Збереження…' : 'Зберегти'}
          </button>
          <button
            type="button"
            className="pvplan-btn"
            disabled={busy || !dirty}
            onClick={() => {
              setForm(saved)
              setError('')
              setNotice('')
            }}
          >
            Скасувати зміни
          </button>
        </div>
      ) : (
        <p className="pvplan-hint">Змінювати паспорт генерації може інженер-керування або адміністратор.</p>
      )}
      {notice ? <p className="pvplan-notice">{notice}</p> : null}
      {error ? (
        <p className="pvplan-notice err" role="alert">
          {error}
        </p>
      ) : null}

      <PlanHistoryCard
        job={job}
        backfilledAt={resp.station?.history_backfilled_at}
        canEdit={canEdit}
        canRebuild={resp.station != null && formModelled(saved)}
        dirty={dirty}
        onRebuild={() => void rebuild()}
      />
      <ChangeJournal entries={entries} />
    </section>
  )
}
