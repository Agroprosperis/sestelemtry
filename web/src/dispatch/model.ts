// Desk model helpers, ported from the approved mockup
// (ems-dispatch-desk-browser-v2.html): preview building, hour states,
// action bands, command descriptions, review groups. Index 0 = the
// current hour (start_hour); history hours are negative.

import type { Command, CommandType, Constraints, DeskHour, HourResult, SimResult, WireModel } from './dispatchClient'

export const FUTURE_HOURS = 24
export const HISTORY_HOURS = 8

export type DeskModel = WireModel

export const clone = <T,>(v: T): T => JSON.parse(JSON.stringify(v)) as T
export const same = (a: unknown, b: unknown): boolean => JSON.stringify(a) === JSON.stringify(b)

const nf1 = new Intl.NumberFormat('uk-UA', { maximumFractionDigits: 1 })
const nf2 = new Intl.NumberFormat('uk-UA', { minimumFractionDigits: 2, maximumFractionDigits: 2 })

export const fmt = (x: number | null | undefined): string => (x === null || x === undefined ? '—' : nf1.format(x))
export const priceFmt = (v: number): string => nf2.format(v)

export const hasValue = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v)

export const scenarioNames: Record<CommandType, string> = {
  cover: 'Покривати споживання',
  solar: 'Заряд надлишками СЕС',
  target: 'Заряд до SOC',
  cap: 'AUTO · ліміт імпорту',
  export: 'Експорт через PCC',
  fixed: 'Потужність УЗЕ',
  hold: 'Пауза · 0 кВт',
  auto: 'AUTO',
}

export const scenarioOptions: { value: CommandType; label: string }[] = [
  { value: 'cover', label: 'Покривати споживання з УЗЕ' },
  { value: 'solar', label: 'Заряджати надлишками СЕС' },
  { value: 'target', label: 'Зарядити до SOC' },
  { value: 'cap', label: 'AUTO з лімітом імпорту' },
  { value: 'export', label: 'Експортувати в мережу через PCC' },
  { value: 'fixed', label: 'Задати потужність УЗЕ' },
  { value: 'hold', label: 'Пауза · 0 кВт' },
  { value: 'auto', label: 'Повернути в AUTO' },
]

export const defaultParams: Record<CommandType, number> = {
  cover: 300,
  solar: 300,
  target: 80,
  cap: 150,
  export: 150,
  fixed: 200,
  hold: 0,
  auto: 0,
}

export const intents: Record<CommandType, string> = {
  cover: 'УЗЕ покриває дефіцит після СЕС у межах потужності та резерву.',
  solar: 'Заряд лише фактичним надлишком СЕС. Без надлишку команда дає 0 кВт.',
  target:
    'Заряджає до заданого SOC наприкінці інтервалу. За потреби AUTO готує запас раніше. Після інтервалу знову діє AUTO з тарифами; ціль SOC не є постійним резервом.',
  cap: 'AUTO обирає заряд, розряд або очікування в межах ліміту загального імпорту PCC. Заряд УЗЕ враховується разом зі споживанням і СЕС. Ліміт — верхня межа, а не ціль: доступну потужність не обов’язково використовувати повністю.',
  export: 'Це експорт у точці приєднання. УЗЕ також має покрити дефіцит споживання.',
  fixed: 'Фіксована потужність УЗЕ. Імпорт або експорт PCC залежить від СЕС і споживання.',
  hold: 'Явні 0 кВт УЗЕ. Це пауза заряду й розряду, а не повернення в AUTO.',
  auto: 'Знімає ручний інтервал. AUTO будує новий план від поточного стану батареї.',
}

export function paramLabel(s: CommandType): string {
  if (s === 'target') return 'Ціль SOC наприкінці інтервалу'
  if (s === 'cap') return 'Максимальний імпорт PCC'
  if (s === 'export') return 'Чистий експорт у PCC'
  if (s === 'fixed') return 'Потужність саме УЗЕ'
  return 'Максимальна потужність УЗЕ'
}

// --- clock -------------------------------------------------------------

// Clock maps desk indices to local wall time. nowHour is the local hour
// of start_hour; midnight is the index of the next local midnight.
export type Clock = { nowHour: number; midnight: number }

export function makeClock(startHourISO: string, timeZone: string): Clock {
  const hour = Number(
    new Intl.DateTimeFormat('en-GB', { hour: '2-digit', hourCycle: 'h23', timeZone }).format(new Date(startHourISO)),
  )
  return { nowHour: hour, midnight: 24 - hour }
}

export const clockAt = (c: Clock, i: number): string => String((((c.nowHour + i) % 24) + 24) % 24).padStart(2, '0') + ':00'
export const timeLabel = (c: Clock, i: number): string => (c.nowHour + i >= 24 ? 'завтра ' : '') + clockAt(c, i)
export const optionTime = (c: Clock, i: number): string => (c.nowHour + i >= 24 ? 'Зав. ' : '') + clockAt(c, i)

// --- commands ----------------------------------------------------------

export function commandDesc(c: Command | null): string {
  if (!c) return 'AUTO · новий розрахунок'
  if (c.type === 'hold') return scenarioNames.hold
  if (c.type === 'target') return 'Заряд до SOC ' + fmt(c.value) + '%'
  if (c.type === 'fixed') return (c.direction === 'charge' ? 'Заряд' : 'Розряд') + ' УЗЕ ' + fmt(c.value) + ' кВт'
  if (c.type === 'cover' || c.type === 'solar') return scenarioNames[c.type] + ' · до ' + fmt(c.value) + ' кВт'
  return scenarioNames[c.type] + ' · ' + fmt(c.value) + ' кВт'
}

export type ActionMark = { name: string; short: string; value: string; compact: string; detail: string }

export function actionMark(c: Command | null): ActionMark {
  if (!c || c.type === 'auto') return { name: 'AUTO', short: 'AUTO', value: '', compact: '', detail: 'AUTO · новий розрахунок' }
  const kw = fmt(c.value) + ' кВт'
  const limit = '≤' + fmt(c.value)
  const charge = c.direction === 'charge'
  const marks: Record<Exclude<CommandType, 'auto'>, Omit<ActionMark, 'detail'>> = {
    cover: { name: 'Покриття', short: 'Покр', value: '≤ ' + kw, compact: limit },
    solar: { name: 'Заряд СЕС', short: 'СЕС', value: '≤ ' + kw, compact: limit },
    target: { name: 'Ціль SOC', short: 'SOC', value: 'до ' + fmt(c.value) + '%', compact: fmt(c.value) + '%' },
    cap: { name: 'AUTO · імпорт', short: 'Імп', value: '≤ ' + kw, compact: limit },
    export: { name: 'Експорт PCC', short: 'Експ', value: kw, compact: fmt(c.value) },
    fixed: { name: charge ? 'Заряд УЗЕ' : 'Розряд УЗЕ', short: charge ? 'Зар.' : 'Розр', value: kw, compact: fmt(c.value) },
    hold: { name: 'Пауза УЗЕ', short: 'Пауза', value: '0 кВт', compact: '0' },
  }
  return { ...marks[c.type], detail: commandDesc(c) }
}

// buildPreview applies the command in the fields to the selected hours
// of the draft — what the chart shows before «У чернетку».
export function buildPreview(
  working: DeskModel,
  cfg: Constraints,
  scenario: CommandType,
  value: number,
  direction: 'charge' | 'discharge',
  start: number,
  end: number,
): DeskModel {
  const m = clone(working)
  m.cfg = clone(cfg)
  const cmd: Command = { type: scenario, value, id: 'block-' + start + '-' + end + '-' + scenario }
  if (scenario === 'fixed') cmd.direction = direction
  for (let i = start; i < end; i++) m.commands[i] = scenario === 'auto' ? null : clone(cmd)
  return m
}

export type PlanStateKey = 'history' | 'draft' | 'manual' | 'auto'

export function hourPlanState(
  active: DeskModel,
  working: DeskModel,
  i: number,
): { key: PlanStateKey; label: string; short: string; detail: string } {
  if (i < 0) return { key: 'history', label: 'Історія', short: 'І', detail: 'Історія' }
  const applied = active.commands[i]
  const staged = working.commands[i]
  if (!same(applied, staged))
    return { key: 'draft', label: 'Чернетка', short: 'Ч', detail: 'Чернетка: ' + commandDesc(staged) + '; застосовано: ' + commandDesc(applied) }
  if (applied && applied.type !== 'auto') return { key: 'manual', label: 'Ручне', short: 'Р', detail: 'Застосовано: ' + commandDesc(applied) }
  return { key: 'auto', label: 'AUTO', short: 'A', detail: 'AUTO · автоматичний план' }
}

export type ActionGroup = {
  start: number
  end: number
  cmd: Command | null
  mark: ActionMark
  status: 'preview' | PlanStateKey
  key: string
}

// actionGroups merges consecutive hours with the same intent and state
// into the «Дія» band segments.
export function actionGroups(model: DeskModel, active: DeskModel, working: DeskModel, visible: number[]): ActionGroup[] {
  const groups: ActionGroup[] = []
  for (const i of visible) {
    if (i < 0) continue
    const cmd = model.commands[i]
    const pending = !same(cmd, working.commands[i])
    const status = pending ? 'preview' : hourPlanState(active, working, i).key
    const mark = actionMark(cmd)
    const key = JSON.stringify([mark.detail, status])
    const last = groups[groups.length - 1]
    if (last && last.key === key) last.end = i + 1
    else groups.push({ start: i, end: i + 1, cmd, mark, status, key })
  }
  return groups
}

// --- BESS flow split (internal/dispatch.SplitFlow) ----------------------

export type FlowKey = 'solar' | 'grid' | 'load' | 'export'

export const flowMeta: Record<FlowKey, { name: string; detail: string; color: string }> = {
  load: { name: 'Розряд → споживання', detail: 'на споживання', color: 'var(--d-flow-load)' },
  export: { name: 'Розряд → експорт', detail: 'на експорт', color: 'var(--d-flow-export)' },
  solar: { name: 'Заряд від сонця', detail: 'від СЕС', color: 'var(--d-flow-solar)' },
  grid: { name: 'Заряд від мережі', detail: 'із мережі', color: 'var(--d-flow-grid)' },
}

export function splitBessFlow(p: number, pv: number, load: number): { key: FlowKey; power: number }[] {
  if (![p, pv, load].every(hasValue)) return []
  if (p < 0) {
    const solar = Math.min(-p, Math.max(0, pv - load))
    return (
      [
        { key: 'solar', power: -solar },
        { key: 'grid', power: p + solar },
      ] as { key: FlowKey; power: number }[]
    ).filter((d) => Math.abs(d.power) > 0.000001)
  }
  const local = Math.min(p, Math.max(0, load - pv))
  return (
    [
      { key: 'load', power: local },
      { key: 'export', power: p - local },
    ] as { key: FlowKey; power: number }[]
  ).filter((d) => Math.abs(d.power) > 0.000001)
}

// --- chart points --------------------------------------------------------

export type ChartPoint = {
  p: number | null
  before: number | null
  soc: number | null
  import: number | null
  export: number | null
  curtailed: number | null
}

export const EMPTY_POINT: ChartPoint = { p: null, before: null, soc: null, import: null, export: null, curtailed: null }

export function pointFromResult(h: HourResult | undefined): ChartPoint {
  if (!h || h.unknown)
    return { ...EMPTY_POINT, before: h?.soc_before_pct ?? null }
  return {
    p: h.p_kw,
    before: h.soc_before_pct,
    soc: h.soc_pct,
    import: h.import_kw,
    export: h.export_kw,
    curtailed: h.curtailed_kw,
  }
}

// historyPoints turns the measured hours into chart points: BESS power,
// SOC at the end of each hour (before = end of the previous one), PCC.
export function historyPoints(hours: DeskHour[]): ChartPoint[] {
  const out: ChartPoint[] = []
  let prevSoc: number | null = null
  for (const h of hours) {
    if (h.offset >= 0) break
    const f = h.fact
    const grid = f?.grid_kw ?? null
    out.push({
      p: f?.ess_kw ?? null,
      before: prevSoc,
      soc: f?.soc_pct ?? null,
      import: hasValue(grid) ? Math.max(0, grid) : null,
      export: hasValue(grid) ? Math.max(0, -grid) : null,
      curtailed: null,
    })
    prevSoc = f?.soc_pct ?? prevSoc
  }
  return out
}

// --- review --------------------------------------------------------------

export type ReviewGroup = { start: number; end: number; text: string }

export function reviewGroups(active: DeskModel, working: DeskModel): { loads: ReviewGroup[]; commands: ReviewGroup[]; cfgChanged: boolean } {
  const commands: ReviewGroup[] = []
  for (let i = 0; i < FUTURE_HOURS; i++) {
    if (same(active.commands[i], working.commands[i])) continue
    const description = commandDesc(working.commands[i])
    const last = commands[commands.length - 1]
    if (last && last.end === i && last.text === description) last.end = i + 1
    else commands.push({ start: i, end: i + 1, text: description })
  }
  const loads: ReviewGroup[] = []
  for (let i = 0; i < FUTURE_HOURS; i++) {
    if (active.loads[i] === working.loads[i]) continue
    const v = working.loads[i]
    const text = 'Споживання: ' + (v === null ? 'не задано' : fmt(v) + ' кВт')
    const last = loads[loads.length - 1]
    if (last && last.end === i && last.text === text) last.end = i + 1
    else loads.push({ start: i, end: i + 1, text })
  }
  return { loads, commands, cfgChanged: !same(active.cfg, working.cfg) }
}

export function cfgSummary(cfg: Constraints): string {
  return (
    'Резерв ' +
    fmt(cfg.reserve_pct) +
    '% · імпорт ≤ ' +
    fmt(cfg.import_cap_kw) +
    ' кВт · експорт PCC ≤ ' +
    (cfg.block_export
      ? '0 кВт · повна заборона СЕС та УЗЕ'
      : cfg.export_cap_kw === null
        ? '0 кВт (ліміт не задано)'
        : fmt(cfg.export_cap_kw) + ' кВт') +
    ' · продаж УЗЕ ' +
    (cfg.ess_sale ? 'дозволено' : 'вимкнено') +
    ' · заряд із мережі ' +
    (cfg.grid_charge ? 'дозволено' : 'вимкнено')
  )
}

// unknownForecastText explains why the forecast stops: the first hour
// without operator load — or, in prod, without a published RDN price.
export function unknownForecastText(c: Clock, result: SimResult, model: DeskModel, hours: DeskHour[]): string {
  const i = result.known_hours
  const span = timeLabel(c, i) + '–' + timeLabel(c, i + 1)
  const future = hours.find((h) => h.offset === i)
  if (hasValue(model.loads[i]) && future && !hasValue(future.rdn_uah_per_kwh))
    return 'Немає ціни РДН на ' + span + ' (ще не опублікована). Далі немає прогнозу УЗЕ, мережі та SOC.'
  return 'Не задано споживання на ' + span + '. Через цю прогалину далі немає прогнозу УЗЕ, мережі та SOC.'
}

// --- draft persistence / time shift -------------------------------------

// shiftModel drops `hours` elapsed hours from the front and pads the
// end: a draft made at 15:00 keeps its meaning at 17:00.
export function shiftModel(m: DeskModel, hours: number): DeskModel {
  if (hours <= 0) return m
  const loads = m.loads.slice(hours)
  const commands = m.commands.slice(hours)
  while (loads.length < FUTURE_HOURS) loads.push(null)
  while (commands.length < FUTURE_HOURS) commands.push(null)
  return { loads, commands, cfg: m.cfg }
}

export function hoursBetween(fromISO: string, toISO: string): number {
  return Math.round((Date.parse(toISO) - Date.parse(fromISO)) / 3_600_000)
}

export function emptyModel(cfg: Constraints): DeskModel {
  return { loads: Array(FUTURE_HOURS).fill(null), commands: Array(FUTURE_HOURS).fill(null), cfg: clone(cfg) }
}

// normalizeModel pads/truncates wire arrays to the desk horizon.
export function normalizeModel(m: WireModel): DeskModel {
  const loads = Array.from({ length: FUTURE_HOURS }, (_, i) => (hasValue(m.loads?.[i]) ? (m.loads[i] as number) : null))
  const commands = Array.from({ length: FUTURE_HOURS }, (_, i) => m.commands?.[i] ?? null)
  return { loads, commands, cfg: clone(m.cfg) }
}
