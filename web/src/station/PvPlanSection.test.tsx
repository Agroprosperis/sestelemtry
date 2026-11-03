import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { PvPlanSection } from './PvPlanSection'
import type { PvStation, PvStationResponse } from './pvPlanClient'

const client = vi.hoisted(() => ({
  fetchPvStation: vi.fn(),
  fetchPvStationHistory: vi.fn(),
  fetchPvPlanHistoryStatus: vi.fn(),
  savePvStation: vi.fn(),
  startPvPlanHistory: vi.fn(),
}))

vi.mock('./pvPlanClient', () => client)

const station: PvStation = {
  organization_id: 'ab',
  source: 'n8n',
  latitude: 49.0406538,
  longitude: 27.7419296,
  pac_limit_kw: 450,
  gamma_pct_per_c: -0.29,
  orientations: [
    { idx: 1, tilt_deg: 30, azimuth_deg: 0, modules_count: 549, module_power_w: 590, enabled: true },
    { idx: 2, tilt_deg: 3.6, azimuth_deg: -92.5, modules_count: 54, module_power_w: 590, enabled: true },
  ],
}

function response(st: PvStation | null, extra: Partial<PvStationResponse> = {}): PvStationResponse {
  return {
    organization_id: 'ab',
    station: st,
    defaults: { latitude: 49.04, longitude: 27.74 },
    pdc_total_kwp: 355.77,
    n8n_available: true,
    today: { date: '2026-11-03', model_kwh: 2310, n8n_kwh: 2050 },
    history_job: { running: false, done: 0, total: 0, days: 0 },
    ...extra,
  }
}

beforeEach(() => {
  vi.clearAllMocks()
  client.fetchPvStationHistory.mockResolvedValue([])
})

describe('PvPlanSection', () => {
  it("shows both sources with today's plan of each", async () => {
    client.fetchPvStation.mockResolvedValue(response(station))
    render(<PvPlanSection organizationID="ab" canEdit ratedKw={null} />)

    // The shared formatter follows the browser's locale for the decimal mark.
    expect(await screen.findByText(/власна модель 2[.,]31 МВт·год · n8n 2[.,]05 МВт·год/)).toBeInTheDocument()
    expect(screen.getByRole('radio', { name: /n8n — як раніше/ })).toBeChecked()
    expect(screen.getByText('355,77 кВт·пік')).toBeInTheDocument()
  })

  // The switch the operator flips per station: n8n → the model.
  it('switches a station to the model on save', async () => {
    client.fetchPvStation.mockResolvedValue(response(station))
    client.savePvStation.mockImplementation(async (_org: string, st: PvStation) => response(st))
    render(<PvPlanSection organizationID="ab" canEdit ratedKw={null} />)

    fireEvent.click(await screen.findByRole('radio', { name: /Власна модель/ }))
    expect(screen.getByText(/зміниться на «власна модель» після збереження/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Зберегти' }))

    await waitFor(() => expect(client.savePvStation).toHaveBeenCalledTimes(1))
    const [org, saved] = client.savePvStation.mock.calls[0] as [string, PvStation]
    expect(org).toBe('ab')
    expect(saved.source).toBe('model')
    expect(saved.orientations).toHaveLength(2)
    expect(await screen.findByText(/Збережено/)).toBeInTheDocument()
  })

  it('keeps the model unavailable until the passport can run it', async () => {
    client.fetchPvStation.mockResolvedValue(response(null))
    render(<PvPlanSection organizationID="ab" canEdit ratedKw={null} />)
    expect(await screen.findByRole('radio', { name: /Власна модель/ })).toBeDisabled()
    expect(screen.getByText('Орієнтацій ще немає.')).toBeInTheDocument()
  })

  it('is read-only without technical.write', async () => {
    client.fetchPvStation.mockResolvedValue(response(station))
    render(<PvPlanSection organizationID="ab" canEdit={false} ratedKw={null} />)
    await screen.findByText(/План на сьогодні/)
    expect(screen.queryByRole('button', { name: 'Зберегти' })).toBeNull()
    expect(screen.getByRole('radio', { name: /Власна модель/ })).toBeDisabled()
    expect(screen.getByLabelText('Нахил орієнтації 1')).toBeDisabled()
  })

  it('warns when the planes stray from the FusionSolar nameplate', async () => {
    client.fetchPvStation.mockResolvedValue(response(station))
    render(<PvPlanSection organizationID="ab" canEdit ratedKw={461.97} />)
    expect(await screen.findByText(/розходиться з номіналом FusionSolar на 23 %/)).toBeInTheDocument()
  })

  it('starts a plan-history rebuild', async () => {
    client.fetchPvStation.mockResolvedValue(response({ ...station, source: 'model' }))
    client.startPvPlanHistory.mockResolvedValue({ running: true, done: 1, total: 4, days: 0, from: '2026-05-01', to: '2026-11-02' })
    render(<PvPlanSection organizationID="ab" canEdit ratedKw={null} />)

    fireEvent.click(await screen.findByRole('button', { name: 'Перерахувати історію плану' }))
    expect(await screen.findByText(/Перераховується 2026-05-01 — 2026-11-02: 25 %/)).toBeInTheDocument()
    expect(client.startPvPlanHistory).toHaveBeenCalledWith('ab')
  })
})
