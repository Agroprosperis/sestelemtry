import type { ReactNode } from 'react'
import type { EconomicsPortfolioResponse, EconomicsPortfolioSite, EconomicsPortfolioTrendMonth } from '../../api'
import { formatOrganizationLabel } from '../../dashboard/config'
import { formatMonthTitle, formatMwh, formatMwhNumber, formatPercent, formatPeriodTitle, formatUah, formatYearTitle } from '../monthly/format'
import { useEconomicsPortfolioData } from '../useEconomicsPortfolioData'

export type PortfolioScope = 'month' | 'year' | 'period'

type Props = {
  // YYYY-MM derived from the page anchor (used when scope=month).
  month: string
  // YYYY derived from the page anchor (used when scope=year).
  period: string
  // from/to are both YYYY-MM, the sliding-window bounds (scope=period).
  from: string
  to: string
  // scope is owned by the page so the header period picker (month / year /
  // custom window) stays in sync with the portfolio's own granularity toggle.
  scope: PortfolioScope
  onScopeChange: (next: PortfolioScope) => void
  // onDiagnoseBess navigates to the flagged object's economics so the
  // operator can inspect the УЗЕ days the anomaly filter excluded.
  onDiagnoseBess?: (site: EconomicsPortfolioSite) => void
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
export function EconomicsPortfolioView({
  month,
  period,
  from,
  to,
  scope,
  onScopeChange,
  onDiagnoseBess,
  refreshKey,
}: Props) {
  const { portfolio, loading, error } = useEconomicsPortfolioData({
    active: true,
    scope,
    month,
    period,
    from,
    to,
    refreshKey,
  })

  const label =
    scope === 'month'
      ? formatMonthTitle(month)
      : scope === 'period'
        ? formatPeriodTitle(from, to) || 'обраний період'
        : formatYearTitle(period)

  return (
    <section className="economics-portfolio" aria-label="Портфельний дашборд">
      <div className="economics-month-section-head">
        <h3 className="economics-month-section-title">Зведений резерв по об'єктах · {label}</h3>
        <div className="economics-range-switch economics-portfolio-scope" role="group" aria-label="Період портфеля">
          <button type="button" className={scope === 'month' ? 'active' : ''} aria-pressed={scope === 'month'} onClick={() => onScopeChange('month')}>
            Місяць
          </button>
          <button type="button" className={scope === 'year' ? 'active' : ''} aria-pressed={scope === 'year'} onClick={() => onScopeChange('year')}>
            Рік
          </button>
          <button type="button" className={scope === 'period' ? 'active' : ''} aria-pressed={scope === 'period'} onClick={() => onScopeChange('period')}>
            Період
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
        <PortfolioBody portfolio={portfolio} onDiagnoseBess={onDiagnoseBess} />
      ) : (
        <p className="economics-loading">Немає даних.</p>
      )}
    </section>
  )
}

const ANOMALY_REASON_TIP_UA: Record<string, string> = {
  peak_spike: 'стрибок потужності',
  hourly_over_limit: 'заряд/розряд понад ліміт',
  after_gap: 'розрив звʼязку',
}

function bessWarnTitle(site: EconomicsPortfolioSite): string {
  const hours = site.bess_anomalous_hours || site.bess_anomalous_days
  const reasons = (site.bess_anomaly_reasons ?? [])
    .map((r) => ANOMALY_REASON_TIP_UA[r] ?? r)
    .filter(Boolean)
  const reasonPart = reasons.length > 0 ? ` · ${reasons.join(', ')}` : ''
  const dates = (site.bess_anomalous_dates ?? []).filter(Boolean)
  const datePart = dates.length > 0 ? ` · ${dates.join(', ')}` : ''
  return `Дані УЗЕ биті: виключено ${hours} год.${reasonPart}${datePart}`
}

function BessWarnButton({
  site,
  onDiagnoseBess,
}: {
  site: EconomicsPortfolioSite
  onDiagnoseBess?: (site: EconomicsPortfolioSite) => void
}) {
  const title = bessWarnTitle(site)

  const body = (
    <>
      <span className="economics-portfolio-warn-icon" aria-hidden="true">
        ⚠
      </span>
      <span className="economics-portfolio-warn-tip" role="tooltip">
        {title}
        {onDiagnoseBess ? ' Натисніть, щоб відкрити день.' : ''}
      </span>
    </>
  )

  if (!onDiagnoseBess) {
    return <span className="economics-portfolio-warn">{body}</span>
  }
  return (
    <button
      type="button"
      className="economics-portfolio-warn"
      aria-label={`${formatOrganizationLabel(site.id)}: ${title}`}
      onClick={(e) => {
        e.stopPropagation()
        onDiagnoseBess(site)
      }}
    >
      {body}
    </button>
  )
}

function PortfolioBody({
  portfolio,
  onDiagnoseBess,
}: {
  portfolio: EconomicsPortfolioResponse
  onDiagnoseBess?: (site: EconomicsPortfolioSite) => void
}) {
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
                  <BessWarnButton site={s} onDiagnoseBess={onDiagnoseBess} />
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
                      <BessWarnButton site={s} onDiagnoseBess={onDiagnoseBess} />
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

      {portfolio.trend.length > 0 ? <PortfolioTrend months={portfolio.trend} /> : null}
    </>
  )
}

// shortMonth turns "YYYY-MM" into "MM.YY" for compact axis labels.
function shortMonth(ym: string): string {
  return `${ym.slice(5, 7)}.${ym.slice(2, 4)}`
}

const TREND_SERIES = [
  { key: 'pv_kwh', name: 'СЕС', color: '#12b76a' },
  { key: 'grid_import_kwh', name: 'імпорт', color: '#3b82f6' },
  { key: 'grid_export_kwh', name: 'експорт', color: '#f97316' },
  { key: 'load_kwh', name: 'споживання', color: '#fdba74' },
] as const

// PortfolioTrend is the per-month portfolio energy trend (year scope): a
// grouped mini-bar column per month, scaled to the axis max across all
// series, with a native tooltip carrying the MWh figures + project effect.
function PortfolioTrend({ months }: { months: EconomicsPortfolioTrendMonth[] }) {
  const axisMax = Math.max(
    ...months.flatMap((m) => [m.pv_kwh, m.grid_import_kwh, m.grid_export_kwh, m.load_kwh]),
    1,
  )
  return (
    <div className="economics-portfolio-trend">
      <div className="economics-month-section-head">
        <h4 className="economics-month-section-title">Енергетичний тренд по місяцях (портфель)</h4>
        <span className="economics-month-muted">МВт·год/міс</span>
      </div>
      <div className="economics-portfolio-trend-legend">
        {TREND_SERIES.map((s) => (
          <span key={s.key}>
            <i style={{ background: s.color }} />
            {s.name}
          </span>
        ))}
      </div>
      <div className="economics-portfolio-trend-plot" style={{ gridTemplateColumns: `repeat(${months.length}, minmax(0, 1fr))` }}>
        {months.map((m) => {
          const tip = `${m.month}\nСЕС ${formatMwhNumber(m.pv_kwh)} · спож. ${formatMwhNumber(m.load_kwh)} · імпорт ${formatMwhNumber(
            m.grid_import_kwh,
          )} · експорт ${formatMwhNumber(m.grid_export_kwh)}\nЕфект ${formatUah(m.effect_uah)}`
          return (
            <div className="economics-portfolio-trend-col" key={m.month} title={tip}>
              <div className="economics-portfolio-trend-bars">
                {TREND_SERIES.map((s) => (
                  <i
                    key={s.key}
                    style={{ height: `${Math.max((m[s.key] / axisMax) * 80, m[s.key] > 0 ? 2 : 0)}px`, background: s.color }}
                  />
                ))}
              </div>
              <em>{shortMonth(m.month)}</em>
            </div>
          )
        })}
      </div>
    </div>
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
