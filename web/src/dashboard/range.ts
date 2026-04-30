export type RangePreset = 'day' | 'month' | 'year'

export type RangeParams = {
  from: string
  to: string
  bucket: string
}

function toISO(date: Date): string {
  return date.toISOString()
}

export function rangeParams(preset: RangePreset, now: Date = new Date()): RangeParams {
  const to = new Date(now)
  const from = new Date(to)
  let bucket: string
  if (preset === 'month') {
    from.setDate(1)
    from.setHours(0, 0, 0, 0)
    bucket = '1 day'
  } else if (preset === 'year') {
    from.setMonth(0, 1)
    from.setHours(0, 0, 0, 0)
    bucket = '1 month'
  } else {
    from.setHours(0, 0, 0, 0)
    bucket = '1 hour'
  }
  return {
    from: toISO(from),
    to: toISO(to),
    bucket,
  }
}

export function dayRangeParams(now: Date = new Date()): RangeParams {
  const to = new Date(now)
  const from = new Date(to)
  from.setHours(0, 0, 0, 0)
  return {
    from: toISO(from),
    to: toISO(to),
    bucket: '15 minutes',
  }
}
