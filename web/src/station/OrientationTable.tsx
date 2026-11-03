import { emptyRow, ratedMismatch, rowPdcKw, rowsPdcTotalKwp, type PvPlanRow } from './pvPlanForm'

type Props = {
  rows: PvPlanRow[]
  // pacLimitKw is the station's inverter limit as typed, for DC/AC.
  pacLimitKw: string
  // ratedKw is FusionSolar's rated PV power, to cross-check the planes.
  ratedKw: number | null
  canEdit: boolean
  disabled: boolean
  busy: boolean
  onChange: (rows: PvPlanRow[]) => void
}

function formatKw(v: number): string {
  return v.toLocaleString('uk-UA', { maximumFractionDigits: 2 })
}

// OrientationTable edits the planes of panels and sums their nameplate.
export function OrientationTable({ rows, pacLimitKw, ratedKw, canEdit, disabled, busy, onChange }: Props) {
  const totalKwp = rowsPdcTotalKwp(rows)
  const pac = Number(pacLimitKw.replace(',', '.'))
  const dcAc = pac > 0 && totalKwp > 0 ? totalKwp / pac : null
  const mismatch = ratedMismatch(totalKwp, ratedKw)
  const updateRow = (key: number, patch: Partial<PvPlanRow>) =>
    onChange(rows.map((r) => (r.key === key ? { ...r, ...patch } : r)))

  return (
    <div className="pvplan-card">
      <h3>Орієнтації панелей</h3>
      <p className="pvplan-hint">Азимут: 0 — південь, −90 — схід, +90 — захід, ±180 — північ.</p>
      <div className="pvplan-table-wrap">
        <table className="pvplan-table">
          <thead>
            <tr>
              <th>№</th>
              <th>Нахил, °</th>
              <th>Азимут, °</th>
              <th>Модулів</th>
              <th>Вт модуля</th>
              <th>кВт·пік</th>
              <th>Увімк.</th>
              <th>Примітка</th>
              {canEdit ? <th aria-label="Дії" /> : null}
            </tr>
          </thead>
          <tbody>
            {rows.length === 0 ? (
              <tr>
                <td colSpan={canEdit ? 9 : 8} className="pvplan-empty">
                  Орієнтацій ще немає.
                </td>
              </tr>
            ) : null}
            {rows.map((r) => {
              const pdc = rowPdcKw(r)
              return (
                <tr key={r.key} className={r.enabled ? undefined : 'is-off'}>
                  <td>
                    <input
                      aria-label="Номер орієнтації"
                      className="pvplan-idx"
                      inputMode="numeric"
                      value={r.idx}
                      disabled={disabled}
                      onChange={(e) => updateRow(r.key, { idx: e.target.value })}
                    />
                  </td>
                  <td>
                    <input
                      aria-label={`Нахил орієнтації ${r.idx}`}
                      inputMode="decimal"
                      value={r.tilt}
                      disabled={disabled}
                      onChange={(e) => updateRow(r.key, { tilt: e.target.value })}
                    />
                  </td>
                  <td>
                    <input
                      aria-label={`Азимут орієнтації ${r.idx}`}
                      inputMode="decimal"
                      value={r.azimuth}
                      disabled={disabled}
                      onChange={(e) => updateRow(r.key, { azimuth: e.target.value })}
                    />
                  </td>
                  <td>
                    <input
                      aria-label={`Модулів в орієнтації ${r.idx}`}
                      inputMode="numeric"
                      value={r.modules}
                      disabled={disabled}
                      onChange={(e) => updateRow(r.key, { modules: e.target.value })}
                    />
                  </td>
                  <td>
                    <input
                      aria-label={`Потужність модуля орієнтації ${r.idx}`}
                      inputMode="decimal"
                      value={r.moduleW}
                      disabled={disabled}
                      onChange={(e) => updateRow(r.key, { moduleW: e.target.value })}
                    />
                  </td>
                  <td className="pvplan-num">{pdc == null ? '—' : formatKw(pdc)}</td>
                  <td className="pvplan-center">
                    <input
                      type="checkbox"
                      aria-label={`Орієнтація ${r.idx} увімкнена`}
                      checked={r.enabled}
                      disabled={disabled}
                      onChange={(e) => updateRow(r.key, { enabled: e.target.checked })}
                    />
                  </td>
                  <td>
                    <input
                      aria-label={`Примітка до орієнтації ${r.idx}`}
                      value={r.note}
                      disabled={disabled}
                      onChange={(e) => updateRow(r.key, { note: e.target.value })}
                    />
                  </td>
                  {canEdit ? (
                    <td className="pvplan-center">
                      <button
                        type="button"
                        className="pvplan-remove"
                        aria-label={`Видалити орієнтацію ${r.idx}`}
                        disabled={busy}
                        onClick={() => onChange(rows.filter((x) => x.key !== r.key))}
                      >
                        ×
                      </button>
                    </td>
                  ) : null}
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
      {canEdit ? (
        <button type="button" className="pvplan-btn" disabled={busy} onClick={() => onChange([...rows, emptyRow(rows)])}>
          + Додати орієнтацію
        </button>
      ) : null}
      <dl className="pvplan-summary">
        <div>
          <dt>Разом</dt>
          <dd>{formatKw(totalKwp)} кВт·пік</dd>
        </div>
        <div>
          <dt>DC/AC</dt>
          <dd>{dcAc == null ? '—' : dcAc.toFixed(2).replace('.', ',')}</dd>
        </div>
        <div>
          <dt>FusionSolar</dt>
          <dd>{ratedKw != null && ratedKw > 0 ? `${formatKw(ratedKw)} кВт` : '—'}</dd>
        </div>
      </dl>
      {mismatch != null ? (
        <p className="pvplan-warn" role="status">
          Сума орієнтацій розходиться з номіналом FusionSolar на {Math.round(mismatch * 100)} % — перевірте,
          чи не пропущена або не задвоєна орієнтація.
        </p>
      ) : null}
    </div>
  )
}
