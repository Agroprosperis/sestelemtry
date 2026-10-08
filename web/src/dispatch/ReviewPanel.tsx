import type { DeskHour, Issue, SimResult } from './dispatchClient'
import { FUTURE_HOURS, cfgSummary, timeLabel, unknownForecastText, type Clock, type DeskModel, type ReviewGroup } from './model'

export function ReviewPanel({
  clock,
  socMinPct,
  socMaxPct,
  reserve,
  reserveError,
  onReserve,
  working,
  onBlockExport,
  review,
  result,
  hours,
  blockers,
  confirmIssues,
  canConfirm,
  confirmBusy,
  stale,
  onClose,
  onEditIssue,
  onConfirm,
}: {
  clock: Clock
  socMinPct: number
  socMaxPct: number
  reserve: string
  reserveError: string
  onReserve: (raw: string) => void
  working: DeskModel
  onBlockExport: (on: boolean) => void
  review: { loads: ReviewGroup[]; commands: ReviewGroup[]; cfgChanged: boolean }
  result: SimResult | null
  hours: DeskHour[]
  blockers: Issue[]
  confirmIssues: string
  canConfirm: boolean
  confirmBusy: boolean
  stale: boolean
  onClose: () => void
  onEditIssue: (hour: number) => void
  onConfirm: () => void
}) {
  const cfg = working.cfg
  return (
    <section className="d-review" aria-label="Підтвердження чернетки">
      <div className="d-row" style={{ justifyContent: 'space-between' }}>
        <h3>Перевірте чернетку</h3>
        <button type="button" className="d-quiet" onClick={onClose} aria-label="Закрити чернетку">
          ✕
        </button>
      </div>
      <div className="d-review-reserve">
        <label className="d-field">
          Резерв SOC для всієї чернетки
          <span className="d-input-unit">
            <input
              type="number"
              min={socMinPct}
              max={socMaxPct}
              step={5}
              value={reserve}
              onChange={(e) => onReserve(e.target.value)}
              aria-label="Резерв SOC у чернетці"
            />
            <span>%</span>
          </span>
        </label>
        <p className="d-small">Зміна перераховує чернетку перед застосуванням.</p>
        {reserveError && (
          <p className="d-small d-error" role="alert">
            {reserveError}
          </p>
        )}
      </div>
      <label className="d-check">
        <input type="checkbox" checked={cfg.block_export} onChange={(e) => onBlockExport(e.target.checked)} />
        Повністю заборонити експорт · СЕС та УЗЕ
      </label>
      <div className="d-review-list">
        {review.loads.map((g) => (
          <div className="d-review-item" key={'l' + g.start}>
            <strong>
              {timeLabel(clock, g.start)}–{timeLabel(clock, g.end)}
            </strong>
            <span>{g.text}</span>
          </div>
        ))}
        {review.commands.map((g) => (
          <div className="d-review-item" key={'c' + g.start}>
            <strong>
              {timeLabel(clock, g.start)}–{timeLabel(clock, g.end)}
            </strong>
            <span>{g.text}</span>
          </div>
        ))}
        {review.cfgChanged && (
          <div className="d-review-item">
            <strong>Для всієї чернетки</strong>
            <span>{cfgSummary(cfg)}</span>
          </div>
        )}
      </div>
      <div className="d-review-checks">
        {result && result.known_hours < FUTURE_HOURS && (
          <p className="d-small">
            {unknownForecastText(clock, result, working, hours)} Команди можна зберегти; досяжність у цих годинах ще не оцінена.
          </p>
        )}
        {blockers.length > 0 && (
          <div className="d-warning">
            <strong>Перед застосуванням виправте позначені інтервали.</strong>
            {blockers.map((x) => (
              <div className="d-issue-edit" key={x.hour + x.text}>
                <span>
                  {timeLabel(clock, x.hour)}–{timeLabel(clock, x.hour + 1)}: {x.text}
                </span>
                <button type="button" onClick={() => onEditIssue(x.hour)}>
                  Редагувати {timeLabel(clock, x.hour)}–{timeLabel(clock, x.hour + 1)}
                </button>
              </div>
            ))}
          </div>
        )}
        {confirmIssues && (
          <div className="d-warning" style={{ whiteSpace: 'pre-line' }}>
            {confirmIssues}
          </div>
        )}
      </div>
      <div className="d-review-foot">
        <span className="d-small">Shadow: план піде на edge погодинно, запису в SmartLogger немає.</span>
        <button type="button" className="d-primary" disabled={!canConfirm} onClick={onConfirm}>
          {confirmBusy ? 'Підтвердження…' : stale ? 'Розрахунок…' : 'Підтвердити'}
        </button>
      </div>
    </section>
  )
}
