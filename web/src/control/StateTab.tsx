// StateTab — the «Стан» tab of the control mode: the dashboard's live
// flow card and day chart, extended with the manifest layer (current
// shadow command, control-state KPIs, plan-vs-fact overlay).

import { useMemo } from 'react'
import { CurrentSnapshotNarrative } from '../dashboard/components/CurrentSnapshotNarrative'
import { EnergyChart } from '../dashboard/components/EnergyChart'
import { WeatherCard } from '../dashboard/components/WeatherCard'
import { useDashboardData } from '../dashboard/hooks/useDashboardData'
import type { EdgeSiteStatus } from './controlClient'
import { appliedAnnotation, manifestPlanToUzePlan } from './manifestPlan'

type Props = {
  site: string
  status: EdgeSiteStatus | null
}

const PLAN_SOURCE_LABELS: Record<string, string> = {
  manifest: 'План (manifest)',
  fallback: 'Fallback-пресет',
  config: 'Пресет із конфіга',
}

const REASON_LABELS: Record<string, string> = {
  plan_discharge: 'розряд за планом',
  plan_charge: 'заряд за планом',
  plan_hold: 'утримання за планом',
  self_charge: 'заряд від надлишку СЕС',
  self_discharge: 'розряд у локальний дефіцит',
  hold: 'утримання',
  data_fault: 'неповні дані',
  pcs_shutdown: 'PCS вимкнено',
  insufficient_data: 'недостатньо даних',
}

function fmtKw(v: number | undefined): string {
  if (v == null || !Number.isFinite(v)) return '—'
  const sign = v > 0 ? '+' : ''
  return `${sign}${v.toFixed(0)} кВт`
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
  const clamped =
    virtualKw != null &&
    planKw != null &&
    Math.abs(planKw) - Math.abs(virtualKw) > 1

  const hb = status?.heartbeat

  return (
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
          <h2>Команда УЗЕ (shadow)</h2>
          <div className="ctl-rows">
            <div className="ctl-row">
              <span className="k">Команда УЗЕ</span>
              <span className="v">{fmtKw(virtualKw)}</span>
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
            {clamped && (
              <div className="ctl-row">
                <span className="k">Обмежено лімітами</span>
                <span className="v warn">
                  план {fmtKw(planKw)} → {fmtKw(virtualKw)}
                </span>
              </div>
            )}
            <div className="ctl-row">
              <span className="k">Рішення</span>
              <span className="v muted">{fmtAge(status?.decision?.age_seconds)}</span>
            </div>
          </div>
        </section>

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
  )
}
