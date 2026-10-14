import type { RangePreset } from '../range'

const PRESETS: { id: RangePreset; label: string }[] = [
  { id: 'day', label: 'День' },
  { id: 'month', label: 'Місяць' },
  { id: 'year', label: 'Рік' },
]

type Props = {
  value: RangePreset
  onChange: (next: RangePreset) => void
}

export function RangeSwitch({ value, onChange }: Props) {
  return (
    <div className="range-switch" role="group" aria-label="Період">
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
