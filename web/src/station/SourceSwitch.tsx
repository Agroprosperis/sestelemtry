import type { PvPlanSource } from '../api'
import { formatEnergyCompactKWhUk } from '../dashboard/format'
import type { PvStationResponse } from './pvPlanClient'
import { SOURCE_LABELS } from './pvPlanForm'

type Props = {
  source: PvPlanSource
  savedSource: PvPlanSource
  n8nAvailable: boolean
  // modelled: the passport is enough for the in-house model to run.
  modelled: boolean
  today: PvStationResponse['today'] | undefined
  disabled: boolean
  onChange: (source: PvPlanSource) => void
}

function formatPlan(kwh: number | undefined): string {
  return kwh == null ? '—' : formatEnergyCompactKWhUk(kwh)
}

// SourceSwitch picks where the station's plan comes from, next to
// today's plan of both sources, so the switch is made against numbers.
export function SourceSwitch({ source, savedSource, n8nAvailable, modelled, today, disabled, onChange }: Props) {
  return (
    <div className="pvplan-card">
      <fieldset className="pvplan-source" disabled={disabled}>
        <legend>Джерело плану</legend>
        <SourceOption
          value="n8n"
          current={source}
          onChange={onChange}
          disabled={!n8nAvailable}
          title="n8n — як раніше"
          hint={n8nAvailable ? 'потік Update SES Plan' : 'для цієї станції в n8n немає прогнозу'}
        />
        <SourceOption
          value="model"
          current={source}
          onChange={onChange}
          disabled={!modelled}
          title="Власна модель"
          hint={modelled ? 'з орієнтацій нижче' : 'спершу внесіть ліміт інвертора й орієнтації'}
        />
      </fieldset>
      <p className="pvplan-today">
        План на сьогодні: власна модель {formatPlan(today?.model_kwh)} · n8n {formatPlan(today?.n8n_kwh)}
      </p>
      {source !== savedSource ? (
        <p className="pvplan-note">Джерело зміниться на «{SOURCE_LABELS[source]}» після збереження.</p>
      ) : null}
    </div>
  )
}

function SourceOption({
  value,
  current,
  onChange,
  disabled,
  title,
  hint,
}: {
  value: PvPlanSource
  current: PvPlanSource
  onChange: (v: PvPlanSource) => void
  disabled: boolean
  title: string
  hint: string
}) {
  return (
    <label className={`pvplan-option${current === value ? ' is-on' : ''}`}>
      <input
        type="radio"
        name="pvplan-source"
        value={value}
        checked={current === value}
        disabled={disabled && current !== value}
        onChange={() => onChange(value)}
      />
      <span>
        <strong>{title}</strong>
        <small>{hint}</small>
      </span>
    </label>
  )
}
