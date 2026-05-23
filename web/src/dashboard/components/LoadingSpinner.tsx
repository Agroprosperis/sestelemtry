import { CircleNotch } from '@phosphor-icons/react'

type Props = {
  // label is read by screen readers via aria-label so the otherwise
  // decorative spinner announces "loading" semantics.
  label?: string
  size?: number
}

// LoadingSpinner is a tiny decorative spinner used inline in the
// metrics-panel narrative card headers. The animation is driven by
// the existing `is-spinning` rule already used by the period-flow
// refresh button so all spinners on the page rotate at the same
// rate (no second timing function to maintain).
export function LoadingSpinner({ label = 'Завантаження…', size = 14 }: Props) {
  return (
    <span
      className="metrics-group-spinner is-spinning"
      role="status"
      aria-live="polite"
      aria-label={label}
    >
      <CircleNotch size={size} weight="bold" />
    </span>
  )
}
