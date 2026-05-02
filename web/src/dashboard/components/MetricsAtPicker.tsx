import { useMemo } from 'react'

type Props = {
  value: Date | null
  onChange: (next: Date | null) => void
}

function pad(n: number): string {
  return n < 10 ? `0${n}` : String(n)
}

// HTML5 <input type="datetime-local"> expects "YYYY-MM-DDTHH:mm:ss" in the
// *local* timezone (no offset, no Z). We format using the individual
// getters rather than toISOString to avoid accidental UTC conversion.
function toLocalInputValue(d: Date): string {
  return (
    `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}` +
    `T${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
  )
}

export function MetricsAtPicker({ value, onChange }: Props) {
  const isLive = value === null
  const inputValue = useMemo(() => toLocalInputValue(value ?? new Date()), [value])

  return (
    <div className="metrics-at-picker" role="group" aria-label="Metrics snapshot time">
      <div className="metrics-at-toggle" role="tablist">
        <button
          type="button"
          role="tab"
          aria-selected={isLive}
          className={isLive ? 'active' : ''}
          onClick={() => onChange(null)}
        >
          <span className="metrics-at-dot" aria-hidden />
          Реальний час
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={!isLive}
          className={!isLive ? 'active' : ''}
          onClick={() => onChange(value ?? new Date())}
        >
          На момент
        </button>
      </div>
      {!isLive && (
        <>
          <input
            type="datetime-local"
            step={1}
            value={inputValue}
            onChange={(e) => {
              const raw = e.target.value
              if (!raw) return
              const parsed = new Date(raw)
              if (Number.isNaN(parsed.getTime())) return
              onChange(parsed)
            }}
          />
          <button
            type="button"
            className="metrics-at-now"
            onClick={() => onChange(new Date())}
            title="Застосувати поточний час"
          >
            Зараз
          </button>
        </>
      )}
    </div>
  )
}
