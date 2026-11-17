import type { EdgeSiteStatus } from './controlClient'

// WriteMode tells whether the IoT2050 writes into the SmartLoggers, from
// the edge heartbeat (health.write). A build without the write block
// cannot write; no status yet is unknown. The consoles word the plan by
// it: «shadow — запису немає» stops being true once the device writes.
export type WriteMode = 'writing' | 'shadow' | 'unknown'

export function writeMode(status: EdgeSiteStatus | null | undefined): WriteMode {
  if (!status) return 'unknown'
  return status.health?.write?.enabled ? 'writing' : 'shadow'
}

// writeBanner is the «Стан» / «Режими» banner for the mode.
export const writeBanner: Record<WriteMode, string> = {
  writing: 'ЗАПИС: IoT2050 пише команди в SmartLogger — план виконується на обладнанні.',
  shadow: 'SHADOW: команди віртуальні — запис у SmartLogger вимкнено, керує Encombi.',
  unknown: 'Стан запису IoT2050 ще не отримано.',
}
