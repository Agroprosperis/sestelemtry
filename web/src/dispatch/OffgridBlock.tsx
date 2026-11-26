// OffgridBlock — «Без мережі» on the desk (ems_offgrid_advice.md §10):
// for each outage of the schedule in the horizon, the charge to have
// when it starts, where its load came from, and whether the plan holds
// it. Hints only; the operator decides.

import { useEffect, useState } from 'react'
import { fetchOffgridReserve, type LoadSource, type OffgridReserveResponse, type OutageReserve } from './offgridClient'

const REFRESH_MS = 5 * 60_000

const SOURCE: Record<LoadSource, string> = {
  plan: 'план енергетика',
  avg7d: 'середнє за 7 днів',
  mixed: 'план енергетика і середнє за 7 днів',
  none: 'немає ні плану, ні історії',
}

function clock(iso: string, timeZone: string, withDay: boolean): string {
  const opts: Intl.DateTimeFormatOptions = { hour: '2-digit', minute: '2-digit', hourCycle: 'h23', timeZone }
  if (withDay) Object.assign(opts, { day: '2-digit', month: '2-digit' })
  return new Intl.DateTimeFormat('uk-UA', opts).format(new Date(iso))
}

function Row({ r, tz, apply, floor }: { r: OutageReserve; tz: string; apply: boolean; floor: number }) {
  const span = clock(r.start, tz, true) + '–' + clock(r.end, tz, false)
  return (
    <li className={'d-offgrid-row' + (r.short ? ' warn' : '')}>
      <strong>
        {span}
        {r.kind === 'other' ? ' · позапланове' : ''}
      </strong>
      {r.need_kwh === null ? (
        <span>розрахунок неможливий ({SOURCE.none}): запас {r.target_pct} %</span>
      ) : (
        <span>
          заряд на початок <b>{r.target_pct} %</b> · потрібно {Math.round(r.need_kwh).toLocaleString('uk-UA')} кВт·год (
          {SOURCE[r.source]}, +{r.error_margin_pct} % на помилку плану) · від {floor} %
        </span>
      )}
      {r.short && (
        <span className="d-offgrid-warn">
          батареї вистачить приблизно до {r.runs_out_at ? clock(r.runs_out_at, tz, false) : '—'}, далі потрібен генератор
        </span>
      )}
      <span className="d-small">{apply ? 'у плані: враховано' : 'у плані: лише показ («Обмеження» → «Без мережі»)'}</span>
    </li>
  )
}

export function OffgridBlock({ site }: { site: string }) {
  const [data, setData] = useState<OffgridReserveResponse | null>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    const ctrl = new AbortController()
    const load = () =>
      fetchOffgridReserve(site, ctrl.signal)
        .then((d) => {
          setData(d)
          setError('')
        })
        .catch((e) => !ctrl.signal.aborted && setError(String(e)))
    void load()
    const id = window.setInterval(() => void load(), REFRESH_MS)
    return () => {
      ctrl.abort()
      window.clearInterval(id)
    }
  }, [site])
  if (error) return <p className="d-small d-error">Без мережі: {error}</p>
  if (!data || data.reserves.length === 0) return null
  return (
    <section className="d-offgrid" aria-label="Без мережі">
      <h3>Без мережі</h3>
      <ul>
        {data.reserves.map((r) => (
          <Row key={r.start + r.kind} r={r} tz={data.timezone} apply={data.settings.apply} floor={data.settings.island_soc_floor_pct} />
        ))}
      </ul>
    </section>
  )
}
