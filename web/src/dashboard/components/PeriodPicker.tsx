import { useEffect, useId, useMemo, useState } from 'react'
import './PeriodPicker.css'
import { isCurrentPeriod, shiftPeriod, startOfPeriod, type RangePreset } from '../range'

type Props = {
  preset: RangePreset
  anchor: Date
  onChange: (next: Date) => void
}

// MIN_REASONABLE_YEAR is the floor we use to reject mid-edit garbage
// from `<input type="date">`. The control fires onChange after every
// keystroke in the year segment, so typing "2025" produces three
// intermediate values whose year parses to 2, 20, then 202. Worse,
// `new Date(2, ...)` and `new Date(20, ...)` get auto-mapped by JS
// to 1902 / 1920 because of the two-digit-year legacy, which then
// flows back into the controlled `value` and clobbers the user's
// half-typed digits — the operator sees "only the last digit
// changes". Anything that ever ran this dashboard postdates 2020,
// so rejecting years below the floor is safe and catches the
// intermediate states without false positives.
const MIN_REASONABLE_YEAR = 2020

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
  const year = Number(m[1])
  if (year < MIN_REASONABLE_YEAR) return null
  const d = new Date(year, Number(m[2]) - 1, Number(m[3]))
  return Number.isFinite(d.getTime()) ? d : null
}

function parseMonthInputValue(value: string): Date | null {
  const m = /^(\d{4})-(\d{2})$/.exec(value)
  if (!m) return null
  const year = Number(m[1])
  if (year < MIN_REASONABLE_YEAR) return null
  const d = new Date(year, Number(m[2]) - 1, 1)
  return Number.isFinite(d.getTime()) ? d : null
}

// DateSegmentInput wraps a `<input type="date"|"month">` with a
// local draft so the user can finish typing a multi-digit year
// without the parent's controlled `value` snapping the field back
// after every keystroke. The native picker fires `onChange` on
// every digit; we keep the draft on the input and only push the
// fully-typed value up via `onCommit`. The draft re-syncs when the
// caller swaps `committed` (e.g. the operator clicks the prev/next
// arrow, or types in a sibling control) so the field never lies
// about the current scope. On blur we drop the draft so any
// half-typed-then-abandoned year falls back to the committed
// value instead of staying stuck on screen.
function DateSegmentInput({
  id,
  type,
  committed,
  max,
  onCommit,
}: {
  id: string
  type: 'date' | 'month'
  committed: string
  max: string
  onCommit: (next: string) => void
}) {
  const [draft, setDraft] = useState<string | null>(null)
  useEffect(() => {
    setDraft(null)
  }, [committed])
  return (
    <input
      id={id}
      type={type}
      value={draft ?? committed}
      max={max}
      onChange={(e) => {
        const next = e.target.value
        setDraft(next)
        onCommit(next)
      }}
      onBlur={() => setDraft(null)}
    />
  )
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
    const committed = toDateInputValue(startOfPeriod(preset, anchor))
    body = (
      <DateSegmentInput
        id={id}
        type="date"
        committed={committed}
        max={todayMax}
        onCommit={(next) => {
          const parsed = parseDateInputValue(next)
          if (parsed) onChange(parsed)
        }}
      />
    )
  } else if (preset === 'month') {
    const committed = toMonthInputValue(startOfPeriod(preset, anchor))
    body = (
      <DateSegmentInput
        id={id}
        type="month"
        committed={committed}
        max={monthMax}
        onCommit={(next) => {
          const parsed = parseMonthInputValue(next)
          if (parsed) onChange(parsed)
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
