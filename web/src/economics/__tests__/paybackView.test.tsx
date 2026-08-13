import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import type { EconomicsAnnualResponse, EconomicsMonthlyTotals } from '../../api'
import { EconomicsPaybackView } from '../payback/EconomicsPaybackView'

const M = 1_000_000

function totals(ebitdaUah: number): EconomicsMonthlyTotals {
  return {
    ebitda_uah: ebitdaUah,
    expense_total_uah: 1.2 * M,
    hours_with_data: 24 * 30,
    pv_kwh: 120_000,
    avg_import_price_uah_per_kwh: 5.4,
  } as unknown as EconomicsMonthlyTotals
}

const months = ['2026-01', '2026-02', '2026-03', '2026-04', '2026-05', '2026-06'].map((month) => ({
  month,
  totals: totals(M),
}))

const data = {
  organization_id: 'preview',
  period: '',
  from: '2026-01',
  to: '2026-06',
  tz: 'Europe/Kyiv',
  months_with_data: months.length,
  prior_ebitda_uah: 0,
  prior_months_with_data: 0,
  totals: totals(6 * M),
  months,
  quarters: [],
  monthly_margin: [],
} as unknown as EconomicsAnnualResponse

describe('EconomicsPaybackView', () => {
  it('reports the investment reached so far when CAPEX came in stages', () => {
    const { container } = render(
      <EconomicsPaybackView
        data={data}
        capexUah={0}
        capexSteps={[
          { effectiveFrom: '2026-01-01', capexUah: 12 * M },
          { effectiveFrom: '2026-06-07', capexUah: 20 * M },
        ]}
        plannedPaybackMonths={0}
      />,
    )

    expect(screen.getByText('2 етапи інвестицій')).toBeTruthy()
    // Progress is measured against the enlarged investment, not the first
    // stage the project had already covered.
    expect(container.textContent).toContain('Повернуто 6,00 млн грн із 20,00 млн грн')
  })

  it('falls back to the flat CAPEX from the tariff form', () => {
    const { container } = render(
      <EconomicsPaybackView data={data} capexUah={20 * M} capexSteps={[]} plannedPaybackMonths={0} />,
    )

    expect(screen.getByText('разові інвестиції')).toBeTruthy()
    expect(container.textContent).toContain('Повернуто 6,00 млн грн із 20,00 млн грн')
  })
})
