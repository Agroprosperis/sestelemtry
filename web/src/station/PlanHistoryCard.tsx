import type { PvPlanHistoryStatus } from './pvPlanClient'
import { formatDateTime } from './pvPlanForm'

type Props = {
  job: PvPlanHistoryStatus | null
  // backfilledAt is when the station's history was last rebuilt, for
  // when no rebuild has run since the API started.
  backfilledAt?: string
  canEdit: boolean
  // canRebuild: the saved passport is enough for the model to run.
  canRebuild: boolean
  // dirty: unsaved edits, which a rebuild would not see.
  dirty: boolean
  onRebuild: () => void
}

// PlanHistoryCard reports and starts the rebuild of the model's plan
// for past days.
export function PlanHistoryCard({ job, backfilledAt, canEdit, canRebuild, dirty, onRebuild }: Props) {
  return (
    <div className="pvplan-card">
      <h3>Історія плану</h3>
      <p className="pvplan-hint">
        Перераховує план власної моделі за минулі дні з архіву прогнозів Open-Meteo (прогноз за добу
        наперед), від першого дня телеметрії станції до вчора. Потрібно після зміни орієнтацій.
      </p>
      <JobStatus job={job} backfilledAt={backfilledAt} />
      {canEdit ? (
        <button
          type="button"
          className="pvplan-btn"
          disabled={job?.running === true || !canRebuild || dirty}
          title={dirty ? 'Спершу збережіть зміни' : undefined}
          onClick={onRebuild}
        >
          Перерахувати історію плану
        </button>
      ) : null}
    </div>
  )
}

function JobStatus({ job, backfilledAt }: { job: PvPlanHistoryStatus | null; backfilledAt?: string }) {
  if (job?.running) {
    const pct = job.total > 0 ? Math.round((job.done / job.total) * 100) : 0
    return (
      <p className="pvplan-status" role="status">
        Перераховується {job.from ?? '…'} — {job.to ?? '…'}: {pct} % ({job.done} з {job.total} запитів)
      </p>
    )
  }
  if (job?.error) {
    return (
      <p className="pvplan-status err" role="status">
        Перерахунок {formatDateTime(job.finished_at)} не вдався: {job.error}
      </p>
    )
  }
  if (job?.finished_at) {
    return (
      <p className="pvplan-status" role="status">
        Перераховано {formatDateTime(job.finished_at)}: {job.days} днів ({job.from} — {job.to}).
      </p>
    )
  }
  return (
    <p className="pvplan-status">
      {backfilledAt ? `Останній перерахунок: ${formatDateTime(backfilledAt)}.` : 'Історію ще не перераховано.'}
    </p>
  )
}
