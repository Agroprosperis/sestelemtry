export type ViewMode = 'overview' | 'dashboard'

const VIEWS: { id: ViewMode; label: string; title: string }[] = [
  {
    id: 'overview',
    label: 'Огляд',
    title: 'Сьогоднішній енергобаланс на одній сторінці',
  },
  {
    id: 'dashboard',
    label: 'Детально',
    title: 'Графіки, лічильники, експорт даних',
  },
]

type Props = {
  value: ViewMode
  onChange: (next: ViewMode) => void
}

// ViewSwitch is the segmented "Огляд / Детально" toggle. It mirrors
// RangeSwitch visually so the controls strip looks like a single
// row of segmented selectors. Lives in /dashboard/components/
// because both the detailed dashboard and the overview page share
// the same control bar; routing happens through ?view= updates by
// the parent (DashboardControls) so this stays a presentational
// segmented control with no router awareness.
export function ViewSwitch({ value, onChange }: Props) {
  return (
    <div className="range-switch view-switch" role="group" aria-label="Режим перегляду">
      {VIEWS.map((v) => {
        const active = value === v.id
        return (
          <button
            key={v.id}
            type="button"
            onClick={() => onChange(v.id)}
            className={active ? 'active' : ''}
            aria-pressed={active}
            title={v.title}
          >
            {v.label}
          </button>
        )
      })}
    </div>
  )
}
