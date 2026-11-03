import type { PvStationResponse } from './pvPlanClient'
import type { PvPlanForm } from './pvPlanForm'

type Props = {
  form: PvPlanForm
  // defaults are the config.yaml coordinates used when the passport
  // leaves its own empty.
  defaults: PvStationResponse['defaults']
  disabled: boolean
  onChange: (patch: Partial<PvPlanForm>) => void
}

function coordinate(v: number | undefined): string {
  return v == null ? '' : String(v).replace('.', ',')
}

// StationFields are the passport's station-level inputs.
export function StationFields({ form, defaults, disabled, onChange }: Props) {
  return (
    <div className="pvplan-card">
      <h3>Станція</h3>
      <div className="pvplan-grid">
        <label className="pvplan-field">
          <span>Широта</span>
          <input
            inputMode="decimal"
            value={form.latitude}
            placeholder={coordinate(defaults?.latitude)}
            disabled={disabled}
            onChange={(e) => onChange({ latitude: e.target.value })}
          />
        </label>
        <label className="pvplan-field">
          <span>Довгота</span>
          <input
            inputMode="decimal"
            value={form.longitude}
            placeholder={coordinate(defaults?.longitude)}
            disabled={disabled}
            onChange={(e) => onChange({ longitude: e.target.value })}
          />
        </label>
        <label className="pvplan-field">
          <span>Ліміт інвертора, кВт AC</span>
          <input
            inputMode="decimal"
            value={form.pacLimitKw}
            disabled={disabled}
            onChange={(e) => onChange({ pacLimitKw: e.target.value })}
          />
        </label>
        <label className="pvplan-field">
          <span>Темп. коефіцієнт модулів, %/°C</span>
          <input
            inputMode="decimal"
            value={form.gamma}
            placeholder="−0,29"
            disabled={disabled}
            onChange={(e) => onChange({ gamma: e.target.value })}
          />
        </label>
        <label className="pvplan-field">
          <span>Прогноз описує весь елеватор з</span>
          <input
            type="date"
            value={form.wholeElevatorFrom}
            disabled={disabled}
            onChange={(e) => onChange({ wholeElevatorFrom: e.target.value })}
          />
        </label>
      </div>
      <p className="pvplan-hint">
        Порожні координати — з конфігурації організації. Дата «весь елеватор» потрібна, поки орієнтації
        описують більше, ніж цю станцію: з неї план підписаний як прогноз усього елеватора, а керування на
        нього не планує.
      </p>
    </div>
  )
}
