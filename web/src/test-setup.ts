import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach } from 'vitest'

// vitest doesn't enable Testing Library's "globals" auto-cleanup
// hook unless `test.globals: true` is set, and we keep that off so
// the rest of the suite has explicit `describe`/`it` imports. Wire
// up cleanup manually here so each `render(...)` starts with a
// fresh DOM — without this, previously-rendered components leak
// nodes across cases and accidentally satisfy or break text-
// presence assertions in the next test.
afterEach(() => {
  cleanup()
})

class ResizeObserverMock {
  observe() {}
  unobserve() {}
  disconnect() {}
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
;(globalThis as any).ResizeObserver = ResizeObserverMock
