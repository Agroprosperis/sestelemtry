// SettingsTab — the «Обмеження» tab: per-site SOC policy, power and
// grid limits (GET/PUT /api/v1/edge/settings). Saved values feed the
// desk LP and every next manifest; «Опублікувати» pushes them to the
// edge immediately instead of waiting for the 15-minute cycle.

import { useEffect, useState } from 'react'
import {
  exportCeilingKw,
  fetchEdgeSettings,
  publishAutoManifest,
  saveEdgeSettings,
  type EdgeSiteSettings,
  type ExportRegime,
} from './controlClient'

type Props = {
  site: string
  // canEdit is technical.write on the site; without it the limits are
  // shown read-only.
  canEdit?: boolean
  onChanged: () => void
}

type FieldKey = Exclude<keyof EdgeSiteSettings, 'export_regime' | 'outage_reserve_apply'>

// active_consumer_export_power_cap.md: which paragraph applies is a
// per-site legal choice; until it is made no export limit exists.
const EXPORT_REGIMES: { value: ExportRegime; label: string }[] = [
  { value: '', label: 'Не задано — експорт вимкнено' },
  { value: 'self_production', label: 'Самовиробництво — 50 % договірної потужності' },
  { value: 'storage', label: 'УЗЕ не за самовиробництвом — 100 % договірної потужності' },
]

const FIELDS: { key: FieldKey; label: string; hint: string }[] = [
  { key: 'soc_target_pct', label: 'Цільовий SOC, %', hint: 'верхня межа економічного циклу (SocMax)' },
  { key: 'soc_reserve_pct', label: 'Резерв SOC за замовчуванням, %', hint: 'стартове значення для чернетки пульта; діючий резерв задає пульт' },
  { key: 'auto_charge_max_kw', label: 'Заряд макс., кВт', hint: 'ліміт заряду в AUTO' },
  { key: 'auto_discharge_max_kw', label: 'Розряд макс., кВт', hint: 'ліміт розряду в AUTO' },
  { key: 'grid_import_kw', label: 'Ліміт імпорту, кВт', hint: 'договірна межа з мережі' },
  { key: 'grid_target_kw', label: 'Цільовий імпорт, кВт', hint: 'бажаний рівень споживання з мережі' },
  { key: 'pv_rated_kw', label: 'СЕС номінал, кВт', hint: 'для прогнозу генерації (fallback)' },
  { key: 'island_charge_max_kw', label: 'Заряд (острів), кВт', hint: 'резерв на автономний режим' },
  { key: 'island_discharge_max_kw', label: 'Розряд (острів), кВт', hint: 'резерв на автономний режим' },
  { key: 'export_pcc_kw', label: 'Технічний ліміт експорту PCC, кВт', hint: 'межа точки приєднання; порожньо — немає' },
]

// Work without the grid (ems_offgrid_advice.md §12); empty = the spec's value.
const OFFGRID_FIELDS: { key: FieldKey; label: string; hint: string }[] = [
  { key: 'island_soc_floor_pct', label: 'Нижня межа заряду без мережі, %', hint: 'типово 20' },
  { key: 'outage_reserve_max_pct', label: 'Верхня межа запасу, %', hint: 'типово 90' },
  { key: 'outage_error_margin_pct', label: 'Запас на помилку плану, %', hint: 'типово 20' },
  { key: 'generator_switch_soc_pct', label: 'Заряд для переходу на генератор, %', hint: 'типово 30' },
  { key: 'generator_min_kw', label: 'Мінімум генератора, кВт', hint: 'типово 220 (30 %)' },
  { key: 'generator_target_kw', label: 'Ціль генератора, кВт', hint: 'типово 480 (60–70 %)' },
  { key: 'generator_rated_kw', label: 'Номінал генератора, кВт', hint: 'типово 734' },
  { key: 'island_power_max_kw', label: 'Потужність УЗЕ в острові, кВт', hint: 'типово 576 (2/3 номіналу)' },
  { key: 'extra_stop_threshold_uah', label: 'Поріг вигоди зайвої зупинки, грн', hint: 'типово 1 000' },
]

const ALL_FIELDS = [...FIELDS, ...OFFGRID_FIELDS]

export function SettingsTab({ site, canEdit = true, onChanged }: Props) {
  const [form, setForm] = useState<Record<string, string>>({})
  const [regime, setRegime] = useState<ExportRegime>('')
  const [apply, setApply] = useState(false)
  const [saved, setSaved] = useState(false)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState('')
  const [error, setError] = useState('')

  // The parent keys the tab by site, so a site switch remounts it with
  // loading on and no error.
  useEffect(() => {
    let cancelled = false
    fetchEdgeSettings(site)
      .then((res) => {
        if (cancelled) return
        const next: Record<string, string> = {}
        for (const f of ALL_FIELDS) {
          const v = res.settings[f.key]
          next[f.key] = v != null && v !== 0 ? String(v) : ''
        }
        setForm(next)
        setRegime(res.settings.export_regime ?? '')
        setApply(res.settings.outage_reserve_apply ?? false)
        setSaved(res.saved)
      })
      .catch((e) => !cancelled && setError(String(e)))
      .finally(() => !cancelled && setLoading(false))
    return () => {
      cancelled = true
    }
  }, [site])

  const parse = (): EdgeSiteSettings | null => {
    const out: EdgeSiteSettings = {}
    for (const f of ALL_FIELDS) {
      const raw = (form[f.key] ?? '').trim()
      if (raw === '') continue
      const v = Number(raw.replace(',', '.'))
      if (!Number.isFinite(v) || v < 0) {
        setError(`«${f.label}» має бути невід'ємним числом`)
        return null
      }
      out[f.key] = v
    }
    if (regime) out.export_regime = regime
    if (apply) out.outage_reserve_apply = true
    return out
  }

  const preview: EdgeSiteSettings = { export_regime: regime }
  for (const key of ['grid_import_kw', 'export_pcc_kw'] as const) {
    const v = Number((form[key] ?? '').replace(',', '.'))
    if (Number.isFinite(v) && v > 0) preview[key] = v
  }
  const ceiling = exportCeilingKw(preview)

  const save = async () => {
    const settings = parse()
    if (!settings) return
    setBusy(true)
    setError('')
    setNotice('')
    try {
      await saveEdgeSettings(site, settings)
      setSaved(true)
      setNotice('Збережено. Значення підуть у наступний manifest (перерахунок кожні 15 хв).')
      onChanged()
    } catch (e) {
      setError(String(e))
    } finally {
      setBusy(false)
    }
  }

  const saveAndPublish = async () => {
    const settings = parse()
    if (!settings) return
    setBusy(true)
    setError('')
    setNotice('')
    try {
      await saveEdgeSettings(site, settings)
      setSaved(true)
      const res = await publishAutoManifest(site)
      if (res.published) {
        setNotice(`Збережено й опубліковано ${res.manifest_id} — edge підхопить протягом хвилини.`)
      } else {
        setNotice('Збережено; план не змінився, чинна версія лишається.')
      }
      onChanged()
    } catch (e) {
      setError(String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div style={{ display: 'grid', gap: 20 }}>
      {notice && <div className="ctl-notice">{notice}</div>}
      {error && <div className="ctl-notice err">{error}</div>}

      <section className="ctl-card">
        <h2>Обмеження та політика SOC</h2>
        <p className="ctl-card-sub">
          {loading
            ? 'Завантаження…'
            : saved
              ? 'Порожнє поле = не задано (діють паспортні значення з інвентаря).'
              : 'Налаштування ще не зберігались — діють паспортні значення з інвентаря.'}
        </p>
        <div className="ctl-settings-grid">
          {FIELDS.map((f) => (
            <label key={f.key} className="ctl-field">
              <span>{f.label}</span>
              <input
                inputMode="decimal"
                placeholder="не задано"
                value={form[f.key] ?? ''}
                disabled={loading || busy || !canEdit}
                onChange={(e) => setForm({ ...form, [f.key]: e.target.value })}
              />
              <small>{f.hint}</small>
            </label>
          ))}
          <label className="ctl-field">
            <span>Режим відпуску в мережу</span>
            <select
              value={regime}
              disabled={loading || busy || !canEdit}
              onChange={(e) => setRegime(e.target.value as ExportRegime)}
            >
              {EXPORT_REGIMES.map((r) => (
                <option key={r.value} value={r.value}>
                  {r.label}
                </option>
              ))}
            </select>
            <small>
              {ceiling !== null
                ? `Стеля відпуску PCC: ${ceiling.toLocaleString('uk-UA')} кВт`
                : regime
                  ? 'Задайте ліміт імпорту (договірну потужність) — від нього рахується стеля'
                  : 'Поки режим не обрано, пульт не дозволяє експорт'}
            </small>
          </label>
        </div>
        {canEdit ? (
          <div className="ctl-form-actions">
            <button type="button" className="ctl-btn" disabled={busy || loading} onClick={() => void save()}>
              Зберегти
            </button>
            <button
              type="button"
              className="ctl-btn primary"
              disabled={busy || loading}
              onClick={() => void saveAndPublish()}
            >
              Зберегти й опублікувати manifest
            </button>
          </div>
        ) : (
          <p className="ctl-card-sub">Змінювати обмеження може інженер-керування або адміністратор обʼєкта.</p>
        )}
      </section>

      <section className="ctl-card" aria-label="Без мережі">
        <h2>Без мережі</h2>
        <p className="ctl-card-sub">
          Запас заряду під кожне відключення з графіка: нижня межа плюс планове споживання за години відключення (без плану —
          середнє за 7 днів) із запасом на помилку, не вище верхньої межі. Пульт показує його завжди; у план він іде лише з
          позначкою нижче. Порожнє поле — значення зі специфікації.
        </p>
        <div className="ctl-settings-grid">
          {OFFGRID_FIELDS.map((f) => (
            <label key={f.key} className="ctl-field">
              <span>{f.label}</span>
              <input
                inputMode="decimal"
                placeholder="типово"
                value={form[f.key] ?? ''}
                disabled={loading || busy || !canEdit}
                onChange={(e) => setForm({ ...form, [f.key]: e.target.value })}
              />
              <small>{f.hint}</small>
            </label>
          ))}
        </div>
        <label className="ctl-field ctl-check">
          <input type="checkbox" checked={apply} disabled={loading || busy || !canEdit} onChange={(e) => setApply(e.target.checked)} />
          <span>Враховувати запас під відключення в плані</span>
          <small>Вимкнено — пульт лише показує ціль заряду. Увімкнено — план заряджає до неї до початку відключення.</small>
        </label>
        {canEdit && (
          <div className="ctl-form-actions">
            <button type="button" className="ctl-btn" disabled={busy || loading} onClick={() => void save()}>
              Зберегти
            </button>
          </div>
        )}
      </section>
    </div>
  )
}
