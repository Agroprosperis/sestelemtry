import type { PvStationHistoryEntry } from './pvPlanClient'
import { describeAuthor, describeChange, formatDateTime } from './pvPlanForm'

// ChangeJournal lists the passport's saves, newest first.
export function ChangeJournal({ entries }: { entries: PvStationHistoryEntry[] }) {
  return (
    <details className="pvplan-card pvplan-journal">
      <summary>Журнал змін ({entries.length})</summary>
      {entries.length === 0 ? (
        <p className="pvplan-hint">Змін поки не зафіксовано.</p>
      ) : (
        <ul>
          {entries.map((e) => (
            <li key={`${e.at}-${e.by ?? ''}`}>
              <div className="pvplan-journal-head">
                <time dateTime={e.at}>{formatDateTime(e.at)}</time>
                <span>{describeAuthor(e.by)}</span>
              </div>
              <ul>
                {e.changes.map((c, i) => (
                  <li key={i}>{describeChange(c)}</li>
                ))}
              </ul>
            </li>
          ))}
        </ul>
      )}
    </details>
  )
}
