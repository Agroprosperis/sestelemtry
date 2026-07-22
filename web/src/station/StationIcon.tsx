import type { StationIconId } from './stationParams'

type Props = {
  id: StationIconId
  className?: string
}

export function StationIcon({ id, className }: Props) {
  const common = {
    className,
    width: 22,
    height: 22,
    viewBox: '0 0 24 24',
    fill: 'none',
    stroke: 'currentColor',
    strokeWidth: 1.75,
    strokeLinecap: 'round' as const,
    strokeLinejoin: 'round' as const,
    'aria-hidden': true as const,
  }

  switch (id) {
    case 'pv':
      return (
        <svg {...common}>
          <circle cx="12" cy="12" r="4" />
          <path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41" />
        </svg>
      )
    case 'essPower':
      return (
        <svg {...common}>
          <path d="M13 2 3 14h9l-1 8 10-12h-9l1-8z" />
        </svg>
      )
    case 'essEnergy':
      return (
        <svg {...common}>
          <rect x="6" y="7" width="12" height="14" rx="2" />
          <path d="M10 7V5a2 2 0 0 1 4 0v2" />
          <path d="M10 12h4M10 16h4" />
        </svg>
      )
    case 'cabinets':
      return (
        <svg {...common}>
          <rect x="3" y="4" width="7" height="16" rx="1" />
          <rect x="14" y="4" width="7" height="16" rx="1" />
          <path d="M6.5 8v0M17.5 8v0M6.5 12v0M17.5 12v0" />
        </svg>
      )
    case 'pcs':
      return (
        <svg {...common}>
          <rect x="2" y="6" width="20" height="12" rx="2" />
          <path d="M6 10h4M6 14h2M14 10h4M14 14h2" />
        </svg>
      )
    case 'soh':
      return (
        <svg {...common}>
          <path d="M22 12h-4l-3 7L9 5l-3 7H2" />
        </svg>
      )
    case 'mode':
      return (
        <svg {...common}>
          <circle cx="12" cy="12" r="3" />
          <path d="M12 2v3M12 19v3M4.9 4.9l2.1 2.1M17 17l2.1 2.1M2 12h3M19 12h3M4.9 19.1 7 17M17 7l2.1-2.1" />
        </svg>
      )
    case 'meta':
    default:
      return (
        <svg {...common}>
          <circle cx="12" cy="12" r="10" />
          <path d="M12 16v-4M12 8h.01" />
        </svg>
      )
  }
}
