// Instant input checks — the same rules and wording as
// internal/dispatch/validate.go (the server re-checks every preview and
// confirm; this only keeps «У чернетку» honest while a request is out).

import type { CommandType, Constraints, SiteInfo } from './dispatchClient'
import { fmt } from './model'

export function constraintsError(site: SiteInfo, cfg: Constraints, rawReserve: string, rawImport: string): string {
  if (rawReserve.trim() === '' || !Number.isFinite(cfg.reserve_pct) || cfg.reserve_pct < site.soc_min_pct || cfg.reserve_pct > site.soc_max_pct)
    return 'Резерв SOC має бути від ' + fmt(site.soc_min_pct) + '% до ' + fmt(site.soc_max_pct) + '%.'
  if (rawImport.trim() === '' || !Number.isFinite(cfg.import_cap_kw) || cfg.import_cap_kw < 0 || cfg.import_cap_kw > site.import_kw)
    return 'Ліміт імпорту має бути від 0 до ' + fmt(site.import_kw) + " кВт за паспортом об'єкта."
  if (cfg.export_cap_kw !== null) {
    if (!Number.isFinite(cfg.export_cap_kw) || cfg.export_cap_kw < 0) return 'Введіть невід’ємний погоджений ліміт експорту.'
    if (site.export_ceiling_kw === null)
      return "Для об'єкта не задано режим відпуску в мережу — ліміт експорту не можна встановити."
    if (cfg.export_cap_kw > site.export_ceiling_kw)
      return 'Ліміт експорту перевищує дозволені паспортом ' + fmt(site.export_ceiling_kw) + ' кВт.'
  }
  if (!cfg.block_export && cfg.export_cap_kw === null && cfg.ess_sale)
    return 'Вкажіть ліміт експорту в обмеженнях; без нього експорт вимкнений.'
  return ''
}

export function commandError(
  site: SiteInfo,
  cfg: Constraints,
  scenario: CommandType,
  rawValue: string,
  direction: 'charge' | 'discharge',
): string {
  if (scenario === 'export' && !cfg.block_export && cfg.export_cap_kw === null)
    return 'Вкажіть ліміт експорту в обмеженнях; без нього експорт вимкнений.'
  if (scenario === 'hold' || scenario === 'auto') return ''
  const v = Number(rawValue)
  if (rawValue.trim() === '' || !Number.isFinite(v) || v < 0) return 'Введіть невід’ємне числове значення.'
  if (scenario === 'target' && (v < cfg.reserve_pct || v > site.soc_max_pct))
    return 'Ціль SOC має бути між резервом та ' + fmt(site.soc_max_pct) + '%.'
  if (scenario === 'export' && cfg.export_cap_kw !== null && v > cfg.export_cap_kw)
    return 'Ціль експорту не може перевищувати ліміт PCC ' + fmt(cfg.export_cap_kw) + ' кВт.'
  if (scenario === 'cap' && v > site.import_kw) return 'Ціль імпорту не може перевищувати паспортні ' + fmt(site.import_kw) + ' кВт.'
  const charging = scenario === 'solar' || (scenario === 'fixed' && direction === 'charge')
  const powerMax = charging ? site.charge_kw : site.discharge_kw
  if ((scenario === 'cover' || scenario === 'solar' || scenario === 'fixed') && v > powerMax)
    return 'Паспортна межа ' + (charging ? 'заряду' : 'розряду') + ' — ' + fmt(powerMax) + ' кВт.'
  return ''
}
