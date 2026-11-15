// Texts of the generator's fuel (ems-spec generator handoff §4.4.1), shared
// by the tariff form, the economics warnings and the station passport.

export const FUEL_PRICE_HELP =
  'Ціна літра без ПДВ. ПДВ додається так само, як до ціни електроенергії з мережі. ' +
  'Нова ціна — нова версія тарифу з датою початку дії; попередні дні не перераховуються.'

export const FUEL_METHOD_PER_KWH_HELP =
  'Пальне рахується від енергії, яку видав генератор, за його лічильником. Підходить, коли навантаження ' +
  'змінюється. Не враховує витрату на холостому ході. Енергія СЕС і УЗЕ, спожита без мережі, оцінюється ' +
  'ціною дизельної кВт·год: кожна така кіловат-година — це пальне, якого не спалили.'

export const FUEL_METHOD_PER_HOUR_HELP =
  'Пальне рахується від часу роботи генератора, незалежно від навантаження. Підходить, коли пальне ' +
  'обліковують за мотогодинами, а навантаження приблизно стале. Ефект СЕС і УЗЕ — лише час без мережі, ' +
  'коли генератор не запускався. Поки генератор працює, СЕС економії не показує, а заряд УЗЕ від ' +
  'генератора вважається безкоштовним.'

export const FUEL_CONSUMPTION_PER_KWH_HELP =
  'Скільки літрів іде на одну кіловат-годину. Узяти з фактичних заправок: літри за період поділити на ' +
  'кВт·год генератора за той самий період.'

export const FUEL_CONSUMPTION_PER_HOUR_HELP =
  'Скільки літрів іде за годину роботи при звичному навантаженні. Узяти з фактичних заправок: літри за ' +
  'період поділити на години роботи.'

export const FUEL_COST_HELP =
  'Ціна × витрата × ПДВ. За цією цифрою рахуються витрати на генератор і ефект СЕС та УЗЕ в години без мережі.'

// FUEL_METHODS_LINE is the one-line explanation of the two methods.
export const FUEL_METHODS_LINE =
  'За кВт·год пальне рахується від енергії генератора за його лічильником, за годину роботи — від часу роботи генератора.'

// fuelMethodLabel names a day's or a rollup's fuel method.
export function fuelMethodLabel(method: string | undefined): string {
  switch (method) {
    case 'per_kwh':
      return 'за кВт·год'
    case 'per_hour':
      return 'за годину роботи'
    case 'mixed':
      return 'обидва способи'
    default:
      return ''
  }
}

// rollupOffgridMinutes are a rollup's minutes without the grid.
export function rollupOffgridMinutes(t: {
  minutes_generator?: number
  minutes_island?: number
  minutes_blackout?: number
}): number {
  return (t.minutes_generator ?? 0) + (t.minutes_island ?? 0) + (t.minutes_blackout ?? 0)
}

// hasRollupGen reports whether a rollup holds any generator data.
export function hasRollupGen(t: { gen_kwh?: number; minutes_generator?: number; minutes_island?: number; minutes_blackout?: number }): boolean {
  return (t.gen_kwh ?? 0) > 0 || rollupOffgridMinutes(t) > 0
}

// formatDuration renders minutes as «1 год 30 хв».
export function formatDuration(minutes: number): string {
  if (!(minutes > 0)) return '0 хв'
  const h = Math.floor(minutes / 60)
  const m = Math.round(minutes - h * 60)
  if (h === 0) return `${m} хв`
  return m === 0 ? `${h} год` : `${h} год ${m} хв`
}
