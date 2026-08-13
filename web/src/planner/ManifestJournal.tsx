import type { ManifestJournal as Journal } from './plannerClient'

const STATUS_LABEL: Record<string, string> = {
  applied: 'застосовано',
  pending: 'очікує',
  rejected: 'відхилено',
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
export function ManifestJournal({ journal }: { journal: Journal }) {
  const rows = journal.manifests ?? []
  const hbFresh =
    journal.heartbeat_at != null &&
    Date.now() - new Date(journal.heartbeat_at).getTime() < 5 * 60_000
  return (
    <div className="planner-card">
      <h2>Журнал публікацій manifest</h2>
      <p className="planner-card-sub">
        Edge-пристрій:{' '}
        {journal.heartbeat_at ? (
          <>
            heartbeat {fmtTime(journal.heartbeat_at)}{' '}
            <span className={'planner-chip ' + (hbFresh ? 'applied' : 'pending')}>
              {hbFresh ? 'на звʼязку' : 'звʼязку немає'}
            </span>
          </>
        ) : (
          <span className="planner-chip pending">ще не підключався</span>
        )}
        {' '}— «очікує» означає, що пристрій ще не підняв нову версію (poll раз на хвилину).
      </p>
      {rows.length === 0 ? (
        <div className="planner-empty">
          Публікацій ще не було. Натисніть «Опублікувати на edge» — версія зʼявиться тут.
        </div>
      ) : (
        <table className="planner-journal-table">
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
                  <span className={'planner-chip ' + m.status}>{STATUS_LABEL[m.status] ?? m.status}</span>
                </td>
                <td>{fmtTime(m.applied_at ?? m.rejected_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}
