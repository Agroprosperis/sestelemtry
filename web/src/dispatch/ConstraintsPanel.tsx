import type { Constraints, SiteInfo } from './dispatchClient'
import type { CfgForm } from './deskDraft'
import { fmt } from './model'

export function ConstraintsPanel({
  site,
  form,
  onForm,
  cfg,
  defaultReserve,
  stageDisabled,
  onStage,
}: {
  site: SiteInfo
  form: CfgForm
  onForm: (f: CfgForm) => void
  cfg: Constraints
  defaultReserve: number
  stageDisabled: boolean
  onStage: () => void
}) {
  const exportCeiling = site.export_ceiling_kw
  return (
    <details>
      <summary>Обмеження на весь горизонт</summary>
      <div className="d-constraints">
        <label className="d-field">
          Резерв SOC
          <span className="d-input-unit">
            <input
              type="number"
              min={site.soc_min_pct}
              max={site.soc_max_pct}
              step={5}
              value={form.reserve}
              onChange={(e) => onForm({ ...form, reserve: e.target.value })}
              aria-label="Резерв SOC на весь горизонт"
            />
            <span>%</span>
          </span>
        </label>
        <div className="d-rule-note">Спільний резерв для всієї чернетки; синхронізований із полем біля команди.</div>
        <label className="d-check">
          <input type="checkbox" checked={form.grid} onChange={(e) => onForm({ ...form, grid: e.target.checked })} />
          Дозволити заряд із мережі
        </label>
        <label className="d-check">
          <input type="checkbox" checked={form.blockExport} onChange={(e) => onForm({ ...form, blockExport: e.target.checked })} />
          Повністю заборонити експорт · СЕС та УЗЕ
        </label>
        <label className="d-check">
          <input
            type="checkbox"
            checked={form.essSale}
            disabled={form.blockExport || exportCeiling === null}
            onChange={(e) => onForm({ ...form, essSale: e.target.checked })}
          />
          Продаж енергії УЗЕ
        </label>
        <label className="d-field">
          Ліміт імпорту, кВт
          <input
            type="number"
            min={0}
            max={site.import_kw}
            step={10}
            value={form.importCap}
            onChange={(e) => onForm({ ...form, importCap: e.target.value })}
          />
        </label>
        <label className="d-field">
          Ліміт експорту PCC, кВт
          <input
            type="number"
            min={0}
            max={exportCeiling ?? undefined}
            step={10}
            placeholder="Не заданий"
            disabled={form.blockExport || exportCeiling === null}
            value={form.exportCap}
            onChange={(e) => onForm({ ...form, exportCap: e.target.value })}
          />
        </label>
        <div className="d-rule-note">
          Паспорт: імпорт до {site.import_set ? fmt(site.import_kw) + ' кВт' : 'не задано'}; заряд / розряд УЗЕ до {fmt(site.charge_kw)} /{' '}
          {fmt(site.discharge_kw)} кВт; ємність {fmt(site.capacity_kwh)} кВт·год; СЕС до {fmt(site.pv_rated_kw)} кВт AC. Вікно SOC{' '}
          {fmt(site.soc_min_pct)}–{fmt(site.soc_max_pct)}% за паспортом. Резерв за замовчуванням {fmt(defaultReserve)}% —
          налаштування «Обмежень», а не паспортна межа.
        </div>
        <div className="d-rule-note">
          <button type="button" disabled={stageDisabled} onClick={onStage}>
            У чернетку лише обмеження
          </button>
        </div>
        <div className="d-rule-note">
          {form.blockExport
            ? 'Експорт у точці приєднання = 0 кВт для СЕС і УЗЕ. Надлишок СЕС заряджає батарею, коли це можливо; решта обмежується. Після зняття заборони повернеться заданий ліміт.'
            : exportCeiling === null
              ? 'Режим відпуску в мережу для обʼєкта не обрано («Обмеження»), тому ліміту експорту немає й експорт вимкнений; надлишок СЕС обмежується.'
              : cfg.export_cap_kw === null
                ? 'Ліміт експорту не заданий. До його введення експорт вимкнений; надлишок СЕС обмежується.'
                : 'Експорт PCC до ' + fmt(cfg.export_cap_kw) + ' кВт; стеля за паспортом — ' + fmt(exportCeiling) +
                  ' кВт. Продаж енергії УЗЕ дозволяється окремо.'}
        </div>
      </div>
    </details>
  )
}
