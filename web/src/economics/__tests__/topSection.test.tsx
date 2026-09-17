import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import type { EconomicsAnnualResponse, EconomicsMonthlyTotals } from '../../api'
import { EconomicsTopSection } from '../components/EconomicsTopSection'
import fixture from '../annual/__fixtures_annual_ze.json'

// The live ze annual payload carries a fully-populated totals object, so
// the cards render from the same shape the real page feeds them.
const data = fixture as unknown as EconomicsAnnualResponse

// A prior period with uniformly smaller volumes, so every delta badge
// has a well-defined denominator and direction.
function priorTotals(): EconomicsMonthlyTotals {
  const t = data.totals
  return {
    ...t,
    load_kwh: t.load_kwh / 2,
    pv_kwh: t.pv_kwh / 2,
    grid_import_kwh: t.grid_import_kwh * 2,
    grid_export_kwh: t.grid_export_kwh / 2,
    pv_to_load_kwh: t.pv_to_load_kwh / 2,
    pv_to_ess_kwh: t.pv_to_ess_kwh / 2,
    ess_to_load_kwh: t.ess_to_load_kwh / 2,
  }
}

describe('EconomicsTopSection', () => {
  it('renders the seven economic cards and six balance cards', () => {
    render(
      <EconomicsTopSection
        totals={data.totals}
        scope="year"
        prior={priorTotals()}
        pvPlan={{ plannedKwh: 550_000, daysCovered: 270, daysExpected: 270 }}
        capexUah={199_000_000}
        annualizeMonths={data.months_with_data}
      />,
    )

    // Row 1: «Економічні показники».
    expect(screen.getByText('Економічні показники')).toBeInTheDocument()
    for (const label of [
      'Базова вартість (без проєкту)',
      'Фактична вартість',
      'Економічний ефект',
      'Ефект СЕС',
      'Ефект УЗЕ',
      'ROCE (річний)',
      'Реалізація потенціалу',
    ]) {
      expect(screen.getByText(label)).toBeInTheDocument()
    }
    expect(screen.getByText(/Інвестований капітал/)).toBeInTheDocument()
    expect(screen.getByText('Факт:')).toBeInTheDocument()
    expect(screen.getByText('Потенціал:')).toBeInTheDocument()
    expect(screen.getByText('Резерв:')).toBeInTheDocument()

    // Row 2: «Енергетичний баланс».
    expect(screen.getByText('Енергетичний баланс')).toBeInTheDocument()
    for (const label of [
      "Споживання об'єкта",
      'Генерація СЕС',
      'Імпорт з мережі',
      'Експорт у мережу',
      'Самозабезпечення',
      'Самоспоживання СЕС',
    ]) {
      expect(screen.getByText(label)).toBeInTheDocument()
    }
    expect(screen.getByText(/План:/)).toBeInTheDocument()
    // Prior-period deltas render on the balance cards.
    expect(screen.getAllByText('до попереднього року').length).toBeGreaterThanOrEqual(4)
    // Grid import doubled vs prior halves in this fixture: the shrink
    // reads as good (green), the label carries the percent value.
    expect(screen.getByText(/-50,0\s?%/)).toBeInTheDocument()
  })

  it('hides deltas, plan line and capital without prior/plan/capex', () => {
    render(<EconomicsTopSection totals={data.totals} scope="year" />)
    expect(screen.queryByText('до попереднього року')).toBeNull()
    expect(screen.queryByText(/План:/)).toBeNull()
    expect(screen.getByText('CAPEX не вказано в тарифах')).toBeInTheDocument()
    // Without capex the ROCE value degrades to the em-dash.
    expect(screen.getByText('ROCE (річний)')).toBeInTheDocument()
  })
})
