import type { UzePlanResponse } from '../../api'
import { formatPercent, formatUah } from '../../economics/monthly/format'

type Props = {
  plan: UzePlanResponse | null | undefined
  loading?: boolean
}

// Data-quality flags the backend attaches to a weak plan. They carry an
// optional ":N" suffix (hours affected), so we match on the prefix.
const WARNING_TEXT: Array<[string, (n: number) => string]> = [
  ['NO_RDN', () => 'немає цін РДН за добу'],
  ['PARTIAL_RDN', (n) => `без цін РДН: ${n} год`],
  ['NO_SOC', () => 'немає телеметрії SOC'],
  ['TELEMETRY_ANOMALY', (n) => `аномальна телеметрія: ${n} год`],
]

function warningText(code: string): string | null {
  const [prefix, rest] = code.split(':')
  const match = WARNING_TEXT.find(([p]) => p === prefix)
  if (!match) return null
  return match[1](Number(rest) || 0)
}

// AiPlanSummary is the one-line verdict above the day chart: what the
// optimizer says the day was worth, what the battery actually earned, and
// how much of that optimum was captured.
//
// Wording and semantics deliberately mirror the monthly economics page
// (`rollup.ts`) so "резерв" and "захоплено" mean the same thing wherever
// an operator reads them.
export function AiPlanSummary({ plan, loading }: Props) {
  if (loading || !plan || !plan.available) return null
  const t = plan.totals
  // A day where nothing was earned and nothing was on the table has no
  // verdict to give; a row of zeros would just be noise above the chart.
  if (Math.abs(t.optimum_uah) < 1 && Math.abs(t.fact_uah) < 1) return null
  const warnings = (plan.warnings ?? []).map(warningText).filter(Boolean) as string[]

  return (
    <div className="ai-plan-summary">
      <span className="ai-plan-summary-item">
        <span className="lbl">Оптимум</span>
        <strong className="opt">{formatUah(t.optimum_uah)}</strong>
      </span>
      <span className="ai-plan-summary-item">
        <span className="lbl">Факт</span>
        <strong>{formatUah(t.fact_uah)}</strong>
      </span>
      <span className="ai-plan-summary-item">
        <span className="lbl">Резерв</span>
        <strong className="bad">{formatUah(t.reserve_uah)}</strong>
      </span>
      {t.optimum_uah > 0 && (
        <span className="ai-plan-summary-item">
          <span className="lbl">Захоплено</span>
          <strong>{formatPercent(t.captured_share)}</strong>
        </span>
      )}
      {warnings.length > 0 && (
        <span className="ai-plan-summary-warn" title={warnings.join('; ')}>
          {warnings.join(' · ')}
        </span>
      )}
    </div>
  )
}
