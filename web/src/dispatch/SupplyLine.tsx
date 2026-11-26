// SupplyLine — the line over the desk (offgrid §4.3): what feeds the
// site, where the cabinet switch is, the battery's mode, who drives the
// loggers and why an action is closed. It comes from the IoT2050; with
// no link to it the line says so instead of showing the last state.

import type { EdgeSiteStatus, EdgeSupply } from '../control/controlClient'
import { writeMode } from '../control/writeState'

type Sev = 'ok' | 'warn' | 'err' | 'plain'

const MODE_SEV: Record<string, Sev> = {
  grid: 'ok',
  generator: 'warn',
  island: 'warn',
  blackout: 'err',
  transition: 'err',
  unknown: 'err',
}

function clockOf(iso: string | undefined, timeZone: string): string {
  if (!iso) return ''
  return new Intl.DateTimeFormat('uk-UA', { hour: '2-digit', minute: '2-digit', hourCycle: 'h23', timeZone }).format(new Date(iso))
}

function Field({ label, value, sev = 'plain' }: { label: string; value: string; sev?: Sev }) {
  return (
    <span className="d-supply-f">
      <span>{label}</span>
      <strong className={sev}>{value}</strong>
    </span>
  )
}

const kw = (v: number | undefined) => (v === undefined ? '—' : Math.round(v).toLocaleString('uk-UA') + ' кВт')

function duration(min: number | undefined): string {
  if (min === undefined) return '—'
  return Math.floor(min / 60) + ' год ' + (min % 60) + ' хв'
}

function OffGrid({ sp }: { sp: EdgeSupply }) {
  const gen = sp.mode === 'generator'
  const island = sp.mode === 'island' || (sp.mode === 'blackout' && sp.switch === 'vsg')
  if (!gen && !island) return null
  const load = sp.gen_load_pct ?? 0
  return (
    <div className="d-supply-extra">
      {gen && (
        <span className="d-supply-gen" aria-label="Навантаження генератора">
          Генератор {kw(sp.gen_power_kw)} · {sp.gen_load_pct ?? '—'} %
          <span className="d-gen-bar">
            <span
              className="d-gen-band"
              style={{ left: sp.gen_best_pct[0] + '%', width: sp.gen_best_pct[1] - sp.gen_best_pct[0] + '%' }}
            />
            <span className="d-gen-fill" style={{ width: Math.max(0, Math.min(100, load)) + '%' }} />
            <span className="d-gen-min" style={{ left: sp.gen_min_pct + '%' }} />
          </span>
          <span className="d-small">
            мінімум {sp.gen_min_pct} % · найдешевше {sp.gen_best_pct[0]}–{sp.gen_best_pct[1]} %
          </span>
        </span>
      )}
      {island && sp.island_power_max_kw !== undefined && (
        <span>Доступна потужність острова: не більше {kw(sp.island_power_max_kw)}</span>
      )}
      {(gen || sp.mode === 'island') && (
        <span>
          Заряд УЗЕ {sp.soc_percent === undefined ? '—' : Math.round(sp.soc_percent) + ' %'} · до {sp.soc_floor_pct} %:{' '}
          {duration(sp.autonomy_min)} при {kw(sp.load_kw)}
        </span>
      )}
    </div>
  )
}

export function SupplyLine({ status, timeZone }: { status: EdgeSiteStatus | null | undefined; timeZone: string }) {
  if (!status) return null
  if (!status.heartbeat.online) {
    const last = status.heartbeat.updated_at
    return (
      <div className="d-supply err" role="status" aria-label="Стан живлення">
        <strong>Немає звʼязку з IoT2050</strong>
        <span>{last ? 'останній звʼязок о ' + clockOf(last, timeZone) : 'звʼязку ще не було'}</span>
        <span>стан живлення й обладнання невідомий</span>
      </div>
    )
  }
  const sp = status.health?.supply
  if (!sp) {
    return (
      <div className="d-supply plain" aria-label="Стан живлення">
        <Field label="Живлення" value="—" />
        <Field label="Хто керує" value={writeMode(status) === 'writing' ? 'EMS (запис увімкнено)' : 'Encombi'} />
        <span className="d-supply-note">IoT2050 не читає Encombi: режим живлення невідомий</span>
      </div>
    )
  }
  return (
    <div className={'d-supply ' + (MODE_SEV[sp.mode] ?? 'plain')} aria-label="Стан живлення">
      <div className="d-supply-row">
        <Field
          label="Живлення"
          value={sp.mode_label + (sp.mode_since ? ' з ' + clockOf(sp.mode_since, timeZone) : '')}
          sev={MODE_SEV[sp.mode]}
        />
        <Field label="Перемикач на шафі" value={sp.switch_label} sev={sp.switch === 'unknown' ? 'err' : 'plain'} />
        <Field label="Режим УЗЕ" value={sp.ess_mode_label} sev={sp.ess_mode === 'vsg' || sp.ess_mode === 'mixed' ? 'warn' : 'plain'} />
        <Field label="Хто керує" value={sp.controller_label} sev={sp.controller === 'none' ? 'err' : 'plain'} />
      </div>
      {sp.restriction && (
        <p className="d-supply-restr" role="status">
          Обмеження: {sp.restriction}
        </p>
      )}
      <OffGrid sp={sp} />
    </div>
  )
}