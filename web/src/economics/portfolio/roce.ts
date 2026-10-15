import type { EconomicsPortfolioSite } from '../../api'

export const ROCE_TIP =
  'EBITDA періоду, приведена до року (×12 / кількість місяців із даними), поділена на інвестований капітал ' +
  '(CAPEX із тарифів, що діє на кінець періоду). Неповний період завищує оцінку — точна окупність на сторінці «Окупність». ' +
  'Для портфеля: сума приведених EBITDA об’єктів із CAPEX до їхнього сумарного CAPEX.'

// annualEbitda scales a site's period EBITDA to a full year, the same
// way the ROCE card on the object's own economics page does.
function annualEbitda(site: EconomicsPortfolioSite): number {
  const months = Math.max(1, site.months_with_data ?? 1)
  return site.ebitda_uah * (12 / months)
}

function hasCapex(site: EconomicsPortfolioSite): boolean {
  return site.has_data && Number.isFinite(site.capex_uah) && (site.capex_uah as number) > 0
}

// siteRoce is NaN (rendered «—») for an object without data or CAPEX.
export function siteRoce(site: EconomicsPortfolioSite): number {
  return hasCapex(site) ? annualEbitda(site) / (site.capex_uah as number) : NaN
}

// portfolioRoce weighs the objects by their capital: annualised EBITDA
// summed over the objects with CAPEX, over that CAPEX.
export function portfolioRoce(sites: EconomicsPortfolioSite[]): number {
  let ebitda = 0
  let capex = 0
  for (const s of sites) {
    if (!hasCapex(s)) continue
    ebitda += annualEbitda(s)
    capex += s.capex_uah as number
  }
  return capex > 0 ? ebitda / capex : NaN
}
