import { useMemo } from 'react'
import { chartChrome, cssVar } from './cssVar'
import { useTheme } from './theme'

/** Re-reads chart chrome CSS vars whenever the resolved theme changes. */
export function useChartChrome() {
  const { resolved } = useTheme()
  return useMemo(() => chartChrome(), [resolved])
}

/** Re-reads a single CSS custom property when the theme changes. */
export function useCssVar(name: string, fallback = '') {
  const { resolved } = useTheme()
  return useMemo(() => cssVar(name, fallback), [resolved, name, fallback])
}
