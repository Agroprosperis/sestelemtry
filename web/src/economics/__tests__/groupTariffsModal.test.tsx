import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { GroupTariffsModal } from '../components/EconomicsTariffsModal'
import { fetchOrgTariffs } from '../orgTariffsClient'
import { DEFAULT_TARIFFS } from '../tariffs'

vi.mock('../orgTariffsClient', () => ({
  fetchOrgTariffs: vi.fn(async (org: string) => ({ ...DEFAULT_TARIFFS, essCapacityKwh: org === 'de' ? 516 : 344 })),
  saveOrgTariffs: vi.fn(async () => {}),
  fetchTariffSchedule: vi.fn(async () => []),
  saveTariffScheduleVersion: vi.fn(async () => {}),
  deleteTariffScheduleVersion: vi.fn(async () => {}),
}))

// The elevator's tariffs live on its sites: the dialog edits one site at a
// time and never the group itself.
describe('GroupTariffsModal', () => {
  it('switches the form between the members of the group', async () => {
    render(<GroupTariffsModal members={['de', 'de1']} date="2026-11-09" onClose={() => {}} />)
    const capacity = () => screen.getByLabelText(/Ємність УЗЕ/) as HTMLInputElement

    await waitFor(() => expect(capacity().value).toBe('516'))
    expect(screen.getByRole('button', { name: "Дубов'язівський Т2" })).toHaveAttribute('aria-pressed', 'true')

    fireEvent.click(screen.getByRole('button', { name: "Дубов'язівський Т1" }))
    await waitFor(() => expect(capacity().value).toBe('344'))
    expect(vi.mocked(fetchOrgTariffs).mock.calls.map((c) => c[0])).toEqual(['de', 'de1'])
  })
})
