import type { ImportProgress } from '../api'

// ImportProgressBar renders the live "done / total" feed streamed by
// the backend during a long import, plus an optional label. Shared by
// the Import page and the DAM price loader on the Economics page.
export function ImportProgressBar({
  progress,
  unit,
}: {
  progress: ImportProgress
  unit: string
}) {
  const pct = progress.total > 0 ? Math.round((progress.done / progress.total) * 100) : 0
  return (
    <div className="import-progress" role="status" aria-live="polite">
      <div className="import-progress-head">
        <span>
          {unit} {progress.done}/{progress.total}
          {progress.label ? ` — ${progress.label}` : ''}
        </span>
        <span className="import-progress-pct">{pct}%</span>
      </div>
      <div className="import-progress-track">
        <div className="import-progress-fill" style={{ width: `${pct}%` }} />
      </div>
    </div>
  )
}
