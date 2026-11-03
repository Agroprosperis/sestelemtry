import type { PvStationResponse } from './pvPlanClient'
import type { PvPlanForm } from './pvPlanForm'

type Props = {
  form: PvPlanForm
  // defaults are the config.yaml coordinates used when the passport
  // leaves its own empty.
  defaults: PvStationResponse['defaults']
  // wholeElevator shows the «План усього елеватора з» date: only a
  // station whose planes still describe its whole elevator needs it.
  wholeElevator: boolean
  disabled: boolean
  onChange: (patch: Partial<PvPlanForm>) => void
}

function coordinate(v: number | undefined): string {
  return v == null ? '' : String(v).replace('.', ',')
}

// StationFields are the passport's station-level inputs.
export function StationFields({ form, defaults, wholeElevator, disabled, onChange }: Props) {
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
        {wholeElevator ? (
          <label className="pvplan-field">
            <span>План усього елеватора з</span>
            <input
              type="date"
              value={form.wholeElevatorFrom}
              disabled={disabled}
              onChange={(e) => onChange({ wholeElevatorFrom: e.target.value })}
            />
          </label>
        ) : null}
      </div>
      <p className="pvplan-hint">
        Якщо широту й довготу не заповнено, план рахується за координатами обʼєкта з налаштувань консолі —
        вони показані сірим у полях.
      </p>
      {wholeElevator ? (
        <p className="pvplan-hint">
          Елеватор розділено на кілька станцій, а площини в цьому паспорті поки описують увесь елеватор. Тому з
          цієї дати план підписано як прогноз усього елеватора, і керування УЗЕ його не використовує. Коли
          внесете площини саме цієї станції, очистіть дату.
        </p>
      ) : null}
    </div>
  )
}
