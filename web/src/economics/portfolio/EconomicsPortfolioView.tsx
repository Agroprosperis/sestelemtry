import type { ReactNode } from 'react'
import type { EconomicsPortfolioResponse, EconomicsPortfolioSite, EconomicsPortfolioTrendMonth } from '../../api'
import { formatOrganizationLabel } from '../../dashboard/config'
import { OptimumInfo } from '../monthly/EconomicsMonthlyView'
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

// compareTotal is the stacked-bar length: earned EBITDA + both reserves.
function compareTotal(s: EconomicsPortfolioSite): number {
  return s.ebitda_uah + s.schedule_reserve_uah + s.bess_reserve_uah
}

// OPTIMUM_TIP explains the УЗЕ reserve in operator language: the
// optimum is the best dispatch the battery could have run inside its
// own demonstrated envelope, so the reserve is missed upside, not loss.
const OPTIMUM_TIP =
  'Оптимум УЗЕ — найкращий можливий графік заряду/розряду батареї в межах її ж фактичних можливостей (потужність, діапазон SOC, ККД циклу за телеметрією періоду), оцінений за цінами РДН і тарифами. Резерв = оптимум − факт: це недоотримана вигода, яку ще можна забрати кращим керуванням, а не збиток.'

// EconomicsPortfolioView is the zведений (all-objects) dashboard: a per-
// object comparison of earned EBITDA + work-schedule reserve + УЗЕ
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
  peak_spike: 'Стрибок потужності',
  hourly_over_limit: 'Заряд/розряд понад ліміт',
  after_gap: 'Розрив звʼязку',
}

function formatAnomalyDate(isoDate: string): string {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(isoDate)) return isoDate
  return `${isoDate.slice(8, 10)}.${isoDate.slice(5, 7)}.${isoDate.slice(0, 4)}`
}

function BessWarnButton({
  site,
  onDiagnoseBess,
}: {
  site: EconomicsPortfolioSite
  onDiagnoseBess?: (site: EconomicsPortfolioSite) => void
}) {
  const hours = site.bess_anomalous_hours || site.bess_anomalous_days
  const reasons = (site.bess_anomaly_reasons ?? [])
    .map((r) => ANOMALY_REASON_TIP_UA[r] ?? r)
    .filter(Boolean)
  const dates = (site.bess_anomalous_dates ?? []).filter(Boolean).map(formatAnomalyDate)
  const ariaTitle = [
    reasons.length > 0 ? reasons.join(', ') : 'Дані УЗЕ биті',
    `виключено ${hours} год.`,
    dates.length > 0 ? dates.join(', ') : '',
  ]
    .filter(Boolean)
    .join(' · ')

  const tip = (
    <span className="economics-portfolio-warn-tip" role="tooltip">
      <span className="economics-portfolio-warn-tip-title">
        {reasons.length > 0 ? reasons.join(' · ') : 'Дані УЗЕ биті'}
      </span>
      <span className="economics-portfolio-warn-tip-meta">
        Виключено {hours} год. з розрахунку УЗЕ
      </span>
      {dates.length > 0 ? (
        <span className="economics-portfolio-warn-tip-dates">{dates.join(', ')}</span>
      ) : null}
      {onDiagnoseBess ? (
        <span className="economics-portfolio-warn-tip-action">Натисніть, щоб відкрити день</span>
      ) : null}
    </span>
  )

  const body = (
    <>
      <span className="economics-portfolio-warn-icon" aria-hidden="true">
        ⚠
      </span>
      {tip}
    </>
  )

  if (!onDiagnoseBess) {
    return <span className="economics-portfolio-warn">{body}</span>
  }
  return (
    <button
      type="button"
      className="economics-portfolio-warn"
      aria-label={`${formatOrganizationLabel(site.id)}: ${ariaTitle}`}
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
        <SummaryCard
          label="EBITDA"
          value={formatUah(totals.ebitda_uah)}
          sub="за період, всі об'єкти"
          info="EBITDA = дохід та економія від СЕС і УЗЕ мінус операційні витрати за період, підсумовані по всіх об'єктах."
        />
        <SummaryCard
          label="Резерв графіка робіт"
          value={formatUah(totals.schedule_reserve_uah)}
          sub="перенесення гнучких робіт у день"
          info="Скільки ще можна зекономити, перенісши гнучкі роботи на денні години з дешевшою електроенергією та власним виробітком СЕС."
          amber
        />
        <SummaryCard
          label="Оптимум УЗЕ (резерв)"
          value={formatUah(totals.bess_reserve_uah)}
          sub="недобрана вигода УЗЕ проти оптимального графіка"
          info={OPTIMUM_TIP}
          amber
        />
        <SummaryCard
          label="Разом з резервами"
          value={formatUah(totals.ebitda_uah + totals.action_reserve_uah)}
          sub="EBITDA + резерв графіка + УЗЕ"
          info="Потенційний результат періоду: фактична EBITDA плюс обидва резерви, якби їх забрали повністю."
          good
        />
      </div>

      <div className="economics-portfolio-legend">
        <span><i style={{ background: '#7c3aed' }} />EBITDA</span>
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
          // A loss-making object can push a segment negative; clamp so the
          // stack degrades to an empty slot instead of an invalid width.
          const share = (v: number) => (total > 0 ? Math.max((v / total) * 100, 0) : 0)
          const eW = share(s.ebitda_uah)
          const sW = share(s.schedule_reserve_uah)
          const bW = share(s.bess_reserve_uah)
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
              <span className="economics-portfolio-value">{formatUah(s.ebitda_uah)}</span>
            </div>
          )
        })}
      </div>

      <div className="economics-table-scroll">
        <table className="economics-table economics-month-table">
          <thead>
            <tr>
              <th>Об'єкт</th>
              <th>EBITDA</th>
              <th>Резерв графіка</th>
              <th>
                Оптимум УЗЕ
                <OptimumInfo tip={OPTIMUM_TIP} />
              </th>
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
                      <td>{formatUah(s.ebitda_uah)}</td>
                      <td>{formatUah(s.schedule_reserve_uah)}</td>
                      <td>{formatUah(s.bess_reserve_uah)}</td>
                      <td>{formatUah(s.action_reserve_uah)}</td>
                      <td>{formatMwh(s.pv_kwh)}</td>
                      <td>{formatPercent(captured)}</td>
                    </>
                  ) : (
                    <td colSpan={6} className="economics-portfolio-empty-cell">немає даних</td>
                  )}
                </tr>
              )
            })}
            <tr className="economics-table-summary-row">
              <td className="economics-month-table-left">Портфель</td>
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
          )} · експорт ${formatMwhNumber(m.grid_export_kwh)}\nEBITDA ${formatUah(m.ebitda_uah)}`
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
  info,
  amber,
  good,
}: {
  label: string
  value: string
  sub: string
  // info renders the hover "i" next to the label, for metrics whose
  // meaning is not obvious from the caption alone (e.g. the УЗЕ optimum).
  info?: string
  amber?: boolean
  good?: boolean
}): ReactNode {
  const cls = good ? 'kpi-card kpi-card-success' : amber ? 'kpi-card kpi-card-warning' : 'kpi-card'
  return (
    <div className={cls}>
      <span className="kpi-label">
        {label}
        {info ? <OptimumInfo tip={info} /> : null}
      </span>
      <span className="kpi-value">{value}</span>
      <span className="kpi-sub">{sub}</span>
    </div>
  )
}
