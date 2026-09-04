// StateTab — the «Стан» tab of the control mode: the dashboard's live
// flow card and day chart, extended with the manifest layer (план ·
// shadow · факт, BESS card, health checks, inverter fleet — diagnostics
// spec §8.2).

import { useMemo, useState } from 'react'
import { CurrentSnapshotNarrative } from '../dashboard/components/CurrentSnapshotNarrative'
import { EnergyChart } from '../dashboard/components/EnergyChart'
import { WeatherCard } from '../dashboard/components/WeatherCard'
import { useDashboardData } from '../dashboard/hooks/useDashboardData'
import type { BessHealth, EdgeSiteStatus, HealthCheck, InverterHealth } from './controlClient'
import { appliedAnnotation, manifestPlanToUzePlan } from './manifestPlan'

type Props = {
  site: string
  status: EdgeSiteStatus | null
}

const PLAN_SOURCE_LABELS: Record<string, string> = {
  manifest: 'План (manifest)',
  fallback: 'Fallback-пресет',
  config: 'Пресет із конфіга',
  override: 'Локальний override',
}

// Reason codes per diagnostics spec §3.2 — including no_plan_* and
// sl_alarm (обов'язкові підписи в Cloud UI).
export const REASON_LABELS: Record<string, string> = {
  plan_discharge: 'розряд за планом',
  plan_charge: 'заряд за планом',
  plan_hold: 'план: утримання (|план| ≤ 2 кВт)',
  no_plan_self_discharge: 'без плану на зараз — розряд у дефіцит',
  no_plan_self_charge: 'без плану на зараз — заряд від СЕС',
  no_plan_hold: 'без плану на зараз — утримання',
  self_charge: 'заряд від надлишку СЕС',
  self_discharge: 'розряд у локальний дефіцит',
  hold: 'утримання',
  sl_alarm: 'аварія SmartLogger — команда 0',
  data_fault: 'неповні дані — команда 0',
  pcs_shutdown: 'PCS вимкнено — команда 0',
  insufficient_data: 'недостатньо даних',
}

function fmtKw(v: number | null | undefined): string {
  if (v == null || !Number.isFinite(v)) return '—'
  const sign = v > 0 ? '+' : ''
  return `${sign}${v.toFixed(1)} кВт`
}

function fmtNum(v: number | null | undefined, digits = 1, unit = ''): string {
  if (v == null || !Number.isFinite(v)) return '—'
  return `${v.toFixed(digits)}${unit}`
}

function fmtAge(seconds: number | undefined): string {
  if (seconds == null) return 'ніколи'
  if (seconds < 90) return `${Math.max(0, Math.round(seconds))} с тому`
  if (seconds < 5400) return `${Math.round(seconds / 60)} хв тому`
  return `${(seconds / 3600).toFixed(1)} год тому`
}

export function StateTab({ site, status }: Props) {
  const anchor = useMemo(() => new Date(), [])
  const {
    config,
    liveAllocation,
    energySeries,
    energySummary,
    damSeries,
    socSeries,
    powerSeries,
    pvForecastSeries,
    loading,
    cardsLoading,
    error,
  } = useDashboardData({ organizationID: site, preset: 'day', anchor, metricsAt: null })

  const payload = status?.manifest?.payload
  const manifestPlan = useMemo(() => manifestPlanToUzePlan(payload, anchor), [payload, anchor])
  const annotation = useMemo(
    () => appliedAnnotation(status?.manifest?.applied_at, anchor),
    [status?.manifest?.applied_at, anchor],
  )

  const decision = status?.decision?.record
  const virtualKw = decision?.outputs?.p_bess_virtual_kw
  const planKw = decision?.inputs?.p_bess_plan_kw
  const factKw = decision?.inputs?.ess_power_kw
  const clamps = decision?.outputs?.clamps ?? []

  const hb = status?.heartbeat
  const health = status?.health

  return (
    <div className="ctl-state-stack">
    <div className="ctl-state-grid">
      <div className="ctl-state-left">
        <div className="ctl-shadow-note">
          SHADOW: команди віртуальні — запис у SmartLogger вимкнено, керує Encombi.
        </div>

        <CurrentSnapshotNarrative
          liveAllocation={liveAllocation}
          loading={cardsLoading}
          debug={false}
          registers={null}
        />

        <section className="ctl-card">
          <h2>План · shadow · факт</h2>
          <div className="ctl-psf">
            <div className="ctl-psf-cell">
              <span className="k">План</span>
              <span className={'v' + ((planKw ?? 0) > 0 ? ' pos' : (planKw ?? 0) < 0 ? ' neg' : '')}>
                {fmtKw(planKw)}
              </span>
              <span className="sub">інтервал manifest</span>
            </div>
            <div className="ctl-psf-cell">
              <span className="k">Shadow</span>
              <span
                className={'v' + ((virtualKw ?? 0) > 0 ? ' pos' : (virtualKw ?? 0) < 0 ? ' neg' : '')}
              >
                {fmtKw(virtualKw)}
              </span>
              <span className="sub">would_write_40381 · віртуально</span>
            </div>
            <div className="ctl-psf-cell">
              <span className="k">Факт УЗЕ</span>
              <span className={'v' + ((factKw ?? 0) > 0 ? ' pos' : (factKw ?? 0) < 0 ? ' neg' : '')}>
                {fmtKw(factKw)}
              </span>
              <span className="sub">40392 · керує Encombi</span>
            </div>
          </div>
          <div className="ctl-rows">
            <div className="ctl-row">
              <span className="k">PV / load</span>
              <span className="v muted">
                {fmtNum(decision?.inputs?.pv_power_kw, 1, ' кВт')} /{' '}
                {fmtNum(decision?.inputs?.load_power_kw, 1, ' кВт')}
              </span>
            </div>
            <div className="ctl-row">
              <span className="k">Джерело</span>
              <span className="v">
                {decision ? (PLAN_SOURCE_LABELS[decision.plan_source] ?? decision.plan_source) : '—'}
              </span>
            </div>
            <div className="ctl-row">
              <span className="k">Причина</span>
              <span className="v muted">
                {decision?.reason_code
                  ? (REASON_LABELS[decision.reason_code] ?? decision.reason_code)
                  : '—'}
              </span>
            </div>
            {clamps.length > 0 && (
              <div className="ctl-row">
                <span className="k">Обмежено (clamp)</span>
                <span className="v warn">{clamps.join('; ')}</span>
              </div>
            )}
            <div className="ctl-row">
              <span className="k">Рішення</span>
              <span className="v muted">{fmtAge(status?.decision?.age_seconds)}</span>
            </div>
          </div>
        </section>

        {health?.bess && <BessCard bess={health.bess} />}

        <section className="ctl-card">
          <h2>Стан керування</h2>
          <div className="ctl-rows">
            <div className="ctl-row">
              <span className="k">Активний режим</span>
              <span className="v">
                {(payload?.mode ?? decision?.mode ?? '—').toUpperCase()}
                {payload?.source === 'manual' ? ' · РУЧНИЙ' : ''}
              </span>
            </div>
            <div className="ctl-row">
              <span className="k">Пресет</span>
              <span className="v muted">{payload?.preset ?? decision?.preset ?? '—'}</span>
            </div>
            <div className="ctl-row">
              <span className="k">Резерв SOC</span>
              <span className="v">
                {payload?.soc_policy?.min_economic_pct != null
                  ? `${payload.soc_policy.min_economic_pct.toFixed(0)}%`
                  : '—'}
              </span>
            </div>
            <div className="ctl-row">
              <span className="k">Ліміт імпорту</span>
              <span className="v">
                {payload?.grid_limits?.import_limit_kw
                  ? `${payload.grid_limits.import_limit_kw.toFixed(0)} кВт`
                  : 'не задано'}
              </span>
            </div>
            <div className="ctl-row">
              <span className="k">Останній heartbeat</span>
              <span className={'v ' + (hb?.online ? 'ok' : 'err')}>{fmtAge(hb?.age_seconds)}</span>
            </div>
            <div className="ctl-row">
              <span className="k">Черга на пристрої</span>
              <span className={'v ' + ((hb?.buffer_pending ?? 0) > 5000 ? 'warn' : 'muted')}>
                {hb ? `${hb.buffer_pending} записів` : '—'}
              </span>
            </div>
            <div className="ctl-row">
              <span className="k">Версія ПЗ edge</span>
              <span className="v muted">{hb?.firmware || '—'}</span>
            </div>
          </div>
        </section>
      </div>

      <div className="ctl-state-right">
        {error && <div className="ctl-notice err">Не вдалося завантажити дані: {error}</div>}
        <WeatherCard organizationID={site} anchor={anchor} preset="day" />
        <EnergyChart
          metrics={config.energy_chart}
          series={energySeries}
          preset="day"
          summary={energySummary}
          loading={loading}
          damSeries={damSeries}
          socSeries={socSeries}
          powerSeries={powerSeries}
          pvForecastSeries={pvForecastSeries}
          aiPlan={manifestPlan}
          planOverlay={{
            title: 'Energy Trend · план і факт',
            essLabel: 'План УЗЕ (manifest)',
            socLabel: 'SOC за планом',
            defaultVisible: true,
            annotation,
          }}
        />
      </div>
    </div>

    {/* Full-width diagnostics under the grid: checks + inverter fleet.
        No health snapshot → nothing rendered (spec §8.2: не малювати
        «0 інверторів» і порожню УЗЕ як OK). */}
    {health && health.checks && health.checks.length > 0 && (
      <HealthChecksCard checks={health.checks} alarms={health.alarms?.words} />
    )}
    {health?.inverters && health.inverters.length > 0 && (
      <InvertersCard inverters={health.inverters} />
    )}
    </div>
  )
}

// --- УЗЕ (BESS) card, spec §7.2/§7.3 ---

const BESS_CLASS_SEV: Record<string, string> = {
  discharging: 'ok',
  charging: 'ok',
  hold: 'plain',
  shutdown: 'err',
  unreachable: 'err',
  unknown: 'warn',
}

function BessCard({ bess }: { bess: BessHealth }) {
  const socOut =
    bess.soc_percent != null &&
    (bess.soc_percent < bess.soc_min_pct || bess.soc_percent > bess.soc_max_pct)
  return (
    <section className="ctl-card">
      <h2>УЗЕ (BESS)</h2>
      <div className="ctl-bess-head">
        <span className={'ctl-chip ' + (BESS_CLASS_SEV[bess.class] ?? 'plain')}>
          {bess.class_label || bess.class}
        </span>
        <span className={socOut ? 'warn' : ''}>SOC {fmtNum(bess.soc_percent, 1, '%')}</span>
        <span>факт {fmtKw(bess.p_kw)}</span>
        <span>shadow {fmtKw(bess.p_shadow_kw)}</span>
        <span>план {fmtKw(bess.p_plan_kw)}</span>
      </div>
      <div className="ctl-bess-sub">
        PCS: {bess.pcs_label || '—'} · ліміти −{fmtNum(bess.charge_max_kw, 0)} / +
        {fmtNum(bess.discharge_max_kw, 0)} кВт · керує Encombi (shadow)
      </div>
      <details className="ctl-bess-details" open>
        <summary>Деталі УЗЕ</summary>
        <div className="ctl-rows">
          <div className="ctl-row">
            <span className="k">SOC / SOH / SOE</span>
            <span className="v muted">
              {fmtNum(bess.soc_percent, 1, '%')} / {fmtNum(bess.soh_percent, 1, '%')} /{' '}
              {fmtNum(bess.soe_percent, 1, '%')}
            </span>
          </div>
          <div className="ctl-row">
            <span className="k">Робоче вікно SOC</span>
            <span className="v muted">
              {bess.soc_min_pct}–{bess.soc_max_pct}%
            </span>
          </div>
          <div className="ctl-row">
            <span className="k">Факт P / Q</span>
            <span className="v muted">
              {fmtKw(bess.p_kw)} / {fmtNum(bess.q_kvar, 1, ' кВар')}
            </span>
          </div>
          {bess.clamps.length > 0 && (
            <div className="ctl-row">
              <span className="k">Clamp</span>
              <span className="v warn">{bess.clamps.join('; ')}</span>
            </div>
          )}
          <div className="ctl-row">
            <span className="k">Chargeable / dischargeable</span>
            <span className="v muted">
              {fmtNum(bess.chargeable_kwh, 0, ' кВт·год')} /{' '}
              {fmtNum(bess.dischargeable_kwh, 0, ' кВт·год')}
            </span>
          </div>
          <div className="ctl-row">
            <span className="k">Rated (SL) P / E</span>
            <span className="v muted">
              {fmtNum(bess.rated_kw, 0, ' кВт')} / {fmtNum(bess.rated_kwh, 0, ' кВт·год')}
            </span>
          </div>
          <div className="ctl-row">
            <span className="k">Паспорт P / E / шаф</span>
            <span className="v muted">
              {fmtNum(bess.passport_kw, 0, ' кВт')} / {fmtNum(bess.passport_kwh, 0, ' кВт·год')} /{' '}
              {bess.passport_ess_count ?? '—'}
            </span>
          </div>
          <div className="ctl-row">
            <span className="k">К-сть ESS / PCS (40488/40489)</span>
            <span className="v muted">
              {bess.n_ess ?? '—'} / {bess.n_pcs ?? '—'}
            </span>
          </div>
          <div className="ctl-row">
            <span className="k">PCS in operation / shutdown</span>
            <span className="v muted">
              {bess.pcs_in_operation ?? '—'} / {bess.pcs_shutdown ?? '—'} — {bess.pcs_label || '—'}
            </span>
          </div>
          <div className="ctl-row">
            <span className="k">Накопичено заряд / розряд</span>
            <span className="v muted">
              {fmtNum(bess.charged_kwh, 0, ' кВт·год')} / {fmtNum(bess.discharged_kwh, 0, ' кВт·год')}
            </span>
          </div>
        </div>
      </details>
    </section>
  )
}

// --- Health checks table, spec §4 ---

const SEV_CLASS: Record<string, string> = {
  ok: 'ok',
  info: 'plain',
  warning: 'warn',
  alarm: 'err',
}

function HealthChecksCard({ checks, alarms }: { checks: HealthCheck[]; alarms?: string[] }) {
  return (
    <section className="ctl-card">
      <h2>Діагностика: очікувано vs факт</h2>
      <table className="ctl-events-table ctl-checks-table">
        <thead>
          <tr>
            <th>Показник</th>
            <th>Очікувано</th>
            <th>Факт</th>
            <th>Статус</th>
          </tr>
        </thead>
        <tbody>
          {checks.map((c) => (
            <tr key={c.id}>
              <td>{c.label || c.id}</td>
              <td className="muted">{c.expected || '—'}</td>
              <td>
                {c.actual || '—'}
                {c.detail && <div className="ctl-check-detail">{c.detail}</div>}
              </td>
              <td>
                <span className={'ctl-sev ' + (SEV_CLASS[c.severity] ?? '')}>{c.severity}</span>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {alarms && (
        <p className="ctl-card-sub" style={{ marginTop: 10 }}>
          Слова алармів SL (50000…50005): <code>{alarms.join(' ')}</code>
        </p>
      )}
    </section>
  )
}

// --- Inverter fleet, spec §6.3 (same layout as the Edge Console) ---

const INV_ORDER: Record<string, number> = {
  fault: 0,
  unreachable: 1,
  shutdown: 2,
  unknown: 3,
  starting: 4,
  on_grid: 5,
  standby: 6,
}

const INV_SEV: Record<string, string> = {
  on_grid: 'ok',
  starting: 'plain',
  standby: 'plain',
  fault: 'err',
  shutdown: 'warn',
  unreachable: 'err',
  unknown: 'warn',
}

function InvertersCard({ inverters }: { inverters: InverterHealth[] }) {
  const [open, setOpen] = useState<number | null>(null)

  const counts: Record<string, number> = {}
  for (const r of inverters) counts[r.class] = (counts[r.class] ?? 0) + 1
  const sorted = [...inverters].sort(
    (a, b) =>
      (INV_ORDER[a.class] ?? 9) - (INV_ORDER[b.class] ?? 9) || a.device_address - b.device_address,
  )

  return (
    <section className="ctl-card">
      <h2>Інвертори</h2>
      <div className="ctl-inv-chips">
        <span className="ctl-chip plain">{inverters.length} інверторів</span>
        <span className="ctl-chip plain">{counts.on_grid ?? 0} у мережі</span>
        {(counts.starting ?? 0) > 0 && <span className="ctl-chip plain">{counts.starting} пуск</span>}
        <span className="ctl-chip plain">{counts.standby ?? 0} standby</span>
        <span className={'ctl-chip ' + ((counts.fault ?? 0) > 0 ? 'err' : 'plain')}>
          {counts.fault ?? 0} аварія
        </span>
        {(counts.shutdown ?? 0) > 0 && (
          <span className="ctl-chip warn">{counts.shutdown} вимкнено</span>
        )}
        <span className={'ctl-chip ' + ((counts.unreachable ?? 0) > 0 ? 'err' : 'plain')}>
          {counts.unreachable ?? 0} без зв'язку
        </span>
      </div>
      <div className="ctl-inv-wrap">
        <table className="ctl-events-table ctl-inv-table">
          <thead>
            <tr>
              <th>Addr</th>
              <th>Мітка</th>
              <th>Статус</th>
              <th>P, кВт</th>
              <th>Status hex</th>
              <th>Major</th>
              <th>Minor</th>
              <th>Warning</th>
              <th>t, °C</th>
              <th>Poll</th>
            </tr>
          </thead>
          <tbody>
            {sorted.map((r) => (
              <InvRow
                key={r.device_address}
                r={r}
                open={open === r.device_address}
                onToggle={() =>
                  setOpen((cur) => (cur === r.device_address ? null : r.device_address))
                }
              />
            ))}
          </tbody>
        </table>
      </div>
    </section>
  )
}

function InvRow({
  r,
  open,
  onToggle,
}: {
  r: InverterHealth
  open: boolean
  onToggle: () => void
}) {
  const rowCls =
    r.class === 'fault' || r.class === 'unreachable'
      ? 'ctl-inv-bad'
      : r.class === 'shutdown'
        ? 'ctl-inv-warn'
        : ''
  // Hex — джерело істини; канонна розшифровка (label_uk) — muted-рядком
  // під ним, ніколи замість нього.
  const hexDec = (v?: string, label?: string) =>
    v && v !== '0x0' ? (
      <>
        <strong>{v}</strong>
        {label && (
          <div className="ctl-inv-declabel" title={label}>
            {label}
          </div>
        )}
      </>
    ) : (
      (v ?? '—')
    )
  const decodeParts = [
    r.status_label_uk && `status: ${r.status_label_uk}`,
    r.major_label_uk && `major: ${r.major_label_uk}`,
    r.minor_label_uk && `minor: ${r.minor_label_uk}`,
    r.warning_label_uk && `warning: ${r.warning_label_uk}`,
  ].filter(Boolean)
  return (
    <>
      <tr className={'ctl-inv-row ' + rowCls} onClick={onToggle}>
        <td>
          <strong>{r.device_address}</strong>
        </td>
        <td>{r.label || `addr ${r.device_address}`}</td>
        <td>
          <span className={'ctl-sev ' + (INV_SEV[r.class] ?? '')}>
            {r.status_label_uk || r.status_label || r.class}
          </span>
        </td>
        <td>{fmtNum(r.p_kw)}</td>
        <td className="mono">{r.status_raw ?? '—'}</td>
        <td className="mono">{hexDec(r.major_fault, r.major_label_uk)}</td>
        <td className="mono">{hexDec(r.minor_fault, r.minor_label_uk)}</td>
        <td className="mono">{hexDec(r.warning, r.warning_label_uk)}</td>
        <td>{fmtNum(r.temp_c)}</td>
        <td>
          <span className={'ctl-sev ' + (r.poll_ok ? 'ok' : 'err')}>
            {r.poll_ok ? 'OK' : 'fail'}
          </span>
        </td>
      </tr>
      {open && (
        <tr className="ctl-inv-detail">
          <td colSpan={10}>
            база регістрів {r.register_base} · P/Q {fmtNum(r.p_kw)} кВт / {fmtNum(r.q_kvar)} кВар ·
            P DC / I DC {fmtNum(r.p_dc_kw)} кВт / {fmtNum(r.i_dc_a, 2)} А · cos φ {fmtNum(r.pf, 3)} ·
            ізоляція {fmtNum(r.insulation_mohm, 3)} МОм · t {fmtNum(r.temp_c)} °C · status{' '}
            {r.status_raw ?? '—'} ({r.status_label || '—'}) · major/minor/warning{' '}
            {r.major_fault ?? '—'}/{r.minor_fault ?? '—'}/{r.warning ?? '—'} · poll{' '}
            {r.poll_ok ? 'OK' : `fail${r.poll_error ? ` — ${r.poll_error}` : ''}`} · {r.ts}
            {decodeParts.length > 0 && (
              <div className="ctl-inv-decode">{decodeParts.join(' · ')}</div>
            )}
          </td>
        </tr>
      )}
    </>
  )
}
