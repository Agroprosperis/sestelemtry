import { useEffect, useState } from 'react'
import { fetchOutagePoint, saveOutagePoint, type OutagePointResponse } from './outagePointClient'

type Props = {
  organizationID: string
  // canEdit is technical.write on the station; without it the block is
  // read-only.
  canEdit: boolean
}

const EIC_LENGTH = 16

function formatMoment(iso?: string): string {
  if (!iso || iso.startsWith('0001-')) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return new Intl.DateTimeFormat('uk-UA', {
    timeZone: 'Europe/Kyiv',
    day: '2-digit',
    month: '2-digit',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  }).format(d)
}

// OutagePointSection is the «Відключення (e-svitlo)» block of the station
// passport: the supply point's EIC and the grid operator whose cabinet the
// planned outages are read from. The parent keys it by station.
export function OutagePointSection({ organizationID, canEdit }: Props) {
  const [resp, setResp] = useState<OutagePointResponse | null>(null)
  const [eic, setEic] = useState('')
  const [host, setHost] = useState('')
  const [loadError, setLoadError] = useState('')
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState('')
  const [error, setError] = useState('')

  const apply = (r: OutagePointResponse) => {
    setResp(r)
    setEic(r.eic ?? '')
    setHost(r.host || r.operators[0]?.host || '')
  }

  useEffect(() => {
    const ac = new AbortController()
    fetchOutagePoint(organizationID, ac.signal)
      .then((r) => {
        setResp(r)
        setEic(r.eic ?? '')
        setHost(r.host || r.operators[0]?.host || '')
      })
      .catch((e: unknown) => {
        if (ac.signal.aborted) return
        setLoadError(e instanceof Error ? e.message : String(e))
      })
    return () => ac.abort()
  }, [organizationID])

  if (loadError) {
    return (
      <section className="station-section pvplan">
        <h2>Відключення (e-svitlo)</h2>
        <div className="station-error" role="alert">
          Не вдалося завантажити точку обліку: {loadError}
        </div>
      </section>
    )
  }
  if (!resp) {
    return (
      <section className="station-section pvplan">
        <h2>Відключення (e-svitlo)</h2>
        <div className="station-loading">Завантаження…</div>
      </section>
    )
  }

  const trimmed = eic.trim().toUpperCase()
  const dirty = trimmed !== (resp.eic ?? '') || (trimmed !== '' && host !== resp.host)
  const eicBad = trimmed !== '' && trimmed.length !== EIC_LENGTH
  const read = resp.read
  const succeeded = formatMoment(read?.succeeded_at)
  const attempted = formatMoment(read?.attempted_at)

  const save = async () => {
    if (eicBad) {
      setError(`EIC має містити ${EIC_LENGTH} символів`)
      setNotice('')
      return
    }
    setBusy(true)
    setError('')
    setNotice('')
    try {
      const r = await saveOutagePoint(organizationID, { eic: trimmed, host: trimmed ? host : '' })
      apply(r)
      setNotice(trimmed ? 'Збережено. Відключення зʼявляться на графіку після наступного читання.' : 'Точку очищено.')
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const disabled = !canEdit || busy

  return (
    <section className="station-section pvplan">
      <h2>Відключення (e-svitlo)</h2>
      <p className="pvplan-lead">
        Код точки обліку (EIC) і обленерго, з кабінету якого читаються заплановані відключення. Вони
        показуються на добовому графіку смугами. Логіни кабінетів задає адміністратор у меню «Відключення
        e-svitlo».
      </p>

      <div className="pvplan-grid">
        <label className="pvplan-field">
          EIC точки обліку
          <input
            value={eic}
            maxLength={EIC_LENGTH}
            placeholder="16 символів"
            disabled={disabled}
            onChange={(e) => {
              setEic(e.target.value)
              setNotice('')
              setError('')
            }}
          />
        </label>
        <label className="pvplan-field">
          Обленерго
          <select value={host} disabled={disabled} onChange={(e) => setHost(e.target.value)}>
            {resp.operators.map((o) => (
              <option key={o.host} value={o.host}>
                {o.name}
              </option>
            ))}
          </select>
        </label>
      </div>

      {resp.eic && succeeded ? <p className="pvplan-status">Останнє читання: {succeeded}.</p> : null}
      {resp.eic && !succeeded && !read?.error ? (
        <p className="pvplan-status">Відключення ще не читалися.</p>
      ) : null}
      {read?.error ? (
        <p className="pvplan-status err" role="alert">
          Остання спроба {attempted} не вдалася: {read.error}
        </p>
      ) : null}

      {canEdit ? (
        <div className="pvplan-actions">
          <button
            type="button"
            className="pvplan-btn primary"
            disabled={busy || !dirty || eicBad}
            onClick={() => void save()}
          >
            {busy ? 'Збереження…' : 'Зберегти'}
          </button>
          <button
            type="button"
            className="pvplan-btn"
            disabled={busy || !dirty}
            onClick={() => {
              apply(resp)
              setError('')
              setNotice('')
            }}
          >
            Скасувати зміни
          </button>
        </div>
      ) : (
        <p className="pvplan-hint">Змінювати точку обліку може інженер-керування або адміністратор.</p>
      )}
      {notice ? <p className="pvplan-notice">{notice}</p> : null}
      {error ? (
        <p className="pvplan-notice err" role="alert">
          {error}
        </p>
      ) : null}
    </section>
  )
}
