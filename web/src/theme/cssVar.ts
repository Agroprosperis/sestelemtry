/** Read a CSS custom property from :root (resolved theme). */
export function cssVar(name: string, fallback = ''): string {
  if (typeof window === 'undefined') return fallback
  const value = getComputedStyle(document.documentElement).getPropertyValue(name).trim()
  return value || fallback
}

/** Chart chrome colors that follow the active theme. */
export function chartChrome() {
  return {
    grid: cssVar('--chart-grid', '#e7ecf2'),
    tick: cssVar('--chart-tick', '#8a94a6'),
    axis: cssVar('--chart-axis', '#98a2b3'),
    cursor: cssVar('--chart-cursor', 'rgba(148, 163, 184, 0.15)'),
    cursorStroke: cssVar('--chart-cursor-stroke', '#94a3b8'),
    zero: cssVar('--chart-zero', '#64748b'),
    label: cssVar('--text-muted', '#475569'),
    tooltipBorder: cssVar('--border', '#e2e8f0'),
    muted: cssVar('--text-subtle', '#64748b'),
    faint: cssVar('--text-faint', '#94a3b8'),
    neutral: cssVar('--border-strong', '#cbd5e1'),
    onAccent: cssVar('--on-accent', '#ffffff'),
    weatherBand: cssVar('--bg-muted', '#f1f5f9'),
  }
}
