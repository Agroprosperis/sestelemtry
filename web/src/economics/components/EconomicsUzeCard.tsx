// EconomicsUzeCard — the day report's УЗЕ block. A past day shows the
// realised effect against the retrospective optimum (uze-plan) with the
// optimum's waterfall; today and tomorrow show the expected effect of
// the applied desk plan (dispatch/effect).

import { useEffect, useState } from 'react'
import {
  fetchDispatchEffect,
  fetchUzeDayPlan,
  type DispatchEffectResponse,
  type UzePlanResponse,
} from '../../api'

type Props = {
  organizationID: string
  date: string
  today: string
}

type Loaded = { key: string; plan?: UzePlanResponse; effect?: DispatchEffectResponse; error?: string }

const uahFmt = new Intl.NumberFormat('uk-UA', { maximumFractionDigits: 0 })
const kwhFmt = new Intl.NumberFormat('uk-UA', { maximumFractionDigits: 0 })
const pctFmt = new Intl.NumberFormat('uk-UA', { style: 'percent', maximumFractionDigits: 0 })

const signed = (v: number) => (v > 0.5 ? '+' : v < -0.5 ? '−' : '') + uahFmt.format(Math.round(Math.abs(v))) + ' грн'
const hourFmt = new Intl.DateTimeFormat('uk-UA', { timeZone: 'Europe/Kyiv', hour: '2-digit', minute: '2-digit' })
const span = (from: string, until: string) => {
  const end = hourFmt.format(new Date(until))
  return hourFmt.format(new Date(from)) + '–' + (end === '00:00' ? '24:00' : end)
}

// Missing access or a site without an edge device is not an error here:
// the block simply does not apply.
const quiet = (msg: string) => /: (403|404)\b/.test(msg)

function Row({ label, value, tone }: { label: string; value: number; tone?: 'pos' | 'neg' | 'total' }) {
  return (
    <div className={'eco-wf-row' + (tone ? ' ' + tone : '')}>
      <span>{label}</span>
      <strong>{signed(value)}</strong>
    </div>
  )
}

export function EconomicsUzeCard({ organizationID, date, today }: Props) {
  const past = date < today
  const key = organizationID + '|' + date
  const [loaded, setLoaded] = useState<Loaded | null>(null)

  useEffect(() => {
    if (!organizationID) return
    const ctrl = new AbortController()
    const req: Promise<Omit<Loaded, 'key'>> = past
      ? fetchUzeDayPlan({ organizationID, date }, ctrl.signal).then((plan) => ({ plan }))
      : fetchDispatchEffect({ organizationID, date }, ctrl.signal).then((effect) => ({ effect }))
    req
      .then((v) => setLoaded({ key, ...v }))
      .catch((e) => {
        if (!ctrl.signal.aborted) setLoaded({ key, error: String(e) })
      })
    return () => ctrl.abort()
  }, [key, past, organizationID, date])

  const cur = loaded && loaded.key === key ? loaded : null
  if (!cur || (cur.error && quiet(cur.error))) return null

  if (cur.error) {
    return (
      <section className="economics-card eco-overview-panel eco-uze-report" aria-label="УЗЕ за добу">
        <h3 className="eco-overview-title">УЗЕ за добу</h3>
        <p className="economics-month-muted">Дані недоступні: {cur.error}</p>
      </section>
    )
  }

  if (cur.plan) {
    const p = cur.plan
    const t = p.totals
    if (!p.available) {
      return (
        <section className="economics-card eco-overview-panel eco-uze-report" aria-label="УЗЕ: факт, оптимум, резерв">
          <h3 className="eco-overview-title">УЗЕ: факт / оптимум / резерв</h3>
          <p className="economics-month-muted">Оптимум недоступний: за добу немає придатної телеметрії або SOC.</p>
        </section>
      )
    }
    const legs = t.load_val_uah + t.export_val_uah - t.charge_pv_cost_uah - t.grid_cost_uah - t.degradation_uah
    const socLeg = t.optimum_uah - legs
    return (
      <section className="economics-card eco-overview-panel eco-uze-report" aria-label="УЗЕ: факт, оптимум, резерв">
        <div className="eco-overview-head">
          <h3 className="eco-overview-title">УЗЕ: факт / оптимум / резерв</h3>
          <span className="economics-month-muted">реалізовано {pctFmt.format(t.captured_share)}</span>
        </div>
        <div className="eco-uze-compare">
          <div>
            <span>Факт</span>
            <strong>{signed(t.fact_uah)}</strong>
          </div>
          <div>
            <span>Оптимум</span>
            <strong className="opt">{signed(t.optimum_uah)}</strong>
          </div>
          <div>
            <span>Резерв</span>
            <strong className="bad">{signed(t.reserve_uah)}</strong>
          </div>
        </div>
        <div className="eco-waterfall" aria-label="Складові оптимуму">
          <div className="eco-wf-title">Оптимум за добу — з чого складається</div>
          <Row label="УЗЕ → споживання (уникнений імпорт)" value={t.load_val_uah} tone="pos" />
          {t.export_val_uah > 0.5 && <Row label="УЗЕ → експорт" value={t.export_val_uah} tone="pos" />}
          {t.charge_pv_cost_uah > 0.5 && <Row label="Собівартість сонця (втрачений експорт)" value={-t.charge_pv_cost_uah} tone="neg" />}
          {t.grid_cost_uah > 0.5 && <Row label="Заряд з мережі" value={-t.grid_cost_uah} tone="neg" />}
          <Row label="Знос батареї" value={-t.degradation_uah} tone="neg" />
          {Math.abs(socLeg) >= 1 && <Row label="Зміна запасу SOC за добу" value={socLeg} tone={socLeg >= 0 ? 'pos' : 'neg'} />}
          <Row label="Оптимум" value={t.optimum_uah} tone="total" />
        </div>
        <p className="economics-month-muted">
          Оптимум — що дала б батарея за ідеального керування на фактичних СЕС, споживанні й цінах РДН цієї доби. Резерв — недобрана
          частина. Розряд в оптимумі {kwhFmt.format(t.discharge_kwh)} кВт·год, заряд з мережі {kwhFmt.format(t.charge_grid_kwh)} кВт·год.
        </p>
      </section>
    )
  }

  const r = cur.effect
  if (!r) return null
  const e = r.effect
  return (
    <section className="economics-card eco-overview-panel eco-uze-report" aria-label="Очікуваний ефект плану">
      <div className="eco-overview-head">
        <h3 className="eco-overview-title">Очікуваний ефект плану УЗЕ</h3>
        {r.version > 0 && <span className="economics-month-muted">план пульта · версія {r.version}</span>}
      </div>
      {!r.available || !e ? (
        <p className="economics-month-muted">{r.reason || 'Плану на цю дату немає.'}</p>
      ) : (
        <>
          <div className="eco-waterfall">
            <div className="eco-wf-title">
              {span(e.from, e.until)} · {e.hours} год плану: {signed(e.net_effect_uah)}
            </div>
            <Row label="УЗЕ → споживання (уникнений імпорт)" value={e.ess_to_load_uah} tone="pos" />
            {e.ess_to_grid_uah > 0.5 && <Row label="УЗЕ → експорт" value={e.ess_to_grid_uah} tone="pos" />}
            {e.pv_charge_cost_uah > 0.5 && (
              <Row label="Собівартість сонця (втрачений експорт)" value={-e.pv_charge_cost_uah} tone="neg" />
            )}
            {e.grid_charge_cost_uah > 0.5 && <Row label="Заряд з мережі" value={-e.grid_charge_cost_uah} tone="neg" />}
            <Row label="Знос батареї" value={-e.degradation_uah} tone="neg" />
            {Math.abs(e.soc_carry_uah) > 0.5 && (
              <Row
                label={
                  'Запас SOC (' + Math.round(e.soc_open_pct) + '% → ' + Math.round(e.soc_close_pct) + '%, за ' +
                  e.shadow_price_uah.toFixed(2) + ' грн/кВт·год)'
                }
                value={e.soc_carry_uah}
                tone={e.soc_carry_uah >= 0 ? 'pos' : 'neg'}
              />
            )}
            <Row label="Чистий ефект" value={e.net_effect_uah} tone="total" />
          </div>
          <div className="eco-uze-compare">
            <div>
              <span>Без УЗЕ</span>
              <strong>{uahFmt.format(Math.round(e.baseline_cost_uah))} грн</strong>
            </div>
            <div>
              <span>Імпорт з планом</span>
              <strong className="opt">{uahFmt.format(Math.round(e.plan_cost_uah))} грн</strong>
            </div>
            <div>
              <span>Заряд з мережі</span>
              <strong>{kwhFmt.format(e.charge_grid_kwh)} кВт·год</strong>
            </div>
          </div>
          <p className="economics-month-muted">
            LP застосованої версії від поточного SOC, лише години з введеним споживанням і ціною РДН. «Без УЗЕ» — прямий імпорт
            (споживання − СЕС).
          </p>
        </>
      )}
    </section>
  )
}
