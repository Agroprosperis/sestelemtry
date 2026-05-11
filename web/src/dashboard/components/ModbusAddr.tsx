import type { RegisterMeta } from '../../types'

// ModbusAddr renders the diagnostic "(40443)" suffix next to a
// card field label when debug mode is on. Returns null when
// debug is off, when no register metadata is loaded yet, or
// when the metric is synthetic (no Modbus address) — that way
// callers can drop it next to every label without guarding the
// call site each time.
//
// `keys` accepts more than one metric key for fields whose value
// derives from multiple registers (e.g. "Батарея: заряд X, розряд Y"
// combines `total_energy_charged_kwh` and `total_energy_discharged_kwh`).
// Addresses are rendered as a single comma-separated group so the
// suffix stays compact: `(40446, 40448)`.

type Props = {
  debug: boolean
  registers: Record<string, RegisterMeta> | null
  keys: string | string[]
}

export function ModbusAddr({ debug, registers, keys }: Props) {
  if (!debug || !registers) return null
  const list = Array.isArray(keys) ? keys : [keys]
  const addresses: number[] = []
  for (const k of list) {
    const meta = registers[k]
    if (meta && Number.isFinite(meta.address)) addresses.push(meta.address)
  }
  if (addresses.length === 0) return null
  return (
    <span className="metric-modbus-addr">({addresses.join(', ')})</span>
  )
}
