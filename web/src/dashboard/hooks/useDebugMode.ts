import { useCallback, useEffect, useState } from 'react'

// useDebugMode is a localStorage-backed flag that operators can toggle
// from the header to reveal vendor-register diagnostic annotations on
// the metric cards. Persisted across reloads (key: `dashboard:debug`)
// so a service engineer doesn't have to flip it again every time the
// tab is restored. We also listen for cross-tab `storage` events so
// flipping the toggle in one tab updates every open dashboard.
//
// Default is OFF — debug annotations are visual noise during normal
// operation and we want a clean dashboard out of the box.

const STORAGE_KEY = 'dashboard:debug'

function readStored(): boolean {
  try {
    return window.localStorage.getItem(STORAGE_KEY) === '1'
  } catch {
    return false
  }
}

function writeStored(value: boolean): void {
  try {
    if (value) window.localStorage.setItem(STORAGE_KEY, '1')
    else window.localStorage.removeItem(STORAGE_KEY)
  } catch {
    // localStorage can throw in privacy / quota modes — silently
    // ignore; debug mode just won't persist across reloads.
  }
}

export function useDebugMode(): {
  debug: boolean
  toggleDebug: () => void
} {
  const [debug, setDebug] = useState<boolean>(() => readStored())

  useEffect(() => {
    function onStorage(e: StorageEvent) {
      if (e.key !== STORAGE_KEY) return
      setDebug(readStored())
    }
    window.addEventListener('storage', onStorage)
    return () => window.removeEventListener('storage', onStorage)
  }, [])

  const toggleDebug = useCallback(() => {
    setDebug((prev) => {
      const next = !prev
      writeStored(next)
      return next
    })
  }, [])

  return { debug, toggleDebug }
}
