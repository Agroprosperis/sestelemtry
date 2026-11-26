import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { EdgeSiteStatus, EdgeSupply } from '../control/controlClient'
import { CommandFields } from './CommandEditor'
import type { SiteInfo } from './dispatchClient'
import { SupplyLine } from './SupplyLine'
import { closedScenarios } from './supply'

const supply = (over: Partial<EdgeSupply> = {}): EdgeSupply => ({
  mode: 'grid',
  mode_label: 'Мережа',
  mode_since: '2026-11-26T08:00:00Z',
  switch: 'pq',
  switch_label: 'PQ',
  ess_mode: 'pq',
  ess_mode_label: 'PQ',
  controller: 'encombi',
  controller_label: 'Encombi',
  gen_min_pct: 30,
  gen_best_pct: [60, 70],
  soc_floor_pct: 20,
  island_power_max_kw: 576,
  ...over,
})

const status = (online: boolean, sp?: EdgeSupply, writing = false): EdgeSiteStatus =>
  ({
    site_id: 'ze',
    heartbeat: { online, buffer_pending: 0, updated_at: '2026-11-26T09:41:00Z' },
    manifest: { state: 'applied' },
    health: { ts: '', ok: true, checks: [], supply: sp, write: { enabled: writing, writer: 'other', state: 'off', gate: {} } },
  }) as unknown as EdgeSiteStatus

describe('SupplyLine', () => {
  it('says there is no link rather than the last state', () => {
    render(<SupplyLine status={status(false, supply())} timeZone="Europe/Kyiv" />)
    expect(screen.getByText('Немає звʼязку з IoT2050')).toBeInTheDocument()
    expect(screen.getByText('останній звʼязок о 11:41')).toBeInTheDocument()
    expect(screen.queryByText('Мережа з 10:00')).toBeNull()
  })

  it('shows the status line from the IoT2050', () => {
    render(<SupplyLine status={status(true, supply())} timeZone="Europe/Kyiv" />)
    const line = screen.getByLabelText('Стан живлення')
    expect(within(line).getByText('Мережа з 10:00')).toHaveClass('ok')
    expect(within(line).getByText('Encombi')).toBeInTheDocument()
    expect(within(line).queryByText(/Обмеження/)).toBeNull()
  })

  it('without the Encombi read: the mode is unknown, who drives is the write flag', () => {
    render(<SupplyLine status={status(true, undefined, true)} timeZone="Europe/Kyiv" />)
    expect(screen.getByText('EMS (запис увімкнено)')).toBeInTheDocument()
    expect(screen.getByText(/не читає Encombi/)).toBeInTheDocument()
  })

  it('with the generator: its load against the band, the charge and the time left', () => {
    const sp = supply({
      mode: 'generator',
      mode_label: 'Генератор',
      gen_power_kw: 300,
      gen_load_pct: 41,
      soc_percent: 50,
      autonomy_min: 155,
      load_kw: 250,
      restriction: 'робота від генератора',
    })
    render(<SupplyLine status={status(true, sp)} timeZone="Europe/Kyiv" />)
    expect(screen.getByText('Обмеження: робота від генератора')).toBeInTheDocument()
    expect(screen.getByLabelText('Навантаження генератора')).toHaveTextContent('Генератор 300 кВт · 41 %')
    expect(screen.getByText(/мінімум 30 % · найдешевше 60–70 %/)).toBeInTheDocument()
    expect(screen.getByText(/до 20 %: 2 год 35 хв при 250 кВт/)).toBeInTheDocument()
  })
})

describe('closed commands', () => {
  const sp = supply({
    mode: 'generator',
    actions: {
      power: { ok: false, reason: 'робота від генератора' },
      plan: { ok: false, reason: 'робота від генератора' },
      manual: {
        hold: { ok: true },
        target: { ok: true },
        fixed: { ok: false, reason: 'робота від генератора' },
        cover: { ok: false, reason: 'робота від генератора' },
      },
      buttons: {},
    },
  })

  it('maps the edge actions onto the desk, not when the link is down', () => {
    expect(closedScenarios(status(true, sp))).toEqual({ fixed: 'робота від генератора', cover: 'робота від генератора' })
    expect(closedScenarios(status(false, sp))).toEqual({})
  })

  it('greys the closed ones with why and warns on the chosen one', () => {
    const site = { soc_max_pct: 90, soc_min_pct: 20, import_kw: 1700, charge_kw: 864, discharge_kw: 864 } as SiteInfo
    const noop = () => {}
    render(
      <CommandFields
        site={site}
        exportCapKw={850}
        scenario="fixed"
        onScenario={noop}
        param="200"
        onParam={noop}
        reserve="20"
        onReserve={noop}
        direction="discharge"
        onDirection={noop}
        closed={closedScenarios(status(true, sp))}
      />,
    )
    const cover = screen.getByRole('option', { name: /Покривати споживання з УЗЕ — недоступно: робота від генератора/ })
    expect(cover).toBeDisabled()
    expect(screen.getByRole('option', { name: 'Пауза · 0 кВт' })).not.toBeDisabled()
    expect(screen.getByText(/Зараз недоступно: робота від генератора/)).toBeInTheDocument()
  })
})
