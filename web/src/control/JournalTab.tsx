// JournalTab — the «Журнал» tab: published manifest versions with
// delivery status plus the edge event feed (manifest applied/rejected,
// poll failures, overrides…) — the shadow-phase command audit.

import { useEffect, useState } from 'react'
import { fetchManifestJournal, type EdgeSiteStatus, type ManifestJournal as Journal } from './controlClient'
import { ManifestJournal } from './ManifestJournal'

type Props = {
  site: string
  status: EdgeSiteStatus | null
}

// Human labels for edge event codes (incl. the diagnostics-spec ones:
// SL_ALARM, INVERTER_FAULT/RECOVERED).
const EVENT_CODE_LABELS: Record<string, string> = {
  SL_POLL_FAIL: 'збій опитування SmartLogger',
  SL_POLL_RECOVERED: 'опитування SL відновлено',
  UPLINK_OFFLINE: 'uplink недоступний',
  UPLINK_BACKLOG: 'черга uplink росте',
  SHADOW_ANOMALY: 'аномалія shadow-двигуна',
  DISPATCH_DEGRADED: 'команду обрізано лімітами',
  MANIFEST_APPLIED: 'manifest застосовано',
  MANIFEST_EXPIRED: 'manifest прострочено',
  MANIFEST_REJECTED: 'manifest відхилено',
  OVERRIDE_SET: 'локальний override увімкнено',
  OVERRIDE_CLEARED: 'локальний override знято',
  SL_ALARM: 'аварія SmartLogger (50000…50007)',
  SL_ALARM_WORDS_FALLBACK: 'логер відмовив 50006/50007 — читаємо 6 слів',
  METER_READ_DISABLED: 'лічильник не віддає регістри — опитування вимкнено',
  INVERTER_FAULT: 'аварія / втрата інвертора',
  INVERTER_RECOVERED: 'інвертор відновився',
}

function fmtTime(iso: string): string {
  return new Intl.DateTimeFormat('uk-UA', {
    day: '2-digit',
    month: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).format(new Date(iso))
}

export function JournalTab({ site, status }: Props) {
  const [journal, setJournal] = useState<{ data: Journal; at: number } | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    let cancelled = false
    const load = () =>
      fetchManifestJournal(site)
        .then((j) => !cancelled && setJournal({ data: j, at: Date.now() }))
        .catch((e) => !cancelled && setError(String(e)))
    void load()
    const id = window.setInterval(load, 30_000)
    return () => {
      cancelled = true
      window.clearInterval(id)
    }
  }, [site])

  const events = status?.events ?? []

  return (
    <div style={{ display: 'grid', gap: 20 }}>
      {error && <div className="ctl-notice err">{error}</div>}
      {journal && <ManifestJournal journal={journal.data} fetchedAt={journal.at} />}

      <section className="ctl-card">
        <h2>Події edge-пристрою</h2>
        <p className="ctl-card-sub">
          Аудит: застосування manifest, збої опитування SmartLogger, аномалії shadow-двигуна,
          локальні перекриття з консолі пристрою.
        </p>
        {events.length === 0 ? (
          <div className="ctl-placeholder">Подій ще немає.</div>
        ) : (
          <table className="ctl-events-table">
            <thead>
              <tr>
                <th>Час</th>
                <th>Рівень</th>
                <th>Код</th>
                <th>Повідомлення</th>
              </tr>
            </thead>
            <tbody>
              {events.map((e, i) => (
                <tr key={`${e.time}-${i}`}>
                  <td>{fmtTime(e.time)}</td>
                  <td>
                    <span className={'ctl-sev ' + e.severity}>{e.severity}</span>
                  </td>
                  <td className="mono">
                    {e.code}
                    {EVENT_CODE_LABELS[e.code] && (
                      <div className="ctl-event-code-label">{EVENT_CODE_LABELS[e.code]}</div>
                    )}
                  </td>
                  <td>{e.message}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
    </div>
  )
}
