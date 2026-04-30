import type { RangePreset } from '../range'

const PRESETS: { id: RangePreset; label: string }[] = [
  { id: 'day', label: 'Day' },
  { id: 'month', label: 'Month' },
  { id: 'year', label: 'Year' },
]

type Props = {
  value: RangePreset
  onChange: (next: RangePreset) => void
}

export function RangeSwitch({ value, onChange }: Props) {
  return (
    <div className="range-switch" role="group" aria-label="Range preset">
      {PRESETS.map((p) => {
        const active = value === p.id
        return (
          <button
            key={p.id}
            type="button"
            onClick={() => onChange(p.id)}
            className={active ? 'active' : ''}
            aria-pressed={active}
          >
            {p.label}
          </button>
        )
      })}
    </div>
  )
}
