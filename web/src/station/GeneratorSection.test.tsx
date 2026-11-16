import { fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { GeneratorPassport } from '../api'
import { GeneratorSection } from './GeneratorSection'

const navigate = vi.fn()
vi.mock('../shell/navigation', () => ({ navigateEconomicsTariffs: (org: string) => navigate(org) }))

function stubApis(passport: Partial<GeneratorPassport>) {
  const fetchMock = vi.fn(async (url: string) => {
    const u = String(url)
    if (u.includes('/api/v1/generator')) {
      return new Response(JSON.stringify({ organization_id: 'ke', has_generator: true, ...passport }), { status: 200 })
    }
    if (u.includes('organization-tariff-schedule')) {
      return new Response(
        JSON.stringify({
          versions: [
            { effective_from: '1970-01-01', tariffs: { include_vat: false, vat_rate: 0.2 } },
            {
              effective_from: '2026-11-15',
              tariffs: {
                include_vat: false,
                vat_rate: 0.2,
                gen_fuel_price_uah_per_l: 82.5,
                gen_fuel_method: 'per_kwh',
                gen_fuel_consumption_l_per_kwh: 0.3,
              },
            },
            { effective_from: '2999-01-01', tariffs: { include_vat: false, vat_rate: 0.2, gen_fuel_price_uah_per_l: 1 } },
          ],
        }),
        { status: 200 },
      )
    }
    return new Response('{}', { status: 200 })
  })
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

describe('GeneratorSection', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.useRealTimers()
    navigate.mockReset()
  })

  it('shows the rating, the fuel in force today, the meter and the Encombi link', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2026-11-16T10:00:00Z'))
    stubApis({
      device_host: '10.24.40.239',
      last_sample_at: '2026-11-16T09:59:55Z',
      rated_kw: 100,
      meter_kwh: 1234.5,
      meter_at: '2026-11-16T09:59:55Z',
      meter_alive: true,
      sw_version: 1805,
      last_run_at: '2026-11-15T18:40:00Z',
    })
    render(<GeneratorSection organizationID="ke" canSeeFuel />)

    expect(await screen.findByText('Генератор')).toBeInTheDocument()
    expect(screen.getByText('100 кВт')).toBeInTheDocument()
    expect(await screen.findByText('24,75 грн/кВт·год')).toBeInTheDocument()
    expect(screen.getByText('82,5 грн/л без ПДВ')).toBeInTheDocument()
    expect(screen.getByText('0,3 л/кВт·год')).toBeInTheDocument()
    expect(screen.getByText(/1\s?234,5 кВт·год/)).toBeInTheDocument()
    expect(screen.getByText('На зв’язку')).toBeInTheDocument()
    expect(screen.getByText('10.24.40.239')).toBeInTheDocument()
    expect(screen.getByText('1805')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Розклад тарифів' }))
    expect(navigate).toHaveBeenCalledWith('ke')
  })

  it('hides the fuel without economics access and never asks for the tariffs', async () => {
    const fetchMock = stubApis({ last_sample_at: '2026-01-01T00:00:00Z' })
    render(<GeneratorSection organizationID="ke" canSeeFuel={false} />)

    expect(await screen.findByText('Генератор')).toBeInTheDocument()
    expect(screen.queryByText('Пальне')).toBeNull()
    expect(screen.getByText('Ще не працював')).toBeInTheDocument()
    expect(screen.getByText('Немає зв’язку')).toBeInTheDocument()
    expect(fetchMock.mock.calls.some(([u]) => String(u).includes('tariff'))).toBe(false)
  })

  it('renders nothing for a site without a generator', async () => {
    const fetchMock = stubApis({ has_generator: false })
    const { container } = render(<GeneratorSection organizationID="pe" canSeeFuel />)
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalled())
    expect(container).toBeEmptyDOMElement()
  })
})
