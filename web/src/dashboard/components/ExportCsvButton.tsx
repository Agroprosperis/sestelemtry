import { useCallback } from 'react'
import { downloadCsv, rowsToCsv } from '../csv'
import type { ExportTable } from '../exports'

type Props = {
  filename: string
  build: () => ExportTable
  disabled?: boolean
  label?: string
}

// ExportCsvButton renders the small "Export CSV" affordance shown on each
// chart card. The actual CSV serialization is deferred behind a callback
// (`build`) so the parent only pays the cost of materializing rows when
// the user clicks the button — for the day chart that's 288 rows × 8
// columns, which is cheap but still pointless to recompute on every render.
export function ExportCsvButton({ filename, build, disabled, label }: Props) {
  const onClick = useCallback(() => {
    if (disabled) return
    const { headers, rows } = build()
    downloadCsv(filename, rowsToCsv(headers, rows))
  }, [build, disabled, filename])

  return (
    <button
      type="button"
      className="export-csv-button"
      onClick={onClick}
      disabled={disabled}
      title={disabled ? 'Немає даних для експорту' : 'Завантажити CSV'}
      aria-label={label ?? 'Експортувати CSV'}
    >
      <svg
        aria-hidden="true"
        width="14"
        height="14"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
      >
        <path d="M12 3v12" />
        <path d="m7 10 5 5 5-5" />
        <path d="M5 21h14" />
      </svg>
      <span>{label ?? 'CSV'}</span>
    </button>
  )
}
