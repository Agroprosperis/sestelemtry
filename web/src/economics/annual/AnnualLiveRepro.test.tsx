import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import fixture from './__fixtures_annual_ze.json'
import type { EconomicsAnnualResponse } from '../../api'
import { EconomicsAnnualView } from './EconomicsAnnualView'

// Repro: live ze annual payload (2026-09-17) that the user reports as
// "no data shown" — render must not throw.
describe('EconomicsAnnualView live ze payload', () => {
  it('renders without crashing', () => {
    const { container } = render(
      <EconomicsAnnualView
        data={fixture as unknown as EconomicsAnnualResponse}
        organizationID="ze"
        onSelectMonth={() => {}}
      />,
    )
    expect(container.textContent).toBeTruthy()
  })
})
