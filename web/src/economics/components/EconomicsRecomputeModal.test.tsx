import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

// The modal auto-detects a station's telemetry span via the api module,
// so we mock both api calls it uses and exercise the auto-fill behaviour.
vi.mock('../../api', () => ({
  fetchEconomicsDataRange: vi.fn(),
  recomputeEconomics: vi.fn(),
}))

import { fetchEconomicsDataRange } from '../../api'
import { EconomicsRecomputeModal } from './EconomicsRecomputeModal'

const mockedRange = vi.mocked(fetchEconomicsDataRange)

afterEach(() => {
  vi.clearAllMocks()
  cleanup()
})

function renderModal() {
  return render(
    <EconomicsRecomputeModal
      onClose={() => {}}
      organizationOptions={['org-a', 'org-b']}
      initialOrganizationID="org-a"
    />,
  )
}

describe('EconomicsRecomputeModal', () => {
  it('auto-fills and locks the date range from the detected period', async () => {
    mockedRange.mockResolvedValue({ from: '2026-01-15', to: '2026-07-05', has_data: true })
    renderModal()

    await waitFor(() =>
      expect(screen.getByText(/Автовизначений період: 2026-01-15 → 2026-07-05/)).toBeInTheDocument(),
    )

    const from = screen.getByLabelText('Від') as HTMLInputElement
    const to = screen.getByLabelText('До (включно)') as HTMLInputElement
    expect(from.value).toBe('2026-01-15')
    expect(to.value).toBe('2026-07-05')
    expect(from.disabled).toBe(true)
    expect(to.disabled).toBe(true)
  })

  it('re-enables manual date entry when the full-period toggle is off', async () => {
    mockedRange.mockResolvedValue({ from: '2026-01-15', to: '2026-07-05', has_data: true })
    renderModal()

    const toggle = screen.getByRole('checkbox') as HTMLInputElement
    await waitFor(() => expect(toggle.checked).toBe(true))

    fireEvent.click(toggle)

    const from = screen.getByLabelText('Від') as HTMLInputElement
    expect(from.disabled).toBe(false)
  })

  it('reports missing telemetry and blocks the run', async () => {
    mockedRange.mockResolvedValue({ from: '', to: '', has_data: false })
    renderModal()

    await waitFor(() =>
      expect(screen.getByText(/Немає телеметрії для цього об/)).toBeInTheDocument(),
    )
    expect((screen.getByText('Перерахувати') as HTMLButtonElement).closest('button')?.disabled).toBe(
      true,
    )
  })
})
