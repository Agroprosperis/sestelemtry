import { useEffect, useRef, useState } from 'react'
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

function effectiveKw(h: PlanPreviewHour, draft: Map<string, number>): number {
  const d = draft.get(h.ts)
  return d !== undefined ? d : h.load_kw
}

// LoadEditor is step 1 of the planner, styled after the mockup's
// load-profile-editor: one column per future hour, the kW value above
// every bar, the hour below (multiples of 3 accented), amber columns =
// «решта сьогодні», a violet divider at midnight, and a detail row for
// the selected hour with ←/→ navigation.
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
  const [selectedTs, setSelectedTs] = useState<string | null>(null)
  const [uniformKw, setUniformKw] = useState('280')
  const [tab, setTab] = useState<'kw' | 'tasks'>('kw')
  const detailInput = useRef<HTMLInputElement>(null)

  // Keep the selection valid across preview refreshes.
  useEffect(() => {
    if (selectedTs && !hours.some((h) => h.ts === selectedTs)) setSelectedTs(hours[0]?.ts ?? null)
  }, [hours, selectedTs])

  const maxKw = Math.max(100, ...hours.map((h) => effectiveKw(h, draft)))
  const hourFmt = new Intl.DateTimeFormat('uk-UA', { timeZone: timezone, hour: '2-digit', hour12: false })
  const nowLabel = new Intl.DateTimeFormat('uk-UA', {
    timeZone: timezone,
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date())
  const firstTomorrow = hours.find((h) => h.tomorrow)
  const tomorrowLabel = firstTomorrow
    ? new Intl.DateTimeFormat('uk-UA', { timeZone: timezone, day: '2-digit', month: '2-digit' }).format(
        new Date(firstTomorrow.ts),
      )
    : ''

  const selected = hours.find((h) => h.ts === selectedTs) ?? null
  const selectedIdx = selected ? hours.findIndex((h) => h.ts === selected.ts) : -1

  const moveSelection = (delta: number) => {
    if (selectedIdx < 0) return
    const next = hours[selectedIdx + delta]
    if (next) setSelectedTs(next.ts)
  }

  const selectedLabel = selected
    ? `${selected.tomorrow ? 'завтра' : 'сьогодні'} ${hourFmt.format(new Date(selected.ts))}:00`
    : '—'

  return (
    <div className="planner-card">
      <div className="planner-subtabs">
        <button
          type="button"
          className={'planner-subtab' + (tab === 'kw' ? ' active' : '')}
          onClick={() => setTab('kw')}
        >
          По годинах (кВт)
        </button>
        <button
          type="button"
          className={'planner-subtab' + (tab === 'tasks' ? ' active' : '')}
          onClick={() => setTab('tasks')}
        >
          За роботами <span className="planner-chip operator">beta</span>
        </button>
      </div>

      {tab === 'tasks' && (
        <p className="planner-card-sub" style={{ margin: 0 }}>
          Планування від робіт елеватора (сушка, перевалка → розкладання по годинах) — наступний етап
          планувальника. Поки що задавайте споживання по годинах.
        </p>
      )}

      {tab === 'kw' && (
        <>
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

          <div className="planner-load-legend">
            <span className="seg">
              <span className="chip-now">● зараз {nowLabel}</span> · <b>решта сьогодні</b> (контекст)
            </span>
            <span className="seg">
              <span className="chip-tom">┃ Завтра {tomorrowLabel}</span> — цільова доба ▶
            </span>
          </div>

          <div
            className="planner-load-grid"
            style={{ gridTemplateColumns: `repeat(${hours.length}, minmax(0, 1fr))` }}
            role="group"
            aria-label="План навантаження по годинах: від зараз до кінця завтра"
          >
            {hours.map((h) => {
              const kw = effectiveKw(h, draft)
              const isOperator = draft.has(h.ts) || h.operator_load
              const label = hourFmt.format(new Date(h.ts))
              const isSelected = h.ts === selectedTs
              const classes = ['planner-load-bar']
              if (!h.tomorrow) classes.push('today')
              if (isSelected) classes.push('selected')
              if (h.tomorrow && firstTomorrow && h.ts === firstTomorrow.ts) classes.push('tomorrow-first')
              return (
                <button
                  type="button"
                  key={h.ts}
                  className={classes.join(' ')}
                  onClick={() => {
                    setSelectedTs(h.ts)
                    window.setTimeout(() => detailInput.current?.select(), 0)
                  }}
                  title={`${label}:00 — ${Math.round(kw)} кВт${isOperator ? ' (оператор)' : ' (heuristic)'}`}
                >
                  <span className="bar-val">{Math.round(kw)}</span>
                  <span className="bar-track">
                    <span
                      className={'bar-fill' + (isOperator ? ' operator' : '')}
                      style={{ height: `${Math.min(100, (kw / maxKw) * 100)}%` }}
                    />
                  </span>
                  <span className={'bar-h' + (Number(label) % 3 === 0 ? ' major' : '')}>{label}</span>
                </button>
              )
            })}
          </div>

          <div className="planner-load-detail">
            <label htmlFor="planner-load-input">
              <strong>{selectedLabel}</strong>
            </label>
            <button type="button" className="planner-button" onClick={() => moveSelection(-1)} disabled={selectedIdx <= 0}>
              ←
            </button>
            <input
              id="planner-load-input"
              ref={detailInput}
              type="number"
              min="0"
              step="5"
              value={selected ? Math.round(effectiveKw(selected, draft)) : 0}
              disabled={!selected}
              onChange={(e) => {
                if (!selected) return
                const v = Number(e.target.value)
                if (Number.isFinite(v) && v >= 0) onEdit(selected.ts, v)
              }}
              onKeyDown={(e) => {
                if (e.key === 'ArrowLeft') {
                  e.preventDefault()
                  moveSelection(-1)
                }
                if (e.key === 'ArrowRight') {
                  e.preventDefault()
                  moveSelection(1)
                }
              }}
            />
            <span style={{ color: '#64748b' }}>кВт</span>
            <button type="button" className="planner-button" onClick={() => moveSelection(1)} disabled={selectedIdx < 0 || selectedIdx >= hours.length - 1}>
              →
            </button>
            <span className="planner-load-hint">Клік на стовпчик · ← → між годинами · минуле — в аналітиці</span>
          </div>
        </>
      )}
    </div>
  )
}
