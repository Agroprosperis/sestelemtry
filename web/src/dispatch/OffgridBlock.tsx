// OffgridBlock — «Без мережі» on the desk (ems_offgrid_advice.md §10):
// the charge for each outage of the schedule, the power at hand, the
// advice when the grid goes, the generator's charge hint, and plan
// against fact. Hints only; the operator decides.

import { useEffect, useState } from 'react'
import {
  fetchOffgridAdvice,
  fetchOffgridOutages,
  fetchOffgridReserve,
  type Advice,
  type LoadSource,
  type OffgridAdviceResponse,
  type OffgridReserveResponse,
  type OutageReserve,
  type OutagesResponse,
} from './offgridClient'

const REFRESH_MS = 5 * 60_000
// While the grid is gone the advice line moves every five minutes (§6.4).
const REFRESH_OFF_MS = 60_000

const SOURCE: Record<LoadSource, string> = {
  plan: 'план енергетика',
  avg7d: 'середнє за 7 днів',
  mixed: 'план енергетика і середнє за 7 днів',
  none: 'немає ні плану, ні історії',
}

function clock(iso: string | undefined, timeZone: string, withDay = false): string {
  if (!iso) return '—'
  const opts: Intl.DateTimeFormatOptions = { hour: '2-digit', minute: '2-digit', hourCycle: 'h23', timeZone }
  if (withDay) Object.assign(opts, { day: '2-digit', month: '2-digit' })
  return new Intl.DateTimeFormat('uk-UA', opts).format(new Date(iso))
}

const uah = (v: number | null | undefined) => (v === null || v === undefined ? '—' : Math.round(v).toLocaleString('uk-UA') + ' грн')
const dur = (min: number | undefined) => (min === undefined ? '—' : Math.floor(min / 60) + ' год ' + Math.round(min % 60) + ' хв')

function ReserveRow({ r, tz, apply, floor }: { r: OutageReserve; tz: string; apply: boolean; floor: number }) {
  const span = clock(r.start, tz, true) + '–' + clock(r.end, tz)
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
          батареї вистачить приблизно до {clock(r.runs_out_at, tz)}, далі потрібен генератор
        </span>
      )}
      <span className="d-small">{apply ? 'у плані: враховано' : 'у плані: лише показ («Обмеження» → «Без мережі»)'}</span>
    </li>
  )
}

function AdviceTable({ a, tz }: { a: Advice; tz: string }) {
  return (
    <table className="d-offgrid-advice">
      <tbody>
        {a.variants.map((v) => (
          <tr key={v.id} className={v.id === a.recommended ? 'rec' : v.feasible ? '' : 'off'}>
            <th>
              {v.id}. {v.label}
              {v.id === a.recommended ? ' — порада' : ''}
            </th>
            {v.feasible ? (
              <td>
                {uah(v.cost_uah)} · пальне {v.fuel_l.toLocaleString('uk-UA')} л · зупинок {v.stops} · заряд на кінець {v.end_soc_pct} %
                {v.switch_at ? ' · генератор з ' + clock(v.switch_at, tz) : ''}
                {(v.notes ?? []).length > 0 ? ' · ' + (v.notes ?? []).join('; ') : ''}
              </td>
            ) : (
              <td>неможливо: {v.why}</td>
            )}
          </tr>
        ))}
      </tbody>
    </table>
  )
}

function AdviceView({ adv }: { adv: OffgridAdviceResponse }) {
  const tz = adv.timezone
  const a = adv.available
  const o = adv.outage
  return (
    <>
      {a && (
        <p className="d-offgrid-line">
          Острів зараз: до {a.island_kw.toLocaleString('uk-UA')} кВт · з генератором: до {a.generator_kw.toLocaleString('uk-UA')} кВт ·
          УЗЕ вистачить на {dur(a.autonomy_now_min)} при поточному споживанні
          {a.autonomy_plan_min !== undefined ? ', за планом — ' + dur(a.autonomy_plan_min) : ''}
          {a.start_generator_by ? '; генератор запускати не пізніше ' + clock(a.start_generator_by, tz) : ''}
        </p>
      )}
      {o && (
        <div className="d-offgrid-card">
          <strong>
            {o.final ? 'Порада зараз' : 'Попередня порада'} на {clock(o.start, tz, true)}–{clock(o.end, tz)} · заряд {Math.round(o.soc_pct)} % ·
            дозаряд {o.recharge_uah_per_kwh.toLocaleString('uk-UA')} грн/кВт·год{o.recharge_estimated ? ' (оцінка)' : ''}
          </strong>
          {o.advice.limit_load_kw !== undefined && (
            <p className="d-offgrid-warn">Потрібно обмежити навантаження до {o.advice.limit_load_kw} кВт: жоден варіант не покриває все.</p>
          )}
          <AdviceTable a={o.advice} tz={tz} />
          {o.advice.why && <p className="d-small">{o.advice.why}</p>}
        </div>
      )}
      {(adv.unscheduled ?? []).length > 0 && (
        <div className="d-offgrid-card">
          <strong>Відключення поза графіком: тривалість невідома</strong>
          {(adv.unscheduled ?? []).map((u) => {
            const rec = u.advice.variants.find((v) => v.id === u.advice.recommended)
            return (
              <p key={u.hours} className="d-small">
                {u.hours} год: {rec ? rec.label + ', ' + uah(rec.cost_uah) : 'немає варіанта з відомою вартістю'}
              </p>
            )
          })}
        </div>
      )}
      {adv.gen_charge && (
        <p className="d-offgrid-line">
          Заряд від генератора: ціль {adv.gen_charge.target_kw} кВт — {adv.gen_charge.why}. Наприкінці відключення за графіком заряд від
          генератора припиняється.
        </p>
      )}
    </>
  )
}

function FactView({ data }: { data: OutagesResponse }) {
  const s = data.summary
  const tz = data.timezone
  return (
    <div className="d-offgrid-card">
      <strong>
        План і факт {data.from} … {data.to}: відключень {s.outages} (поза графіком {s.unscheduled}) · без мережі{' '}
        {s.hours_offgrid.toLocaleString('uk-UA')} год · пальне {s.fuel_l.toLocaleString('uk-UA')} л, {uah(s.fuel_uah)} · фактично{' '}
        {uah(s.cost_fact_uah)}, за порадами було б {uah(s.cost_advised_uah)}
      </strong>
      <p className="d-small">
        Запас на помилку плану: {data.error_margin_pct} %
        {s.with_plan < 10 ? ` (з факту — після 10 відключень із планом; зараз ${s.with_plan})` : ' (з факту)'}
      </p>
      <table className="d-offgrid-advice">
        <tbody>
          {data.records.map((r, k) => (
            <tr key={k}>
              <th>
                {r.start ? clock(r.start, tz, true) + '–' + clock(r.end, tz) : clock(r.scheduled_start, tz, true) + ' (за графіком)'}
                {r.kind === 'unscheduled' ? ' · поза графіком' : ''}
              </th>
              {r.happened ? (
                <td>
                  {(r.sequence ?? []).join(' → ')} · генератор {Math.round(r.minutes_generator)} хв, {r.gen_kwh} кВт·год, {r.fuel_l} л ·
                  без живлення {r.minutes_blackout} хв · споживання {r.load_fact_kwh} кВт·год
                  {r.load_plan_kwh !== undefined ? ' проти плану ' + r.load_plan_kwh + ' (' + (r.deviation_pct ?? 0) + ' %)' : ''} · фактично{' '}
                  {uah(r.cost_fact_uah)}
                  {r.advised ? ' · порада ' + r.advised : ''}
                </td>
              ) : (
                <td>мережа не зникала</td>
              )}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

export function OffgridBlock({ site }: { site: string }) {
  const [data, setData] = useState<OffgridReserveResponse | null>(null)
  const [adv, setAdv] = useState<OffgridAdviceResponse | null>(null)
  const [fact, setFact] = useState<OutagesResponse | null>(null)
  const [error, setError] = useState('')
  const off = !!adv?.live.online && ['blackout', 'island', 'generator'].includes(adv.live.mode ?? '')
  useEffect(() => {
    const ctrl = new AbortController()
    const load = () => {
      fetchOffgridReserve(site, ctrl.signal)
        .then((d) => {
          setData(d)
          setError('')
        })
        .catch((e) => !ctrl.signal.aborted && setError(String(e)))
      fetchOffgridAdvice(site, ctrl.signal)
        .then(setAdv)
        .catch(() => {})
    }
    load()
    const id = window.setInterval(load, off ? REFRESH_OFF_MS : REFRESH_MS)
    return () => {
      ctrl.abort()
      window.clearInterval(id)
    }
  }, [site, off])
  if (error) return <p className="d-small d-error">Без мережі: {error}</p>
  const any = (data && data.reserves.length > 0) || adv?.outage || adv?.available || (adv?.unscheduled ?? []).length > 0
  return (
    <section className="d-offgrid" aria-label="Без мережі">
      <h3>Без мережі</h3>
      {data && data.reserves.length > 0 && (
        <ul>
          {data.reserves.map((r) => (
            <ReserveRow key={r.start + r.kind} r={r} tz={data.timezone} apply={data.settings.apply} floor={data.settings.island_soc_floor_pct} />
          ))}
        </ul>
      )}
      {!any && <p className="d-small">Відключень за графіком у горизонті пульта немає.</p>}
      {adv && <AdviceView adv={adv} />}
      {fact ? (
        <FactView data={fact} />
      ) : (
        <button
          type="button"
          className="d-quiet"
          onClick={() =>
            void fetchOffgridOutages(site)
              .then(setFact)
              .catch((e) => setError(String(e)))
          }
        >
          План і факт за місяць →
        </button>
      )}
    </section>
  )
}
