import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { useState } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

vi.mock('../../api', () => ({
  fetchEconomicsMonthly: vi.fn(),
}))

import { fetchEconomicsMonthly, type EconomicsMonthlyResponse } from '../../api'
import { DEFAULT_TARIFFS, fuelCostUah, fuelExample, fuelSet, type Tariffs } from '../tariffs'
import { GeneratorFuelFields } from './GeneratorFuelFields'

const mockedMonthly = vi.mocked(fetchEconomicsMonthly)

afterEach(() => {
  vi.clearAllMocks()
  cleanup()
})

function Harness({ initial }: { initial: Tariffs }) {
  const [t, setT] = useState(initial)
  return <GeneratorFuelFields tariffs={t} onChange={(p) => setT({ ...t, ...p })} organizationID="ke" date="2026-11-16" />
}

function monthWithLoad(days: Array<{ date: string; load_kwh: number; hours_with_data: number }>) {
  return { days } as unknown as EconomicsMonthlyResponse
}

describe('fuel tariff helpers', () => {
  it('prices the chosen method with the import VAT', () => {
    const t: Tariffs = { ...DEFAULT_TARIFFS, genFuelPriceUahPerL: 82.5, genFuelConsumptionLPerKwh: 0.3, genFuelConsumptionLPerH: 20 }
    expect(fuelSet(t)).toBe(true)
    expect(fuelCostUah(t)).toBeCloseTo(24.75, 9)
    expect(fuelCostUah({ ...t, includeVat: true, vatRate: 0.2 })).toBeCloseTo(29.7, 9)
    expect(fuelCostUah({ ...t, genFuelMethod: 'per_hour' })).toBeCloseTo(1650, 9)
    expect(fuelSet({ ...t, genFuelMethod: 'per_hour', genFuelConsumptionLPerH: 0 })).toBe(false)
    expect(fuelSet({ ...t, genFuelPriceUahPerL: 0 })).toBe(false)
  })

  it('builds the §4.4.1 example', () => {
    const t: Tariffs = { ...DEFAULT_TARIFFS, genFuelPriceUahPerL: 55, genFuelConsumptionLPerKwh: 0.4, genFuelConsumptionLPerH: 80 }
    expect(fuelExample(t, 200)).toEqual({ generatorOnly: 4400, halfGenerator: 2200, essEffect: 2200 })
    const h = fuelExample({ ...t, genFuelMethod: 'per_hour' }, 200)
    expect(h.generatorOnly).toBeCloseTo(4400, 9)
    expect(h.halfGenerator).toBeCloseTo(2200, 9)
    expect(h.essEffect).toBeCloseTo(2200, 9)
  })
})

describe('GeneratorFuelFields', () => {
  it('shows one consumption for the chosen method and the cost it gives', async () => {
    mockedMonthly.mockResolvedValue(monthWithLoad([]))
    render(<Harness initial={{ ...DEFAULT_TARIFFS, genFuelPriceUahPerL: 82.5, genFuelConsumptionLPerKwh: 0.3, genFuelConsumptionLPerH: 20 }} />)
    expect(screen.getByText('л/кВт·год')).toBeInTheDocument()
    expect(screen.queryByText('л/год')).toBeNull()
    expect(screen.getByText('24,75 грн/кВт·год')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'за годину роботи' }))
    expect(screen.getByText('л/год')).toBeInTheDocument()
    expect(screen.queryByText('л/кВт·год')).toBeNull()
    expect(screen.getByText('1 650,00 грн/год')).toBeInTheDocument()
  })

  it('takes the example load from the last 30 days, rounded to 10 kW', async () => {
    mockedMonthly.mockImplementation(async ({ month }) =>
      month === '2026-11'
        ? monthWithLoad([
            { date: '2026-11-15', load_kwh: 4560, hours_with_data: 24 },
            { date: '2026-11-16', load_kwh: 4560, hours_with_data: 24 },
          ])
        : monthWithLoad([{ date: '2026-10-01', load_kwh: 99999, hours_with_data: 1 }]),
    )
    render(<Harness initial={{ ...DEFAULT_TARIFFS, genFuelPriceUahPerL: 82.5, genFuelConsumptionLPerKwh: 0.3 }} />)
    expect(await screen.findByText(/навантаження 190 кВт/)).toBeInTheDocument()
    expect(screen.getByText(/Лише генератор — 4 703 грн/)).toBeInTheDocument()
  })

  it('falls back to 200 kW and says when the fuel is not priced', async () => {
    mockedMonthly.mockRejectedValue(new Error('offline'))
    render(<Harness initial={DEFAULT_TARIFFS} />)
    expect(screen.getByText('не задана')).toBeInTheDocument()
    expect(screen.getByText(/вартість пального не задана/)).toBeInTheDocument()
    const [price, consumption] = screen.getAllByRole('spinbutton')
    fireEvent.change(price, { target: { value: '50' } })
    fireEvent.change(consumption, { target: { value: '0.4' } })
    expect(screen.getByText(/навантаження 200 кВт/)).toBeInTheDocument()
    expect(screen.getByText(/Лише генератор — 4 000 грн/)).toBeInTheDocument()
  })
})
