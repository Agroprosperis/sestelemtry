import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import {
  STORAGE_KEY,
  ThemeContext,
  applyResolvedTheme,
  readStoredPreference,
  resolveTheme,
  type ResolvedTheme,
  type ThemePreference,
} from './themeContext'

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [preference, setPreferenceState] = useState<ThemePreference>(() =>
    typeof window === 'undefined' ? 'system' : readStoredPreference(),
  )
  const [resolved, setResolved] = useState<ResolvedTheme>(() =>
    typeof window === 'undefined' ? 'light' : resolveTheme(readStoredPreference()),
  )

  const setPreference = useCallback((next: ThemePreference) => {
    setPreferenceState(next)
    try {
      localStorage.setItem(STORAGE_KEY, next)
    } catch {
      /* ignore */
    }
    const nextResolved = resolveTheme(next)
    setResolved(nextResolved)
    applyResolvedTheme(nextResolved, next)
  }, [])

  useEffect(() => {
    applyResolvedTheme(resolved, preference)
  }, [resolved, preference])

  useEffect(() => {
    if (preference !== 'system') return
    const mq = window.matchMedia('(prefers-color-scheme: dark)')
    const onChange = () => {
      const next = resolveTheme('system')
      setResolved(next)
      applyResolvedTheme(next, 'system')
    }
    mq.addEventListener('change', onChange)
    return () => mq.removeEventListener('change', onChange)
  }, [preference])

  const value = useMemo(
    () => ({ preference, resolved, setPreference }),
    [preference, resolved, setPreference],
  )

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>
}
