import type { ReactNode } from 'react'
import { FUEL_METHODS_LINE } from '../generatorText'

// GenCostMissingBanner is the gen_cost_missing warning: the generator ran
// on days whose tariff prices no fuel. It opens the tariff form, where the
// fuel is set from a date.
export function GenCostMissingBanner({ children, onOpenTariffs }: { children: ReactNode; onOpenTariffs: () => void }) {
  return (
    <section className="economics-banner" role="status">
      {children} Задайте ціну літра, спосіб обліку й витрату в розкладі тарифів. {FUEL_METHODS_LINE}{' '}
      <button type="button" className="economics-banner-action" onClick={onOpenTariffs}>
        Відкрити тарифи
      </button>
    </section>
  )
}
