import { useId, useMemo } from 'react'
import './PeriodPicker.css'
import { isCurrentPeriod, shiftPeriod, startOfPeriod, type RangePreset } from '../range'

type Props = {
  preset: RangePreset
  anchor: Date
  onChange: (next: Date) => void
}

function pad(n: number): string {
  return n < 10 ? `0${n}` : String(n)
}

function toDateInputValue(d: Date): string {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

function toMonthInputValue(d: Date): string {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}`
}

function parseDateInputValue(value: string): Date | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value)
  if (!m) return null
  const d = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]))
  return Number.isFinite(d.getTime()) ? d : null
}

function parseMonthInputValue(value: string): Date | null {
  const m = /^(\d{4})-(\d{2})$/.exec(value)
  if (!m) return null
  const d = new Date(Number(m[1]), Number(m[2]) - 1, 1)
  return Number.isFinite(d.getTime()) ? d : null
}

export function PeriodPicker({ preset, anchor, onChange }: Props) {
  const id = useId()
  const now = useMemo(() => new Date(), [])
  const todayMax = useMemo(() => toDateInputValue(now), [now])
  const monthMax = useMemo(() => toMonthInputValue(now), [now])
  const currentYear = now.getFullYear()
  const yearOptions = useMemo(() => {
    const start = currentYear - 9
    return Array.from({ length: 10 }, (_, i) => start + i)
  }, [currentYear])

  const isAtCurrent = isCurrentPeriod(preset, anchor, now)

  function shift(delta: number) {
    onChange(shiftPeriod(preset, anchor, delta))
  }

  let body: React.ReactNode
  if (preset === 'day') {
    body = (
      <input
        id={id}
        type="date"
        value={toDateInputValue(startOfPeriod(preset, anchor))}
        max={todayMax}
        onChange={(e) => {
          const next = parseDateInputValue(e.target.value)
          if (next) onChange(next)
        }}
      />
    )
  } else if (preset === 'month') {
    body = (
      <input
        id={id}
        type="month"
        value={toMonthInputValue(startOfPeriod(preset, anchor))}
        max={monthMax}
        onChange={(e) => {
          const next = parseMonthInputValue(e.target.value)
          if (next) onChange(next)
        }}
      />
    )
  } else {
    body = (
      <select
        id={id}
        value={anchor.getFullYear()}
        onChange={(e) => {
          const year = Number(e.target.value)
          if (Number.isFinite(year)) onChange(new Date(year, 0, 1))
        }}
      >
        {yearOptions.map((y) => (
          <option key={y} value={y}>
            {y}
          </option>
        ))}
      </select>
    )
  }

  return (
    <div className="period-picker" role="group" aria-label="Period">
      <button type="button" className="period-nav" onClick={() => shift(-1)} aria-label="Previous period">
        ‹
      </button>
      <label className="period-input" htmlFor={id}>
        {body}
      </label>
      <button
        type="button"
        className="period-nav"
        onClick={() => shift(1)}
        aria-label="Next period"
        disabled={isAtCurrent}
      >
        ›
      </button>
    </div>
  )
}
