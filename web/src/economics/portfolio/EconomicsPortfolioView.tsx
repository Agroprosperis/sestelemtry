import { useState } from 'react'
import type { ReactNode } from 'react'
import type { EconomicsPortfolioSite } from '../../api'
import { formatOrganizationLabel } from '../../dashboard/config'
import { formatMonthTitle, formatMwh, formatPercent, formatUah, formatYearTitle } from '../monthly/format'
import { useEconomicsPortfolioData } from '../useEconomicsPortfolioData'

type Scope = 'month' | 'year'

type Props = {
  // YYYY-MM derived from the page anchor (used when scope=month).
  month: string
  // YYYY derived from the page anchor (used when scope=year).
  period: string
  refreshKey?: number
}

// compareTotal is the stacked-bar length: project effect + both reserves.
function compareTotal(s: EconomicsPortfolioSite): number {
  return s.effect_uah + s.schedule_reserve_uah + s.bess_reserve_uah
}

// EconomicsPortfolioView is the zведений (all-objects) dashboard: a per-
// object comparison of project effect + work-schedule reserve + УЗЕ
// optimum reserve (project_net), with ⚠ flags for objects whose УЗЕ
// telemetry had anomalous days excluded (§2).
export function EconomicsPortfolioView({ month, period, refreshKey }: Props) {
  const [scope, setScope] = useState<Scope>('month')
  const { portfolio, loading, error } = useEconomicsPortfolioData({
    active: true,
    scope,
    month,
    period,
    refreshKey,
  })

  const label = scope === 'month' ? formatMonthTitle(month) : formatYearTitle(period)

  return (
    <section className="economics-portfolio" aria-label="Портфельний дашборд">
      <div className="economics-month-section-head">
        <h3 className="economics-month-section-title">Зведений резерв по об'єктах · {label}</h3>
        <div className="economics-range-switch economics-portfolio-scope" role="group" aria-label="Період портфеля">
          <button type="button" className={scope === 'month' ? 'active' : ''} aria-pressed={scope === 'month'} onClick={() => setScope('month')}>
            Місяць
          </button>
          <button type="button" className={scope === 'year' ? 'active' : ''} aria-pressed={scope === 'year'} onClick={() => setScope('year')}>
            Рік
          </button>
        </div>
      </div>

      {error ? (
        <section className="economics-banner economics-banner-error" role="alert">
          Не вдалося завантажити портфель: {error}
        </section>
      ) : loading ? (
        <p className="economics-loading">Завантаження…</p>
      ) : portfolio ? (
        <PortfolioBody portfolio={portfolio} />
      ) : (
        <p className="economics-loading">Немає даних.</p>
      )}
    </section>
  )
}

function PortfolioBody({ portfolio }: { portfolio: { sites: EconomicsPortfolioSite[]; totals: EconomicsPortfolioSite } }) {
  const sites = portfolio.sites
  const totals = portfolio.totals
  const withData = sites.filter((s) => s.has_data)
  const maxTotal = Math.max(...withData.map(compareTotal), 1)

  return (
    <>
      <div className="economics-portfolio-cards">
        <SummaryCard label="Ефект проєкту" value={formatUah(totals.effect_uah)} sub="за період, всі об'єкти" />
        <SummaryCard label="Резерв графіка робіт" value={formatUah(totals.schedule_reserve_uah)} sub="перенесення гнучких робіт у день" amber />
        <SummaryCard label="Оптимум УЗЕ (резерв)" value={formatUah(totals.bess_reserve_uah)} sub="project_net, наскрізний DP" amber />
        <SummaryCard label="Разом з резервами" value={formatUah(totals.effect_uah + totals.action_reserve_uah)} sub="ефект + резерв графіка + УЗЕ" good />
      </div>

      <div className="economics-portfolio-legend">
        <span><i style={{ background: '#7c3aed' }} />ефект проєкту</span>
        <span><i style={{ background: '#f59e0b' }} />резерв графіка</span>
        <span><i style={{ background: '#0ea5e9' }} />оптимум УЗЕ</span>
      </div>

      <div className="economics-portfolio-bars">
        {sites.map((s) => {
          if (!s.has_data) {
            return (
              <div className="economics-portfolio-row" key={s.id}>
                <span className="economics-portfolio-name">{formatOrganizationLabel(s.id)}</span>
                <div className="economics-portfolio-track">
                  <div className="economics-portfolio-fill nodata" style={{ width: '4px' }} />
                </div>
                <span className="economics-portfolio-value">—</span>
              </div>
            )
          }
          const total = compareTotal(s)
          const outerW = (total / maxTotal) * 100
          const eW = total ? (s.effect_uah / total) * 100 : 0
          const sW = total ? (s.schedule_reserve_uah / total) * 100 : 0
          const bW = total ? (s.bess_reserve_uah / total) * 100 : 0
          return (
            <div className="economics-portfolio-row" key={s.id}>
              <span className="economics-portfolio-name">
                {formatOrganizationLabel(s.id)}
                {!s.bess_data_ok ? (
                  <span
                    className="economics-portfolio-warn"
                    title={`Дані УЗЕ частково некоректні — виключено ${s.bess_anomalous_days} дн.`}
                  >
                    {' '}⚠
                  </span>
                ) : null}
              </span>
              <div className="economics-portfolio-track">
                <div className="economics-portfolio-stack" style={{ width: `${Math.max(outerW, 0.5)}%` }}>
                  <div className="economics-portfolio-fill effect" style={{ width: `${eW}%` }} />
                  <div className="economics-portfolio-fill schedule" style={{ width: `${sW}%` }} />
                  <div className="economics-portfolio-fill bess" style={{ width: `${bW}%` }} />
                </div>
              </div>
              <span className="economics-portfolio-value">{formatUah(s.effect_uah)}</span>
            </div>
          )
        })}
      </div>

      <div className="economics-table-scroll">
        <table className="economics-table economics-month-table">
          <thead>
            <tr>
              <th>Об'єкт</th>
              <th>Ефект проєкту</th>
              <th>EBITDA</th>
              <th>Резерв графіка</th>
              <th>Оптимум УЗЕ</th>
              <th>Разом резерв</th>
              <th>СЕС</th>
              <th>Захоплення УЗЕ</th>
            </tr>
          </thead>
          <tbody>
            {sites.map((s) => {
              const captured =
                s.bess_reserve_uah + s.ess_net_uah > 0 ? s.ess_net_uah / (s.bess_reserve_uah + s.ess_net_uah) : 0
              return (
                <tr key={s.id} className={s.has_data ? undefined : 'economics-portfolio-empty'}>
                  <td className="economics-month-table-left">
                    {formatOrganizationLabel(s.id)}
                    {!s.bess_data_ok && s.has_data ? (
                      <span
                        className="economics-portfolio-warn"
                        title={`Дані УЗЕ биті: виключено ${s.bess_anomalous_days} дн.`}
                      >
                        {' '}⚠
                      </span>
                    ) : null}
                  </td>
                  {s.has_data ? (
                    <>
                      <td>{formatUah(s.effect_uah)}</td>
                      <td>{formatUah(s.ebitda_uah)}</td>
                      <td>{formatUah(s.schedule_reserve_uah)}</td>
                      <td>{formatUah(s.bess_reserve_uah)}</td>
                      <td>{formatUah(s.action_reserve_uah)}</td>
                      <td>{formatMwh(s.pv_kwh)}</td>
                      <td>{formatPercent(captured)}</td>
                    </>
                  ) : (
                    <td colSpan={7} className="economics-portfolio-empty-cell">немає даних</td>
                  )}
                </tr>
              )
            })}
            <tr className="economics-table-summary-row">
              <td className="economics-month-table-left">Портфель</td>
              <td>{formatUah(totals.effect_uah)}</td>
              <td>{formatUah(totals.ebitda_uah)}</td>
              <td>{formatUah(totals.schedule_reserve_uah)}</td>
              <td>{formatUah(totals.bess_reserve_uah)}</td>
              <td>{formatUah(totals.action_reserve_uah)}</td>
              <td>{formatMwh(totals.pv_kwh)}</td>
              <td>—</td>
            </tr>
          </tbody>
        </table>
      </div>
    </>
  )
}

function SummaryCard({
  label,
  value,
  sub,
  amber,
  good,
}: {
  label: string
  value: string
  sub: string
  amber?: boolean
  good?: boolean
}): ReactNode {
  const cls = good ? 'kpi-card kpi-card-success' : amber ? 'kpi-card kpi-card-warning' : 'kpi-card'
  return (
    <div className={cls}>
      <span className="kpi-label">{label}</span>
      <span className="kpi-value">{value}</span>
      <span className="kpi-sub">{sub}</span>
    </div>
  )
}
