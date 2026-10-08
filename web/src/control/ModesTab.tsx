// ModesTab — the «Режими» tab: which edge preset is running and why.
// The cloud publishes only the desk plan (economic_arbitrage +
// plan.intervals); an hour without an interval falls back to
// self-consumption on the edge, a lost manifest to the safe preset.
// Manual control lives in «План УЗЕ».

import type { EdgeSiteStatus } from './controlClient'

type Props = {
  status: EdgeSiteStatus | null
  onOpenDesk: () => void
}

type ModeKey = 'plan' | 'self_consumption' | 'self_consumption_safe'

const MODE_CARDS: { key: ModeKey; preset: string; title: string; text: string }[] = [
  {
    key: 'plan',
    preset: 'economic_arbitrage',
    title: 'План пульта',
    text: 'Edge виконує погодинний план із «План УЗЕ». Хмара перераховує його кожні 15 хв від поточного SOC на години з введеним споживанням і ціною РДН.',
  },
  {
    key: 'self_consumption',
    preset: 'self_consumption',
    title: 'Самоспоживання',
    text: 'Години без плану: заряд від надлишку СЕС, розряд у локальний дефіцит, без арбітражу. Так edge працює, поки в пульті не введено споживання.',
  },
  {
    key: 'self_consumption_safe',
    preset: 'self_consumption_safe',
    title: 'Безпечний',
    text: 'Самоспоживання без заряду з мережі за будь-яких умов. Режим за замовчуванням при втраті manifest.',
  },
]

// activeMode reads the edge's latest decision: an arbitrage manifest
// whose current hour has no interval runs self-consumption (no_plan_*).
function activeMode(status: EdgeSiteStatus | null): ModeKey | null {
  const decision = status?.decision?.record
  const preset = decision?.preset ?? status?.manifest?.payload?.preset
  if (preset === 'self_consumption' || preset === 'self_consumption_safe') return preset
  if (preset !== 'economic_arbitrage') return null
  return decision?.reason_code?.startsWith('no_plan_') ? 'self_consumption' : 'plan'
}

export function ModesTab({ status, onOpenDesk }: Props) {
  const active = activeMode(status)
  const payload = status?.manifest?.payload
  const intervals = payload?.plan?.intervals?.length ?? 0
  return (
    <div style={{ display: 'grid', gap: 20 }}>
      <div className="ctl-shadow-note">
        SHADOW: edge рахує команди у тіні, фізично керує Encombi. Запису в SmartLogger немає.
      </div>

      <section className="ctl-card">
        <h2>Режими роботи</h2>
        <p className="ctl-card-sub">
          Чинний manifest: <strong>{payload?.note || payload?.manifest_id || 'немає'}</strong>
          {payload ? ` · ${intervals} год плану` : ''}. Режим змінюється через план у пульті, окремого
          перемикання пресетів немає.
        </p>
        <div className="ctl-modes-grid">
          {MODE_CARDS.map((m) => {
            const isActive = active === m.key
            return (
              <div key={m.key} className={'ctl-mode-card' + (isActive ? ' active' : '')}>
                <h3>
                  {m.title}
                  {isActive && <span className="ctl-chip ok">зараз</span>}
                </h3>
                <p>{m.text}</p>
                <p className="ctl-card-sub mono">{m.preset}</p>
              </div>
            )
          })}
        </div>
        <div className="ctl-form-actions">
          <button type="button" className="ctl-btn primary" onClick={onOpenDesk}>
            Відкрити пульт «План УЗЕ»
          </button>
        </div>
      </section>
    </div>
  )
}
