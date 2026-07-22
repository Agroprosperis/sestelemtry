import { useState } from 'react'
import { OrganizationSelect } from '../dashboard/components/OrganizationSelect'
import { formatOrganizationLabel } from '../dashboard/config'
import { useOrganizationParam } from '../dashboard/hooks/useOrganizationParam'
import type { PlantInventory, PlantInventoryChange, PlantInventoryHistory } from '../types'
import { StationIcon } from './StationIcon'
import './station.css'
import {
  GROUP_LABELS,
  STATION_PARAMS,
  type StationParamDef,
  type StationParamGroup,
  type StationParamKey,
  formatHistoryValue,
  formatParamDisplay,
  formatPollReason,
  formatQualityFlag,
  readParamValue,
} from './stationParams'
import { usePlantInventory } from './usePlantInventory'
import { usePlantInventoryHistory } from './usePlantInventoryHistory'

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
  return (
    d.toLocaleString('uk-UA', {
      timeZone: 'UTC',
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
    }) + ' UTC'
  )
}

function ParamCard({
  inv,
  def,
  changes,
}: {
  inv: PlantInventory
  def: StationParamDef
  changes: PlantInventoryChange[]
}) {
  const [open, setOpen] = useState(false)
  const value = readParamValue(inv, def.key)
  const display = formatParamDisplay(def, value)
  const changeCount = changes.length

  return (
    <article className={`station-card${open ? ' is-open' : ''}`}>
      <button
        type="button"
        className="station-card-main"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
      >
        <span className="station-card-icon" data-icon={def.icon}>
          <StationIcon id={def.icon} />
        </span>
        <span className="station-card-body">
          <span className="station-card-label">{def.label}</span>
          <span className="station-card-value">
            {display}
            {def.unit && display !== '—' ? (
              <span className="station-unit">{def.unit}</span>
            ) : null}
          </span>
        </span>
        <span className="station-card-meta">
          <span className="station-card-history-hint">
            {changeCount > 0 ? `${changeCount} змін` : 'історія'}
          </span>
          <span className="station-card-chevron" aria-hidden="true">
            {open ? '▾' : '▸'}
          </span>
        </span>
      </button>
      {open ? (
        <div className="station-card-history">
          {changeCount === 0 ? (
            <p className="station-history-empty">Змін поки не зафіксовано.</p>
          ) : (
            <ul className="station-history-list">
              {changes.map((ev) => (
                <li key={`${ev.at}-${ev.from}-${ev.to}`}>
                  <time dateTime={ev.at}>{formatSnapshotTime(ev.at)}</time>
                  <span className="station-history-diff">
                    <span>{formatHistoryValue(def, ev.from)}</span>
                    <span className="station-history-arrow">→</span>
                    <span>{formatHistoryValue(def, ev.to)}</span>
                    {def.unit ? <span className="station-unit">{def.unit}</span> : null}
                  </span>
                  {ev.poll_reason ? (
                    <span className="station-history-reason">
                      {formatPollReason(ev.poll_reason)}
                    </span>
                  ) : null}
                </li>
              ))}
            </ul>
          )}
        </div>
      ) : null}
    </article>
  )
}

function MetaCard({ inv }: { inv: PlantInventory }) {
  return (
    <article className="station-card station-card-static">
      <div className="station-card-main static">
        <span className="station-card-icon" data-icon="meta">
          <StationIcon id="meta" />
        </span>
        <span className="station-card-body">
          <span className="station-card-label">Останній знімок</span>
          <span className="station-card-value station-card-value-sm">
            {formatSnapshotTime(inv.time)}
          </span>
          <dl className="station-meta-dl">
            <div>
              <dt>Причина</dt>
              <dd>{formatPollReason(inv.poll_reason)}</dd>
            </div>
            {inv.device_host ? (
              <div>
                <dt>SmartLogger</dt>
                <dd>{inv.device_host}</dd>
              </div>
            ) : null}
            <div>
              <dt>Прапорці якості</dt>
              <dd>
                {inv.quality_flags.length === 0 ? (
                  'немає'
                ) : (
                  <ul className="station-flags">
                    {inv.quality_flags.map((f) => (
                      <li key={f}>{formatQualityFlag(f)}</li>
                    ))}
                  </ul>
                )}
              </dd>
            </div>
          </dl>
        </span>
      </div>
    </article>
  )
}

function changesFor(
  history: PlantInventoryHistory | null,
  key: StationParamKey,
): PlantInventoryChange[] {
  return history?.changes?.[key] ?? []
}

function InventoryBody({
  inv,
  history,
}: {
  inv: PlantInventory
  history: PlantInventoryHistory | null
}) {
  const groups: StationParamGroup[] = ['passport', 'ops']
  return (
    <>
      {groups.map((group) => {
        const defs = STATION_PARAMS.filter((d) => d.group === group)
        return (
          <section key={group} className="station-section">
            <h2>{GROUP_LABELS[group]}</h2>
            <div className="station-grid">
              {defs.map((def: StationParamDef) => (
                <ParamCard
                  key={def.key}
                  inv={inv}
                  def={def}
                  changes={changesFor(history, def.key)}
                />
              ))}
              {group === 'ops' ? <MetaCard inv={inv} /> : null}
            </div>
          </section>
        )
      })}
    </>
  )
}

export function StationPage() {
  const { organizationID, options, change: onOrganizationChange } = useOrganizationParam()
  const { data, loading, error } = usePlantInventory(organizationID)
  const {
    data: history,
    loading: historyLoading,
    error: historyError,
  } = usePlantInventoryHistory(organizationID)

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

      {historyError ? (
        <div className="station-error" role="alert">
          Не вдалося завантажити історію: {historyError}
        </div>
      ) : null}

      {!error && (loading || historyLoading) ? (
        <div className="station-loading">Завантаження…</div>
      ) : null}

      {!error && !loading && !historyLoading && data == null ? (
        <div className="station-empty">
          Ще немає знімка — collector зніме при старті / за розкладом.
        </div>
      ) : null}

      {!error && !loading && !historyLoading && data != null ? (
        <InventoryBody inv={data} history={history} />
      ) : null}
    </main>
  )
}
