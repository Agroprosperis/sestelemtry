import { describe, expect, it } from 'vitest'
import { chartChrome } from './cssVar'
import { applyResolvedTheme, resolveTheme } from './theme'

describe('theme', () => {
  it('resolves light and dark preferences directly', () => {
    expect(resolveTheme('light')).toBe('light')
    expect(resolveTheme('dark')).toBe('dark')
  })

  it('applies data-theme on the document element', () => {
    applyResolvedTheme('dark', 'system')
    expect(document.documentElement.getAttribute('data-theme')).toBe('dark')
    expect(document.documentElement.getAttribute('data-theme-preference')).toBe('system')
    expect(document.documentElement.style.colorScheme).toBe('dark')

    applyResolvedTheme('light', 'light')
    expect(document.documentElement.getAttribute('data-theme')).toBe('light')
  })

  it('exposes contrast-safe chrome neutrals', () => {
    const chrome = chartChrome()
    expect(chrome.muted).toBeTruthy()
    expect(chrome.neutral).toBeTruthy()
    expect(chrome.onAccent).toBeTruthy()
  })
})
