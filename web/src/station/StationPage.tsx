import { OrganizationSelect } from '../dashboard/components/OrganizationSelect'
import { formatOrganizationLabel } from '../dashboard/config'
import { useOrganizationParam } from '../dashboard/hooks/useOrganizationParam'
import type { PlantInventory } from '../types'
import './station.css'
import {
  GROUP_LABELS,
  STATION_PARAMS,
  type StationParamDef,
  type StationParamGroup,
  formatParamDisplay,
  formatPollReason,
  formatQualityFlag,
  readParamValue,
} from './stationParams'
import { usePlantInventory } from './usePlantInventory'

function backToDashboard() {
  if (typeof window === 'undefined') return
  const url = new URL(window.location.href)
  url.searchParams.delete('view')
  window.history.pushState({}, '', url.toString())
  window.dispatchEvent(new PopStateEvent('popstate'))
}

function formatSnapshotTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString('uk-UA', {
    timeZone: 'UTC',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }) + ' UTC'
}

function ParamRows({ inv, group }: { inv: PlantInventory; group: StationParamGroup }) {
  const defs = STATION_PARAMS.filter((d) => d.group === group)
  return (
    <>
      {defs.map((def) => (
        <ParamRow key={def.key} inv={inv} def={def} />
      ))}
    </>
  )
}

function ParamRow({ inv, def }: { inv: PlantInventory; def: StationParamDef }) {
  const value = readParamValue(inv, def.key)
  const display = formatParamDisplay(def, value)
  return (
    <tr>
      <th scope="row">{def.label}</th>
      <td>
        {display}
        {def.unit && display !== '—' ? <span className="station-unit">{def.unit}</span> : null}
      </td>
    </tr>
  )
}

function InventoryBody({ inv }: { inv: PlantInventory }) {
  const groups: StationParamGroup[] = ['passport', 'ops']
  return (
    <>
      {groups.map((group) => (
        <section key={group} className="station-section">
          <h2>{GROUP_LABELS[group]}</h2>
          <table className="station-table">
            <tbody>
              <ParamRows inv={inv} group={group} />
              {group === 'ops' ? (
                <>
                  <tr>
                    <th scope="row">Час знімка</th>
                    <td>{formatSnapshotTime(inv.time)}</td>
                  </tr>
                  <tr>
                    <th scope="row">Причина опитування</th>
                    <td>{formatPollReason(inv.poll_reason)}</td>
                  </tr>
                  {inv.device_host ? (
                    <tr>
                      <th scope="row">SmartLogger</th>
                      <td>{inv.device_host}</td>
                    </tr>
                  ) : null}
                  <tr>
                    <th scope="row">Прапорці якості</th>
                    <td>
                      {inv.quality_flags.length === 0 ? (
                        'немає'
                      ) : (
                        <ul className="station-flags">
                          {inv.quality_flags.map((f) => (
                            <li key={f}>{formatQualityFlag(f)}</li>
                          ))}
                        </ul>
                      )}
                    </td>
                  </tr>
                </>
              ) : null}
            </tbody>
          </table>
        </section>
      ))}
    </>
  )
}

export function StationPage() {
  const { organizationID, options, change: onOrganizationChange } = useOrganizationParam()
  const { data, loading, error } = usePlantInventory(organizationID)

  return (
    <main className="station-page">
      <header className="station-header">
        <button type="button" className="station-back" onClick={backToDashboard}>
          ← Дашборд
        </button>
        <div className="station-heading">
          <h1>Паспорт станції</h1>
          <p className="station-subtitle">
            Номінальні параметри з SmartLogger
            {organizationID ? ` · ${formatOrganizationLabel(organizationID)}` : ''}
          </p>
        </div>
      </header>

      <div className="station-toolbar">
        <OrganizationSelect
          value={organizationID}
          options={options}
          onChange={onOrganizationChange}
        />
      </div>

      {error ? (
        <div className="station-error" role="alert">
          Не вдалося завантажити паспорт: {error}
        </div>
      ) : null}

      {!error && loading ? <div className="station-loading">Завантаження…</div> : null}

      {!error && !loading && data == null ? (
        <div className="station-empty">
          Ще немає знімка — collector зніме при старті / за розкладом.
        </div>
      ) : null}

      {!error && !loading && data != null ? <InventoryBody inv={data} /> : null}
    </main>
  )
}
