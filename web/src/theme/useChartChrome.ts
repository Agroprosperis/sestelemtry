import { useMemo } from 'react'
import { chartChrome, cssVar } from './cssVar'
import { useTheme } from './themeContext'

// The CSS vars live on :root and change only with the theme, so
// `resolved` keys the memo without being read inside it.

/** Re-reads chart chrome CSS vars whenever the resolved theme changes. */
export function useChartChrome() {
  const { resolved } = useTheme()
  // eslint-disable-next-line react-hooks/exhaustive-deps -- theme flip re-reads :root
  return useMemo(() => chartChrome(), [resolved])
}

/** Re-reads a single CSS custom property when the theme changes. */
export function useCssVar(name: string, fallback = '') {
  const { resolved } = useTheme()
  // eslint-disable-next-line react-hooks/exhaustive-deps -- theme flip re-reads :root
  return useMemo(() => cssVar(name, fallback), [resolved, name, fallback])
}
