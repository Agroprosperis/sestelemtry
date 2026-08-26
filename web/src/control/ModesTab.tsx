// ModesTab — the «Режими» tab: preset cards + the operator's manual
// hourly plan. Everything publishes manifests; while a manual manifest
// is valid the rolling planner leaves it alone (backend guard), and
// «Повернутись до AUTO» force-republishes the rolling plan.

import { useMemo, useState } from 'react'
import {
  publishManualManifest,
  type EdgeSiteStatus,
  type ManualInterval,
  type PublishResult,
} from './controlClient'

type Props = {
  site: string
  status: EdgeSiteStatus | null
  onChanged: () => void
}

const MODE_CARDS: { preset: string; title: string; text: string }[] = [
  {
    preset: 'economic_arbitrage',
    title: 'AUTO · Арбітраж',
    text: 'Rolling-план кожні 15 хв: заряд у дешеві години РДН, розряд у дорогі, з урахуванням прогнозу СЕС і плану споживання.',
  },
  {
    preset: 'self_consumption',
    title: 'Самоспоживання',
    text: 'Максимум власного споживання: заряд від надлишку СЕС, розряд у локальний дефіцит. Без арбітражу.',
  },
  {
    preset: 'self_consumption_safe',
    title: 'Безпечний',
    text: 'Те саме, що самоспоживання, але без заряду з мережі за будь-яких умов. Режим за замовчуванням при втраті manifest.',
  },
]

const TTL_OPTIONS = [2, 4, 8, 12, 24]

function nextHours(n: number): Date[] {
  const start = new Date()
  start.setMinutes(0, 0, 0)
  start.setHours(start.getHours() + 1)
  return Array.from({ length: n }, (_, i) => new Date(start.getTime() + i * 3600_000))
}

function fmtHour(d: Date): string {
  return new Intl.DateTimeFormat('uk-UA', {
    weekday: 'short',
    hour: '2-digit',
    minute: '2-digit',
  }).format(d)
}

export function ModesTab({ site, status, onChanged }: Props) {
  const [ttl, setTtl] = useState(4)
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState('')
  const [error, setError] = useState('')
  // ess/soc drafts keyed by the hour's ISO ts; strings so the operator
  // can type minus signs and partial numbers freely.
  const [essDraft, setEssDraft] = useState<Map<string, string>>(new Map())
  const [socDraft, setSocDraft] = useState<Map<string, string>>(new Map())

  const hours = useMemo(() => nextHours(12), [])
  const payload = status?.manifest?.payload
  const manualActive =
    payload?.source === 'manual' &&
    !!payload.valid_until &&
    new Date(payload.valid_until).getTime() > Date.now()
  const activePreset = payload?.preset

  const report = (res: PublishResult, what: string) => {
    if (res.skipped) {
      setNotice(`Пропущено: ${res.skipped} (чинний ${res.manifest_id}).`)
    } else if (res.published) {
      setNotice(`${what}: опубліковано ${res.manifest_id}, діє до ${new Date(res.valid_until).toLocaleString('uk-UA')}. Edge підхопить протягом хвилини.`)
    } else {
      setNotice(`Без змін — чинною лишається версія ${res.manifest_id}.`)
    }
    onChanged()
  }

  const run = async (fn: () => Promise<PublishResult>, what: string) => {
    setBusy(true)
    setError('')
    setNotice('')
    try {
      report(await fn(), what)
    } catch (e) {
      setError(String(e))
    } finally {
      setBusy(false)
    }
  }

  const applyPreset = (preset: string) =>
    run(
      () => publishManualManifest(site, { preset, ttl_hours: ttl }),
      `Пресет ${preset} на ${ttl} год`,
    )

  const backToAuto = () =>
    run(() => publishManualManifest(site, { cancel: true }), 'Повернення до AUTO')

  const publishManualPlan = () => {
    const intervals: ManualInterval[] = []
    for (const h of hours) {
      const key = h.toISOString()
      const raw = (essDraft.get(key) ?? '').trim()
      if (raw === '') continue
      const ess = Number(raw.replace(',', '.'))
      if (!Number.isFinite(ess)) {
        setError(`Некоректне значення УЗЕ для ${fmtHour(h)}`)
        return
      }
      const socRaw = (socDraft.get(key) ?? '').trim()
      const iv: ManualInterval = { ts: key, ess_kw: ess }
      if (socRaw !== '') {
        const soc = Number(socRaw.replace(',', '.'))
        if (!Number.isFinite(soc) || soc < 0 || soc > 100) {
          setError(`SOC ціль для ${fmtHour(h)} має бути 0..100`)
          return
        }
        iv.soc_target_pct = soc
      }
      intervals.push(iv)
    }
    if (intervals.length === 0) {
      setError('Заповніть хоча б одну годину (УЗЕ, кВт).')
      return
    }
    void run(
      () => publishManualManifest(site, { ttl_hours: ttl, intervals, note: 'консоль: ручний план' }),
      `Ручний план (${intervals.length} год)`,
    )
  }

  return (
    <div style={{ display: 'grid', gap: 20 }}>
      <div className="ctl-shadow-note">
        SHADOW: зміна режиму публікує manifest, edge рахує його у тіні — фізично керує Encombi.
      </div>

      {notice && <div className="ctl-notice">{notice}</div>}
      {error && <div className="ctl-notice err">{error}</div>}

      <section className="ctl-card">
        <h2>Режими роботи</h2>
        <p className="ctl-card-sub">
          Активний зараз: <strong>{activePreset ?? 'невідомо'}</strong>
          {manualActive ? ' (ручне перекриття)' : ' (rolling-план)'} · TTL для застосування:{' '}
          <select value={ttl} onChange={(e) => setTtl(Number(e.target.value))} disabled={busy}>
            {TTL_OPTIONS.map((t) => (
              <option key={t} value={t}>
                {t} год
              </option>
            ))}
          </select>
        </p>
        <div className="ctl-modes-grid">
          {MODE_CARDS.map((m) => {
            const isActive = activePreset === m.preset
            const isAuto = m.preset === 'economic_arbitrage'
            return (
              <div key={m.preset} className={'ctl-mode-card' + (isActive ? ' active' : '')}>
                <h3>
                  {m.title}
                  {isActive && <span className="ctl-chip ok">активний</span>}
                </h3>
                <p>{m.text}</p>
                {isAuto ? (
                  <button
                    type="button"
                    className="ctl-btn primary"
                    disabled={busy || !manualActive}
                    title={manualActive ? '' : 'AUTO вже керує (rolling-план)'}
                    onClick={backToAuto}
                  >
                    Повернутись до AUTO
                  </button>
                ) : (
                  <button
                    type="button"
                    className="ctl-btn"
                    disabled={busy}
                    onClick={() => void applyPreset(m.preset)}
                  >
                    Застосувати на {ttl} год
                  </button>
                )}
              </div>
            )
          })}
        </div>
      </section>

      <section className="ctl-card">
        <h2>Ручний план УЗЕ</h2>
        <p className="ctl-card-sub">
          Задайте потужність по годинах: «+» — розряд у навантаження, «−» — заряд. Порожні години
          лишаються за пресетом. План діє {ttl} год, далі повертається rolling.
        </p>
        <table className="ctl-manual-table">
          <thead>
            <tr>
              <th>Година</th>
              <th>УЗЕ, кВт (+розряд / −заряд)</th>
              <th>SOC ціль, % (опц.)</th>
            </tr>
          </thead>
          <tbody>
            {hours.map((h) => {
              const key = h.toISOString()
              return (
                <tr key={key}>
                  <td>{fmtHour(h)}</td>
                  <td>
                    <input
                      inputMode="decimal"
                      placeholder="—"
                      value={essDraft.get(key) ?? ''}
                      onChange={(e) => {
                        const next = new Map(essDraft)
                        next.set(key, e.target.value)
                        setEssDraft(next)
                      }}
                    />
                  </td>
                  <td>
                    <input
                      inputMode="decimal"
                      placeholder="—"
                      value={socDraft.get(key) ?? ''}
                      onChange={(e) => {
                        const next = new Map(socDraft)
                        next.set(key, e.target.value)
                        setSocDraft(next)
                      }}
                    />
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
        <div className="ctl-form-actions">
          <button type="button" className="ctl-btn primary" disabled={busy} onClick={publishManualPlan}>
            Опублікувати ручний план ({ttl} год)
          </button>
          {manualActive && (
            <button type="button" className="ctl-btn danger" disabled={busy} onClick={backToAuto}>
              Скасувати ручний режим
            </button>
          )}
        </div>
        <p className="ctl-manual-hint">
          Поки ручний manifest чинний, rolling-планувальник його не перезаписує. Ліміти
          потужності та SOC-політика підтягуються з «Обмежень» автоматично.
        </p>
      </section>
    </div>
  )
}
