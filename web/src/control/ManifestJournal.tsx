import type { ManifestJournal as Journal } from './controlClient'

const STATUS_LABEL: Record<string, string> = {
  applied: 'застосовано',
  pending: 'очікує',
  rejected: 'відхилено',
}

const STATUS_CHIP: Record<string, string> = {
  applied: 'ok',
  pending: 'warn',
  rejected: 'err',
}

function fmtTime(iso?: string | null): string {
  if (!iso) return '—'
  return new Intl.DateTimeFormat('uk-UA', {
    timeZone: 'Europe/Kyiv',
    day: '2-digit',
    month: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date(iso))
}

// ManifestJournal shows the published manifest versions and whether the
// edge confirmed applying them (MANIFEST_APPLIED / _REJECTED events).
// fetchedAt (ms) is when the journal was loaded — heartbeat freshness is
// judged against it.
export function ManifestJournal({ journal, fetchedAt }: { journal: Journal; fetchedAt: number }) {
  const rows = journal.manifests ?? []
  const hbFresh =
    journal.heartbeat_at != null && fetchedAt - new Date(journal.heartbeat_at).getTime() < 5 * 60_000
  return (
    <section className="ctl-card">
      <h2>Журнал публікацій manifest</h2>
      <p className="ctl-card-sub">
        Edge-пристрій:{' '}
        {journal.heartbeat_at ? (
          <>
            heartbeat {fmtTime(journal.heartbeat_at)}{' '}
            <span className={'ctl-sev ' + (hbFresh ? 'ok' : 'warn')}>{hbFresh ? 'на звʼязку' : 'звʼязку немає'}</span>
          </>
        ) : (
          <span className="ctl-sev warn">ще не підключався</span>
        )}{' '}
        — «очікує» означає, що пристрій ще не підняв нову версію (poll раз на хвилину).
      </p>
      {rows.length === 0 ? (
        <div className="ctl-placeholder">
          Публікацій ще не було. Версія зʼявиться після «Підтвердити» в пульті або чергового 15-хвилинного
          перерахунку.
        </div>
      ) : (
        <table className="ctl-events-table">
          <thead>
            <tr>
              <th>Manifest</th>
              <th>Опубліковано</th>
              <th>Діє до</th>
              <th>Інтервалів</th>
              <th>Load</th>
              <th>Статус</th>
              <th>Підтверджено</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((m) => (
              <tr key={m.manifest_id}>
                <td className="mono">{m.manifest_id}</td>
                <td>{fmtTime(m.issued_at)}</td>
                <td>{fmtTime(m.valid_until)}</td>
                <td>{m.intervals}</td>
                <td>{m.load_source || '—'}</td>
                <td>
                  <span className={'ctl-sev ' + (STATUS_CHIP[m.status] ?? 'plain')}>
                    {STATUS_LABEL[m.status] ?? m.status}
                  </span>
                </td>
                <td>{fmtTime(m.applied_at ?? m.rejected_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  )
}
