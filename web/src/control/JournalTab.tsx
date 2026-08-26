// JournalTab — the «Журнал» tab: published manifest versions with
// delivery status (reuses the planner's ManifestJournal) plus the edge
// event feed (manifest applied/rejected, poll failures, overrides…) —
// the shadow-phase command audit.

import { useEffect, useState } from 'react'
import { ManifestJournal } from '../planner/ManifestJournal'
import { fetchManifestJournal, type ManifestJournal as Journal } from '../planner/plannerClient'
import '../planner/planner.css'
import type { EdgeSiteStatus } from './controlClient'

type Props = {
  site: string
  status: EdgeSiteStatus | null
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
  const [journal, setJournal] = useState<Journal | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    let cancelled = false
    const load = () =>
      fetchManifestJournal(site)
        .then((j) => !cancelled && setJournal(j))
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
      {journal && <ManifestJournal journal={journal} />}

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
                  <td className="mono">{e.code}</td>
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
