import {
  Bar,
  Cell,
  ComposedChart,
  Line,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import type { PlanDayEffect, PlanPreview, PlanPreviewHour } from './plannerClient'

const COLORS = {
  rdn: '#94a3b8',
  pv: '#f59e0b',
  load: '#0ea5e9',
  discharge: '#16a34a',
  chargePv: '#f59e0b',
  chargeGrid: '#8b5cf6',
  soc: '#0f172a',
  reserve: '#dc2626',
  tomorrow: '#8b5cf6',
  positive: '#16a34a',
  negative: '#dc2626',
  total: '#2563eb',
}

function hourLabel(ts: string, timezone: string): string {
  return new Intl.DateTimeFormat('uk-UA', {
    timeZone: timezone,
    hour: '2-digit',
    hour12: false,
  }).format(new Date(ts))
}

function firstTomorrowTs(hours: PlanPreviewHour[]): string | undefined {
  return hours.find((h) => h.tomorrow)?.ts
}

const fmt1 = (v: number) => (Math.round(v * 10) / 10).toLocaleString('uk-UA')
const fmtUah = (v: number) =>
  `${Math.round(v).toLocaleString('uk-UA')} грн`

// ContextChart — «РДН + прогноз СЕС + план-load» (spec step-context):
// price columns on the right axis, PV forecast and planned load lines
// on the kW axis, with the «завтра» divider.
export function ContextChart({ preview }: { preview: PlanPreview }) {
  const data = preview.hours.map((h) => ({
    ts: h.ts,
    label: hourLabel(h.ts, preview.timezone),
    rdn: h.rdn_uah_per_kwh ?? null,
    pv: h.pv_kw,
    load: h.load_kw,
  }))
  const divider = firstTomorrowTs(preview.hours)
  return (
    <div className="planner-card">
      <h2>Контекст: ціни РДН, прогноз СЕС і план споживання</h2>
      <ResponsiveContainer width="100%" height={230}>
        <ComposedChart data={data} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
          <XAxis dataKey="label" tick={{ fontSize: 10 }} interval={1} />
          <YAxis
            yAxisId="kw"
            tick={{ fontSize: 10 }}
            width={44}
            label={{ value: 'кВт', angle: -90, position: 'insideLeft', fontSize: 11 }}
          />
          <YAxis
            yAxisId="uah"
            orientation="right"
            tick={{ fontSize: 10 }}
            width={40}
            label={{ value: 'грн/кВт·год', angle: 90, position: 'insideRight', fontSize: 11 }}
          />
          <Tooltip
            formatter={(value, name) => {
              const v = typeof value === 'number' ? value : Number(value)
              if (name === 'РДН') return [`${fmt1(v)} грн/кВт·год`, name]
              return [`${fmt1(v)} кВт`, name]
            }}
            labelFormatter={(l) => `${l}:00`}
          />
          <Bar yAxisId="uah" dataKey="rdn" name="РДН" fill={COLORS.rdn} opacity={0.45} />
          <Line
            yAxisId="kw"
            dataKey="pv"
            name="СЕС (прогноз)"
            stroke={COLORS.pv}
            strokeWidth={2}
            dot={false}
          />
          <Line
            yAxisId="kw"
            dataKey="load"
            name="Load (план)"
            stroke={COLORS.load}
            strokeWidth={2}
            dot={false}
          />
          {divider && (
            <ReferenceLine
              yAxisId="kw"
              x={hourLabel(divider, preview.timezone)}
              stroke={COLORS.tomorrow}
              strokeDasharray="5 4"
              label={{ value: 'завтра', fontSize: 11, fill: COLORS.tomorrow, position: 'top' }}
            />
          )}
        </ComposedChart>
      </ResponsiveContainer>
    </div>
  )
}

// PlanChart — step 3: hourly ESS dispatch (discharge up, charge down,
// grid/PV charge split), the SOC line with the reserve floor and the
// RDN price backdrop.
export function PlanChart({ preview }: { preview: PlanPreview }) {
  const data = preview.hours.map((h) => ({
    ts: h.ts,
    label: hourLabel(h.ts, preview.timezone),
    rdn: h.rdn_uah_per_kwh ?? null,
    discharge: h.discharge_kwh,
    chargePv: -h.charge_pv_kwh,
    chargeGrid: -h.charge_grid_kwh,
    soc: h.soc_end_pct,
  }))
  const divider = firstTomorrowTs(preview.hours)
  return (
    <div className="planner-card">
      <h2>Крок 3 — план заряд/розряд УЗЕ та SOC</h2>
      <p className="planner-card-sub">
        Зелений — розряд на споживання; бурштиновий — заряд від СЕС; фіолетовий — заряд з мережі.
        Чорна лінія — SOC, червона пунктирна — економічний резерв.
      </p>
      <ResponsiveContainer width="100%" height={280}>
        <ComposedChart data={data} margin={{ top: 8, right: 8, bottom: 0, left: 0 }} stackOffset="sign">
          <XAxis dataKey="label" tick={{ fontSize: 10 }} interval={1} />
          <YAxis
            yAxisId="kw"
            tick={{ fontSize: 10 }}
            width={44}
            label={{ value: 'кВт·год/год', angle: -90, position: 'insideLeft', fontSize: 11 }}
          />
          <YAxis yAxisId="soc" orientation="right" domain={[0, 100]} tick={{ fontSize: 10 }} width={36} />
          <YAxis yAxisId="rdn" hide domain={[0, 'dataMax']} />
          <Tooltip
            formatter={(value, name) => {
              const v = typeof value === 'number' ? value : Number(value)
              if (name === 'SOC') return [`${fmt1(v)} %`, name]
              if (name === 'РДН') return [`${fmt1(v)} грн/кВт·год`, name]
              return [`${fmt1(Math.abs(v))} кВт·год`, name]
            }}
            labelFormatter={(l) => `${l}:00`}
          />
          <Bar yAxisId="rdn" dataKey="rdn" name="РДН" fill={COLORS.rdn} opacity={0.18} />
          <Bar yAxisId="kw" dataKey="discharge" name="Розряд" stackId="ess" fill={COLORS.discharge} />
          <Bar yAxisId="kw" dataKey="chargePv" name="Заряд від СЕС" stackId="ess" fill={COLORS.chargePv} />
          <Bar yAxisId="kw" dataKey="chargeGrid" name="Заряд з мережі" stackId="ess" fill={COLORS.chargeGrid} />
          <Line yAxisId="soc" dataKey="soc" name="SOC" stroke={COLORS.soc} strokeWidth={2} dot={false} />
          <ReferenceLine
            yAxisId="soc"
            y={preview.params.soc_min_pct}
            stroke={COLORS.reserve}
            strokeDasharray="4 4"
            label={{
              value: `резерв ${preview.params.soc_min_pct}%`,
              fontSize: 10,
              fill: COLORS.reserve,
              position: 'insideBottomRight',
            }}
          />
          {divider && (
            <ReferenceLine
              yAxisId="kw"
              x={hourLabel(divider, preview.timezone)}
              stroke={COLORS.tomorrow}
              strokeDasharray="5 4"
              label={{ value: 'завтра', fontSize: 11, fill: COLORS.tomorrow, position: 'top' }}
            />
          )}
        </ComposedChart>
      </ResponsiveContainer>
    </div>
  )
}

type WaterfallItem = {
  name: string
  base: number
  delta: number
  color: string
}

// EffectWaterfall — «Очікуваний ефект за добу (завтра)» per spec §3/§4:
// battery flows priced hour-by-hour plus the shadow value of the SOC
// carried into the day after.
export function EffectWaterfall({ day, dateLabel }: { day: PlanDayEffect; dateLabel: string }) {
  const items: WaterfallItem[] = []
  let running = 0
  const push = (name: string, delta: number, color: string) => {
    const base = delta >= 0 ? running : running + delta
    items.push({ name, base, delta: Math.abs(delta), color })
    running += delta
  }
  push('УЗЕ → споживання', day.ess_to_load_uah, COLORS.positive)
  push('Заряд з мережі', -day.grid_charge_cost_uah, COLORS.negative)
  push('Собівартість сонця', -day.pv_charge_cost_uah, COLORS.negative)
  push('Знос', -day.degradation_uah, COLORS.negative)
  push('Резерв SOC на наст. добу', day.soc_carry_uah, day.soc_carry_uah >= 0 ? COLORS.positive : COLORS.negative)
  items.push({ name: 'Чистий ефект', base: 0, delta: running, color: COLORS.total })

  const negative = day.net_effect_uah < 0
  return (
    <div className="planner-card">
      <h2>Очікуваний ефект за добу (завтра {dateLabel})</h2>
      <div className={'planner-effect-headline' + (negative ? ' negative' : '')}>
        {day.net_effect_uah >= 0 ? '+' : ''}
        {fmtUah(day.net_effect_uah)}
      </div>
      <div className="planner-effect-compare">
        Без УЗЕ: {fmtUah(day.baseline_cost_uah)} → з планом: {fmtUah(day.plan_cost_uah)}; SOC {fmt1(day.soc_open_pct)}% →{' '}
        {fmt1(day.soc_close_pct)}% (перенос {day.soc_carry_uah >= 0 ? '+' : ''}
        {fmtUah(day.soc_carry_uah)}). Розряд {fmt1(day.ess_to_load_kwh)} кВт·год, заряд від СЕС{' '}
        {fmt1(day.charge_pv_kwh)}, з мережі {fmt1(day.charge_grid_kwh)}.
      </div>
      <ResponsiveContainer width="100%" height={230}>
        <ComposedChart data={items} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
          <XAxis dataKey="name" tick={{ fontSize: 10 }} interval={0} />
          <YAxis tick={{ fontSize: 10 }} width={56} tickFormatter={(v) => Math.round(Number(v)).toLocaleString('uk-UA')} />
          <Tooltip
            formatter={(value, name, entry) => {
              if (name === 'base') return [null, null]
              const item = entry?.payload as WaterfallItem | undefined
              return [fmtUah((item?.delta ?? Number(value)) * (item && item.color === COLORS.negative ? -1 : 1)), item?.name]
            }}
          />
          <Bar dataKey="base" stackId="wf" fill="transparent" isAnimationActive={false} />
          <Bar dataKey="delta" stackId="wf" isAnimationActive={false}>
            {items.map((it) => (
              <Cell key={it.name} fill={it.color} />
            ))}
          </Bar>
        </ComposedChart>
      </ResponsiveContainer>
    </div>
  )
}
