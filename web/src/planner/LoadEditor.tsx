import { useState } from 'react'
import type { PlanPreviewHour } from './plannerClient'

type Props = {
  hours: PlanPreviewHour[]
  timezone: string
  // draft holds the operator's values keyed by hour ts; hours not in
  // the draft show the heuristic profile from the preview.
  draft: Map<string, number>
  onEdit: (ts: string, kw: number) => void
  onFillYesterday: () => void
  onFillUniform: (kw: number) => void
  onClear: () => void
  busy?: boolean
}

// LoadEditor is step 1 of the planner: one column per future hour
// (spec: value above the bar, hour below, amber = «решта сьогодні»,
// dashed violet divider = midnight into tomorrow).
export function LoadEditor({
  hours,
  timezone,
  draft,
  onEdit,
  onFillYesterday,
  onFillUniform,
  onClear,
  busy,
}: Props) {
  const [editingTs, setEditingTs] = useState<string | null>(null)
  const [editValue, setEditValue] = useState('')
  const [uniformKw, setUniformKw] = useState('280')

  const maxKw = Math.max(100, ...hours.map((h) => effectiveKw(h, draft)))
  const hourFmt = new Intl.DateTimeFormat('uk-UA', {
    timeZone: timezone,
    hour: '2-digit',
    hour12: false,
  })

  const commitEdit = (ts: string) => {
    const v = Number(editValue.replace(',', '.'))
    if (Number.isFinite(v) && v >= 0) onEdit(ts, Math.round(v * 10) / 10)
    setEditingTs(null)
  }

  let firstTomorrowSeen = false
  return (
    <div className="planner-card">
      <h2>Крок 1 — план споживання</h2>
      <p className="planner-card-sub">
        Клікніть на годину, щоб задати кВт. Бурштинові стовпці — решта сьогодні (контекст),
        фіолетовий поділ — початок завтра. Синім — операторські години, блакитним — heuristic-профіль.
      </p>

      <div className="planner-editor-toolbar">
        <button type="button" className="planner-button" onClick={onFillYesterday} disabled={busy}>
          Заповнити з учора (факт)
        </button>
        <span>
          Рівномірно{' '}
          <input
            type="number"
            min="0"
            value={uniformKw}
            onChange={(e) => setUniformKw(e.target.value)}
            aria-label="кВт для рівномірного заповнення"
          />{' '}
          кВт
        </span>
        <button
          type="button"
          className="planner-button"
          disabled={busy || !Number.isFinite(Number(uniformKw))}
          onClick={() => onFillUniform(Number(uniformKw))}
        >
          Застосувати
        </button>
        <button type="button" className="planner-button" onClick={onClear} disabled={busy}>
          Очистити (повернути heuristic)
        </button>
      </div>

      <div className="planner-editor-scroll">
        <div className="planner-editor-grid">
          {hours.map((h, i) => {
            const kw = effectiveKw(h, draft)
            const isOperator = draft.has(h.ts) || h.operator_load
            const tomorrowFirst = h.tomorrow && !firstTomorrowSeen
            if (tomorrowFirst) firstTomorrowSeen = true
            const classes = ['planner-hour-col']
            if (!h.tomorrow) classes.push('today')
            if (tomorrowFirst) classes.push('tomorrow-first')
            if (i === 0) classes.push('now-first')
            const label = hourFmt.format(new Date(h.ts))
            return (
              <div
                key={h.ts}
                className={classes.join(' ')}
                onClick={() => {
                  if (editingTs !== h.ts) {
                    setEditingTs(h.ts)
                    setEditValue(String(kw))
                  }
                }}
              >
                <span className={'planner-hour-kw' + (isOperator ? ' operator' : '')}>
                  {editingTs === h.ts ? (
                    <input
                      autoFocus
                      value={editValue}
                      onChange={(e) => setEditValue(e.target.value)}
                      onBlur={() => commitEdit(h.ts)}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter') commitEdit(h.ts)
                        if (e.key === 'Escape') setEditingTs(null)
                      }}
                      aria-label={`Навантаження о ${label}`}
                    />
                  ) : (
                    Math.round(kw)
                  )}
                </span>
                <div className="planner-hour-bar-track">
                  <div
                    className={'planner-hour-bar' + (isOperator ? ' operator' : '')}
                    style={{ height: `${Math.min(100, (kw / maxKw) * 100)}%` }}
                  />
                </div>
                <span
                  className={
                    'planner-hour-label' + (Number(label) % 3 === 0 ? ' accent' : '')
                  }
                >
                  {label}
                </span>
              </div>
            )
          })}
        </div>
      </div>
    </div>
  )
}

function effectiveKw(h: PlanPreviewHour, draft: Map<string, number>): number {
  const d = draft.get(h.ts)
  return d !== undefined ? d : h.load_kw
}
