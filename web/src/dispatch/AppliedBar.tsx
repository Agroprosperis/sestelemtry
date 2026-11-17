// AppliedBar — «На контролері»: which desk version is in force, who
// confirmed it and when, whether the controller took it, how far its
// plan reaches and whether the IoT2050 writes it into the SmartLoggers.
// The desk itself shows hours; this line answers «what does the edge run».

import type { EdgeSiteStatus } from '../control/controlClient'
import { writeMode } from '../control/writeState'
import { manifestVersion } from './applied'
import type { Command, DeskState } from './dispatchClient'
import { FUTURE_HOURS, hoursBetween, same, timeLabel, type Clock } from './model'

type Sev = 'ok' | 'warn' | 'err' | 'plain'
type Part = { text: string; sev: Sev }

const dayOf = (d: Date, timeZone: string) =>
  new Intl.DateTimeFormat('en-CA', { timeZone, year: 'numeric', month: '2-digit', day: '2-digit' }).format(d)

// when is the local time of an instant, with the date when not today.
function when(iso: string | null | undefined, timeZone: string, nowISO: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  const time = new Intl.DateTimeFormat('uk-UA', { hour: '2-digit', minute: '2-digit', hourCycle: 'h23', timeZone }).format(d)
  if (dayOf(d, timeZone) === dayOf(new Date(nowISO), timeZone)) return time
  return new Intl.DateTimeFormat('uk-UA', { day: '2-digit', month: '2-digit', timeZone }).format(d) + ' ' + time
}

function delivery(state: DeskState, status: EdgeSiteStatus | null | undefined): Part {
  if (!status) return { text: 'стан контролера завантажується…', sev: 'plain' }
  const m = status.manifest
  if (!m || m.state === 'none' || !m.manifest_id) return { text: 'на контролер ще нічого не публікувалось', sev: 'warn' }
  const at = (iso?: string) => when(iso, state.timezone, state.now)
  const v = manifestVersion(m.payload?.note)
  const ver = state.applied.version
  if (v !== null && v < ver) return { text: 'на контролері ще версія ' + v + ': нову не опубліковано', sev: 'err' }
  if (v !== null && v > ver) return { text: 'на контролері вже версія ' + v + ', оновлюю пульт…', sev: 'warn' }
  if (m.state === 'expired') return { text: 'manifest прострочений: контролер у безпечному режимі', sev: 'err' }
  if (m.state === 'pending') return { text: 'очікує контролера · опубліковано ' + at(m.issued_at), sev: 'warn' }
  return { text: 'контролер застосував о ' + at(m.applied_at), sev: 'ok' }
}

// planEnd is the desk index where the plan in force stops: the
// manifest's own plan when it is this version, else the desk's LP.
function planEnd(state: DeskState, status: EdgeSiteStatus | null | undefined): number {
  const payload = status?.manifest?.payload
  const intervals = payload?.plan?.intervals
  if (payload && manifestVersion(payload.note) === state.applied.version) {
    if (!intervals || intervals.length === 0) return 0
    const last = Date.parse(intervals[intervals.length - 1].ts) + 3_600_000
    return hoursBetween(state.start_hour, new Date(last).toISOString())
  }
  return state.result.known_hours
}

function horizon(state: DeskState, status: EdgeSiteStatus | null | undefined, clock: Clock): Part {
  const end = planEnd(state, status)
  if (end <= 0) return { text: 'плану немає: самоспоживання', sev: 'plain' }
  if (end >= FUTURE_HOURS) return { text: 'план на всі 24 год', sev: 'plain' }
  return { text: 'план до ' + timeLabel(clock, end) + ', далі самоспоживання', sev: 'plain' }
}

// blocksOf counts the operator's blocks: runs of one command.
function blocksOf(commands: (Command | null)[]): number {
  let n = 0
  commands.forEach((c, i) => {
    if (c && c.type !== 'auto' && !same(c, commands[i - 1] ?? null)) n++
  })
  return n
}

function manual(state: DeskState, status: EdgeSiteStatus | null | undefined): Part {
  const payload = status?.manifest?.payload
  const n =
    payload && manifestVersion(payload.note) === state.applied.version
      ? (payload.manual_commands ?? []).length
      : blocksOf(state.applied.model.commands)
  return { text: n > 0 ? 'ручних блоків: ' + n : 'ручних команд немає', sev: 'plain' }
}

function writing(status: EdgeSiteStatus | null | undefined): Part | null {
  switch (writeMode(status)) {
    case 'writing':
      return { text: 'IoT2050 пише в SmartLogger: план виконується на обладнанні', sev: 'err' }
    case 'shadow':
      return { text: 'запис вимкнено: план виконується лише в тіні, керує Encombi', sev: 'plain' }
    default:
      return null
  }
}

export function AppliedBar({ state, status, clock }: { state: DeskState; status: EdgeSiteStatus | null | undefined; clock: Clock }) {
  const a = state.applied
  const version: Part =
    a.version > 0
      ? {
          text:
            'версія ' + a.version + ' · підтверджено ' + when(a.confirmed_at, state.timezone, state.now) +
            (a.confirmed_by ? ' · ' + a.confirmed_by : ''),
          sev: 'plain',
        }
      : { text: 'підтверджених версій немає: AUTO за замовчуванням', sev: 'plain' }
  const parts = [version, delivery(state, status), horizon(state, status, clock), manual(state, status), writing(status)].filter(
    (p): p is Part => p !== null,
  )
  return (
    <div className="d-applied" aria-label="План на контролері" aria-live="polite">
      <strong>На контролері</strong>
      {parts.map((p, k) => (
        <span key={k} className={'d-applied-part ' + p.sev}>
          {p.text}
        </span>
      ))}
    </div>
  )
}
