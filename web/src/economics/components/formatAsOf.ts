const KYIV = 'Europe/Kyiv'

const kyivDate = new Intl.DateTimeFormat('en-CA', {
  timeZone: KYIV,
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
})

const kyivClock = new Intl.DateTimeFormat('uk-UA', {
  timeZone: KYIV,
  hour: '2-digit',
  minute: '2-digit',
  hourCycle: 'h23',
})

const kyivHour = new Intl.DateTimeFormat('en-GB', {
  timeZone: KYIV,
  hour: 'numeric',
  hourCycle: 'h23',
})

const kyivDayMonth = new Intl.DateTimeFormat('uk-UA', {
  timeZone: KYIV,
  day: 'numeric',
  month: 'long',
})

// AsOfScope names the viewed period in the «Дані за весь …» wording.
export type AsOfScope = 'day' | 'month' | 'year' | 'period'

const WHOLE: Record<AsOfScope, string> = {
  day: 'весь день',
  month: 'весь місяць',
  year: 'весь рік',
  period: 'весь період',
}

// daypart spells out the time of day so «03:00» is not read as 3 p.m.
function daypart(hour: number): string {
  if (hour < 5) return 'ночі'
  if (hour < 12) return 'ранку'
  if (hour < 17) return 'дня'
  return 'вечора'
}

// economicsAsOfLabel is the header line that says how current the
// numbers on screen are. lastDay (YYYY-MM-DD) is the viewed period's
// final Kyiv day: a stamp from a later day means the period had already
// ended when it was computed, so the numbers cover all of it; otherwise
// they stop at the stamp.
export function economicsAsOfLabel(
  iso: string,
  lastDay: string,
  scope: AsOfScope,
  now = new Date(),
): string | null {
  const at = new Date(iso)
  if (Number.isNaN(at.getTime())) return null
  const stampDay = kyivDate.format(at)
  const day = stampDay === kyivDate.format(now) ? 'сьогодні' : kyivDayMonth.format(at)
  const clock = `${kyivClock.format(at)} ${daypart(Number(kyivHour.format(at)))}`
  if (stampDay > lastDay) {
    return `Дані за ${WHOLE[scope]}, розраховано ${day} о ${clock}`
  }
  return `Дані станом на ${day}, ${clock}`
}
