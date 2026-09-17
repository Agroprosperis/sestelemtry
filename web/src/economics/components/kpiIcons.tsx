// KpiIcon renders one 24×24 stroke path in the host card's accent
// colour. Paths live in kpiIconPaths.ts (component-free module) so
// this file exports only a component and Fast Refresh keeps working.

export function KpiIcon({ d }: { d: string }) {
  return (
    <svg
      viewBox="0 0 24 24"
      width="16"
      height="16"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d={d} />
    </svg>
  )
}
