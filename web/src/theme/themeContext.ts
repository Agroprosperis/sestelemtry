import { createContext, useContext } from 'react'

export type ThemePreference = 'light' | 'dark' | 'system'
export type ResolvedTheme = 'light' | 'dark'

export const STORAGE_KEY = 'ses.theme'

export type ThemeContextValue = {
  preference: ThemePreference
  resolved: ResolvedTheme
  setPreference: (next: ThemePreference) => void
}

export const ThemeContext = createContext<ThemeContextValue | null>(null)

export function readStoredPreference(): ThemePreference {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (raw === 'light' || raw === 'dark' || raw === 'system') return raw
  } catch {
    /* private mode / blocked storage */
  }
  return 'system'
}

function systemPrefersDark(): boolean {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return false
  return window.matchMedia('(prefers-color-scheme: dark)').matches
}

export function resolveTheme(preference: ThemePreference): ResolvedTheme {
  if (preference === 'system') return systemPrefersDark() ? 'dark' : 'light'
  return preference
}

export function applyResolvedTheme(resolved: ResolvedTheme, preference: ThemePreference) {
  const root = document.documentElement
  root.setAttribute('data-theme', resolved)
  root.setAttribute('data-theme-preference', preference)
  root.style.colorScheme = resolved
}

export function useTheme(): ThemeContextValue {
  const ctx = useContext(ThemeContext)
  if (ctx) return ctx

  // Isolated renders (unit tests) skip ThemeProvider; read the document
  // attribute set by the FOUC script, or fall back to light.
  const preference =
    typeof document !== 'undefined'
      ? ((document.documentElement.getAttribute('data-theme-preference') as ThemePreference | null) ??
        readStoredPreference())
      : 'system'
  const attr =
    typeof document !== 'undefined'
      ? document.documentElement.getAttribute('data-theme')
      : null
  const resolved: ResolvedTheme =
    attr === 'dark' || attr === 'light' ? attr : resolveTheme(preference)

  return {
    preference,
    resolved,
    setPreference: (next) => {
      try {
        localStorage.setItem(STORAGE_KEY, next)
      } catch {
        /* ignore */
      }
      applyResolvedTheme(resolveTheme(next), next)
    },
  }
}
