import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { EdgeHealth, EdgeSiteStatus } from './controlClient'
import { REASON_LABELS, StateTab } from './StateTab'

// The tab embeds the dashboard's live cards and chart — stub them, the
// subject here is the diagnostics layer (план·shadow·факт, УЗЕ, checks,
// інвертори) of the spec §8.2.
vi.mock('../dashboard/hooks/useDashboardData', () => ({
  useDashboardData: () => ({
    config: { energy_chart: [] },
    liveAllocation: null,
    energySeries: [],
    energySummary: null,
    damSeries: [],
    socSeries: [],
    powerSeries: [],
    pvForecastSeries: [],
    loading: false,
    cardsLoading: false,
    error: null,
  }),
}))
vi.mock('../dashboard/components/CurrentSnapshotNarrative', () => ({
  CurrentSnapshotNarrative: () => <div data-testid="stub-narrative" />,
}))
vi.mock('../dashboard/components/EnergyChart', () => ({
  EnergyChart: () => <div data-testid="stub-chart" />,
}))
vi.mock('../dashboard/components/WeatherCard', () => ({
  WeatherCard: () => <div data-testid="stub-weather" />,
}))

function statusWith(health: EdgeHealth | undefined): EdgeSiteStatus {
  return {
    site_id: 'ze',
    heartbeat: {
      online: true,
      age_seconds: 20,
      status: 'shadow',
      buffer_pending: 12,
      firmware: 'dev',
    },
    manifest: { state: 'applied' },
    decision: {
      at: '2026-09-01T12:00:00Z',
      age_seconds: 3,
      record: {
        site_id: 'ze',
        ts: '2026-09-01T12:00:00Z',
        mode: 'shadow',
        preset: 'economic_arbitrage',
        state_machine: 'shadow',
        plan_source: 'manifest',
        reason_code: 'sl_alarm',
        rationale: 'аварія SmartLogger',
        inputs: {
          soc_percent: 60,
          pv_power_kw: 120,
          ess_power_kw: -35,
          grid_power_kw: 15,
          load_power_kw: 100,
          p_bess_plan_kw: 180,
        },
        outputs: {
          p_bess_virtual_kw: 0,
          p_pv_limit_virtual_kw: 450,
          would_write_40381: 0,
          would_write_40378: 450000,
          clamps: ['SOC на мінімумі — розряд заборонено'],
        },
      },
    },
    health,
  }
}

const fullHealth: EdgeHealth = {
  ts: '2026-09-01T12:00:00Z',
  ok: false,
  checks: [
    {
      id: 'sl_alarms',
      ok: false,
      severity: 'alarm',
      label: 'Аварії SmartLogger',
      expected: 'усі слова 0',
      actual: '0x0 0x0010 0x0 0x0 0x0 0x0',
      detail: 'dispatch заблоковано (sl_alarm)',
    },
    {
      id: 'shadow_vs_fact',
      ok: true,
      severity: 'info',
      label: 'Shadow vs факт УЗЕ',
      expected: '0.0 кВт',
      actual: '-35.0 кВт',
    },
  ],
  bess: {
    class: 'charging',
    class_label: 'заряд',
    soc_percent: 60,
    soh_percent: 99.5,
    soe_percent: 58,
    soc_min_pct: 20,
    soc_max_pct: 90,
    p_kw: -35,
    q_kvar: 1.2,
    p_plan_kw: 180,
    p_shadow_kw: 0,
    clamps: [],
    charge_max_kw: 864,
    discharge_max_kw: 864,
    chargeable_kwh: 700,
    dischargeable_kwh: 900,
    rated_kw: 864,
    rated_kwh: 1720,
    passport_kw: 864,
    passport_kwh: 1720,
    passport_ess_count: 8,
    n_ess: 8,
    n_pcs: 8,
    pcs_in_operation: 8,
    pcs_shutdown: 0,
    pcs_label: 'в роботі',
    charged_kwh: 100000,
    discharged_kwh: 90000,
    poll_ok: true,
    poll_error: null,
    ts: '2026-09-01T12:00:00Z',
  },
  inverters: [
    {
      device_address: 12,
      register_base: 51275,
      label: 'INV-12',
      class: 'on_grid',
      status_raw: '0x0200',
      status_label: 'у мережі',
      p_kw: 41.2,
      q_kvar: 0.4,
      p_dc_kw: 42,
      i_dc_a: 18.4,
      pf: 0.99,
      insulation_mohm: 12.5,
      temp_c: 38.1,
      major_fault: '0x0',
      minor_fault: '0x0',
      warning: '0x0',
      poll_ok: true,
      poll_error: null,
      ts: '2026-09-01T12:00:00Z',
    },
    {
      device_address: 13,
      register_base: 51300,
      class: 'unreachable',
      status_label: "без зв'язку",
      p_kw: null,
      q_kvar: null,
      p_dc_kw: null,
      i_dc_a: null,
      pf: null,
      insulation_mohm: null,
      temp_c: null,
      poll_ok: false,
      poll_error: 'dial tcp: timeout',
      ts: '2026-09-01T12:00:00Z',
    },
  ],
  alarms: { words: ['0x0', '0x0010', '0x0', '0x0', '0x0', '0x0'] },
}

describe('StateTab diagnostics panels', () => {
  it('renders BESS card, checks table and inverter fleet from health', () => {
    render(<StateTab site="ze" status={statusWith(fullHealth)} />)

    expect(screen.getByText('УЗЕ (BESS)')).toBeInTheDocument()
    expect(screen.getByText('заряд')).toBeInTheDocument()

    expect(screen.getByText('Діагностика: очікувано vs факт')).toBeInTheDocument()
    expect(screen.getByText('Аварії SmartLogger')).toBeInTheDocument()
    expect(screen.getByText('dispatch заблоковано (sl_alarm)')).toBeInTheDocument()

    expect(screen.getByText('Інвертори')).toBeInTheDocument()
    expect(screen.getByText('2 інверторів')).toBeInTheDocument()
    expect(screen.getByText('1 у мережі')).toBeInTheDocument()
    expect(screen.getByText("1 без зв'язку")).toBeInTheDocument()
    expect(screen.getByText('INV-12')).toBeInTheDocument()

    // Clamp from the decision record surfaces in план·shadow·факт.
    expect(screen.getByText('SOC на мінімумі — розряд заборонено')).toBeInTheDocument()
    // sl_alarm gets its human label.
    expect(screen.getByText(REASON_LABELS.sl_alarm)).toBeInTheDocument()
  })

  it('hides diagnostics panels gracefully when health is absent', () => {
    render(<StateTab site="ze" status={statusWith(undefined)} />)

    expect(screen.queryByText('УЗЕ (BESS)')).not.toBeInTheDocument()
    expect(screen.queryByText('Діагностика: очікувано vs факт')).not.toBeInTheDocument()
    expect(screen.queryByText('Інвертори')).not.toBeInTheDocument()
    // The core план·shadow·факт card still renders from the decision.
    expect(screen.getByText('План · shadow · факт')).toBeInTheDocument()
  })

  it('hides fleet panel when health carries no inverters (poll disabled)', () => {
    const noFleet: EdgeHealth = { ...fullHealth, inverters: undefined }
    render(<StateTab site="ze" status={statusWith(noFleet)} />)

    expect(screen.getByText('УЗЕ (BESS)')).toBeInTheDocument()
    expect(screen.queryByText('Інвертори')).not.toBeInTheDocument()
  })
})
