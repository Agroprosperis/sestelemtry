import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { EdgeSiteStatus } from '../control/controlClient'
import { EdgeStatusChips } from './EdgeStatusChips'

function status(writing: boolean): EdgeSiteStatus {
  return {
    site_id: 'ze',
    heartbeat: { online: true, status: 'shadow', buffer_pending: 0 },
    manifest: { state: 'applied', manifest_id: 'ze-20261118-585114c3' },
    decision: { at: '', age_seconds: 1, record: { mode: 'shadow' } },
    health: { ts: '', ok: true, checks: [], write: { enabled: writing } },
  } as unknown as EdgeSiteStatus
}

describe('EdgeStatusChips', () => {
  it('shows the controller mode while the IoT2050 does not write', () => {
    render(<EdgeStatusChips status={status(false)} mode="control" />)
    expect(screen.getByText('SHADOW')).toHaveClass('plain')
    expect(screen.queryByText('ЗАПИС')).toBeNull()
  })

  it('shows «ЗАПИС» once the IoT2050 writes, though the mode still reads shadow', () => {
    render(<EdgeStatusChips status={status(true)} mode="control" />)
    expect(screen.getByText('ЗАПИС')).toHaveClass('warn')
    expect(screen.queryByText('SHADOW')).toBeNull()
  })
})
