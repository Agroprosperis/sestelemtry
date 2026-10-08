import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { EdgeSiteStatus } from './controlClient'
import { ModesTab } from './ModesTab'

function statusWith(preset: string, reason: string): EdgeSiteStatus {
  return {
    manifest: { state: 'applied', payload: { preset, note: 'dispatch v3', manifest_id: 'ze-x', plan: { granularity: '1h', intervals: [] } } },
    decision: { at: '', age_seconds: 5, record: { preset, reason_code: reason } },
  } as unknown as EdgeSiteStatus
}

const activeTitle = () => screen.getByText('зараз').closest('.ctl-mode-card')?.querySelector('h3')?.firstChild?.textContent

describe('ModesTab', () => {
  it('marks the desk plan while the edge follows an interval', () => {
    render(<ModesTab status={statusWith('economic_arbitrage', 'plan_discharge')} onOpenDesk={() => {}} />)
    expect(activeTitle()).toBe('План пульта')
    expect(screen.getByText('dispatch v3')).toBeInTheDocument()
  })

  it('marks self-consumption for an hour without a plan', () => {
    render(<ModesTab status={statusWith('economic_arbitrage', 'no_plan_self_consumption')} onOpenDesk={() => {}} />)
    expect(activeTitle()).toBe('Самоспоживання')
  })

  it('opens the desk', () => {
    const open = vi.fn()
    render(<ModesTab status={statusWith('self_consumption_safe', 'self_consumption')} onOpenDesk={open} />)
    expect(activeTitle()).toBe('Безпечний')
    fireEvent.click(screen.getByRole('button', { name: 'Відкрити пульт «План УЗЕ»' }))
    expect(open).toHaveBeenCalled()
  })
})
