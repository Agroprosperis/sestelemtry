import { useEffect, useRef, useState } from 'react'
import { formatPeriodTitle } from '../monthly/format'

// pad / ym / shiftMonth are the small calendar helpers the picker needs to
// compute preset windows relative to "now".
function pad(n: number): string {
  return String(n).padStart(2, '0')
}

function ym(d: Date): string {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}`
}

function shiftMonth(month: string, delta: number): string {
  const m = /^(\d{4})-(\d{2})$/.exec(month)
  if (!m) return month
  const d = new Date(Number(m[1]), Number(m[2]) - 1 + delta, 1)
  return ym(d)
}

type Preset = { id: string; label: string; from: string; to: string }

// buildPresets returns the quick-pick windows offered in the dropdown,
// anchored to the current month.
function buildPresets(now: Date): Preset[] {
  const cur = ym(now)
  const y = now.getFullYear()
  return [
    { id: 'cal', label: 'Календарний рік', from: `${y}-01`, to: `${y}-12` },
    { id: 'ytd', label: 'Цей рік (з початку)', from: `${y}-01`, to: cur },
    { id: 'last12', label: 'Останні 12 місяців', from: shiftMonth(cur, -11), to: cur },
    { id: 'last6', label: 'Останні 6 місяців', from: shiftMonth(cur, -5), to: cur },
    { id: 'last3', label: 'Останні 3 місяці', from: shiftMonth(cur, -2), to: cur },
    { id: 'prev', label: 'Минулий рік', from: `${y - 1}-01`, to: `${y - 1}-12` },
  ]
}

type Props = {
  from: string
  to: string
  onChange: (from: string, to: string) => void
}

// EconomicsPeriodPicker is the year-view period selector: a single button
// showing the active window, opening a dropdown of quick presets plus a
// custom from/to month range.
export function EconomicsPeriodPicker({ from, to, onChange }: Props) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  const now = new Date()
  const presets = buildPresets(now)
  const maxMonth = ym(now)
  const label = formatPeriodTitle(from, to) || 'Оберіть період'
  const activeId = presets.find((p) => p.from === from && p.to === to)?.id ?? null

  useEffect(() => {
    if (!open) return
    function onDoc(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onDoc)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDoc)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  return (
    <div className="economics-period-picker" ref={ref}>
      <button
        type="button"
        className="economics-period-trigger"
        aria-haspopup="dialog"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
      >
        <span className="economics-period-trigger-label">{label}</span>
        <span className="economics-period-trigger-caret" aria-hidden>
          ▾
        </span>
      </button>
      {open && (
        <div className="economics-period-menu" role="dialog" aria-label="Вибір періоду">
          <div className="economics-period-presets">
            {presets.map((p) => (
              <button
                key={p.id}
                type="button"
                className={`economics-period-preset${activeId === p.id ? ' active' : ''}`}
                onClick={() => {
                  onChange(p.from, p.to)
                  setOpen(false)
                }}
              >
                {p.label}
              </button>
            ))}
          </div>
          <div className="economics-period-custom">
            <span className="economics-period-custom-title">Власний діапазон</span>
            <div className="economics-period-custom-row">
              <input
                type="month"
                aria-label="Період з"
                value={from}
                max={to || maxMonth}
                onChange={(e) => onChange(e.target.value, to)}
              />
              <span className="economics-period-dash">—</span>
              <input
                type="month"
                aria-label="Період по"
                value={to}
                min={from || undefined}
                max={maxMonth}
                onChange={(e) => onChange(from, e.target.value)}
              />
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
