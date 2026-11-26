import type { EdgeSiteStatus } from '../control/controlClient'

// closedScenarios are the desk's manual commands the edge would not
// carry out now (offgrid §4.3). «AUTO» only removes a block, so it stays
// open; a paused price plan shows in the power line's restriction.
export function closedScenarios(status: EdgeSiteStatus | null | undefined): Record<string, string> {
  const a = status?.heartbeat.online ? status.health?.supply?.actions : undefined
  if (!a) return {}
  const out: Record<string, string> = {}
  for (const [type, v] of Object.entries(a.manual ?? {})) {
    if (!v.ok) out[type] = v.reason ?? 'недоступно'
  }
  return out
}
