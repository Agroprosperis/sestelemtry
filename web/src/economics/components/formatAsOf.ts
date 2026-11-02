const KYIV = 'Europe/Kyiv'

// economicsAsOfLabel is the header line that tells the operator when
// the numbers on screen were last computed. Same Kyiv calendar day as
// now drops the date, so today's open day reads «Дані станом на 13:16».
export function economicsAsOfLabel(iso: string, now = new Date()): string | null {
  const at = new Date(iso)
  if (Number.isNaN(at.getTime())) return null
  const day = new Intl.DateTimeFormat('en-CA', {
    timeZone: KYIV,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  })
  const time = new Intl.DateTimeFormat('uk-UA', {
    timeZone: KYIV,
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  }).format(at)
  if (day.format(at) === day.format(now)) {
    return `Дані станом на ${time}`
  }
  const date = new Intl.DateTimeFormat('uk-UA', {
    timeZone: KYIV,
    day: 'numeric',
    month: 'long',
  }).format(at)
  return `Дані станом на ${date}, ${time}`
}
