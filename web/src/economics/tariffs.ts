// Tariffs encapsulates every per-kWh number the daily economics
// model needs. The spec [daily_economic_model_mvp.md] uses these
// inputs to derive the hourly import/export prices that turn raw
// energy flows into UAH. All values are UAH/kWh except `vatRate`
// (fraction) and the two booleans.
//
// Defaults reflect the operator-supplied numbers for 2026-05-09:
//   - `distributionUahPerKwh`: 1-class tariff for ВІННИЦЯОБЛЕНЕРГО
//   - `transmissionUahPerKwh`: НЕК "Укренерго" 2-й етап (з 01.04.2026)
//   - `exportDiscount`: 5% знижка на експортну сторону (per ТЗ)
//   - `degradationUahPerKwh`: вартість циклів УЗЕ (питомий знос)
//   - `supplierMargin` / `otherFees`: оператор-специфічні; зазвичай 0
//   - `includeVat=false`: моделюємо нетто; UI окремо показує brutto
//   - `essCapacityKwh`: для розрахунку equivalent cycles на годину
//
// `essCapacityKwh` ξs technically not a tariff but it lives in the
// same form on the page (and is URL-encoded along with the rest), so
// we co-locate it here to keep the URL parser symmetrical.
export type Tariffs = {
  distributionUahPerKwh: number
  transmissionUahPerKwh: number
  supplierMarginUahPerKwh: number
  otherFeesUahPerKwh: number
  exportDiscount: number
  degradationUahPerKwh: number
  includeVat: boolean
  vatRate: number
  essCapacityKwh: number
}

export const DEFAULT_TARIFFS: Tariffs = {
  distributionUahPerKwh: 0.4881,
  transmissionUahPerKwh: 0.74291,
  supplierMarginUahPerKwh: 0,
  otherFeesUahPerKwh: 0,
  exportDiscount: 0.05,
  degradationUahPerKwh: 0.6,
  includeVat: false,
  vatRate: 0.2,
  essCapacityKwh: 215,
}

// readNumber accepts either a string (URL parameter) or undefined
// and returns the parsed number when it's a finite real number, or
// `fallback` otherwise. Negative values are accepted because the
// supplierMargin field may legitimately be negative (rebate); other
// fields are clamped at the call site rather than here.
function readNumber(raw: string | null, fallback: number): number {
  if (raw === null || raw === '') return fallback
  const n = Number(raw)
  return Number.isFinite(n) ? n : fallback
}

function readBool(raw: string | null, fallback: boolean): boolean {
  if (raw === null) return fallback
  if (raw === 'true' || raw === '1') return true
  if (raw === 'false' || raw === '0') return false
  return fallback
}

// parseTariffsFromSearch reads the URL query string and produces a
// fully populated Tariffs object. Unknown / missing keys fall back
// to DEFAULT_TARIFFS so a bare `?view=economics` still renders.
//
// We accept short, snake-cased query keys so the URL stays
// shareable-by-Slack. Booleans accept `true|false|1|0` because the
// operator may hand-edit the URL.
export function parseTariffsFromSearch(search: string | URLSearchParams): Tariffs {
  const p = typeof search === 'string' ? new URLSearchParams(search) : search
  return {
    distributionUahPerKwh: readNumber(p.get('distribution'), DEFAULT_TARIFFS.distributionUahPerKwh),
    transmissionUahPerKwh: readNumber(p.get('transmission'), DEFAULT_TARIFFS.transmissionUahPerKwh),
    supplierMarginUahPerKwh: readNumber(p.get('supplier_margin'), DEFAULT_TARIFFS.supplierMarginUahPerKwh),
    otherFeesUahPerKwh: readNumber(p.get('other_fees'), DEFAULT_TARIFFS.otherFeesUahPerKwh),
    exportDiscount: readNumber(p.get('export_discount'), DEFAULT_TARIFFS.exportDiscount),
    degradationUahPerKwh: readNumber(p.get('degradation'), DEFAULT_TARIFFS.degradationUahPerKwh),
    includeVat: readBool(p.get('include_vat'), DEFAULT_TARIFFS.includeVat),
    vatRate: readNumber(p.get('vat_rate'), DEFAULT_TARIFFS.vatRate),
    essCapacityKwh: readNumber(p.get('ess_capacity'), DEFAULT_TARIFFS.essCapacityKwh),
  }
}

// serializeTariffsToSearch writes a Tariffs object into the supplied
// URLSearchParams in-place. We only emit keys whose value differs
// from the default so a "default tariffs" URL stays clean — the
// operator can still copy it into Slack without 9 redundant
// query parameters.
export function serializeTariffsToSearch(tariffs: Tariffs, params: URLSearchParams): void {
  const writeNum = (key: string, value: number, fallback: number) => {
    if (value === fallback) {
      params.delete(key)
      return
    }
    params.set(key, String(value))
  }
  const writeBool = (key: string, value: boolean, fallback: boolean) => {
    if (value === fallback) {
      params.delete(key)
      return
    }
    params.set(key, value ? 'true' : 'false')
  }
  writeNum('distribution', tariffs.distributionUahPerKwh, DEFAULT_TARIFFS.distributionUahPerKwh)
  writeNum('transmission', tariffs.transmissionUahPerKwh, DEFAULT_TARIFFS.transmissionUahPerKwh)
  writeNum('supplier_margin', tariffs.supplierMarginUahPerKwh, DEFAULT_TARIFFS.supplierMarginUahPerKwh)
  writeNum('other_fees', tariffs.otherFeesUahPerKwh, DEFAULT_TARIFFS.otherFeesUahPerKwh)
  writeNum('export_discount', tariffs.exportDiscount, DEFAULT_TARIFFS.exportDiscount)
  writeNum('degradation', tariffs.degradationUahPerKwh, DEFAULT_TARIFFS.degradationUahPerKwh)
  writeBool('include_vat', tariffs.includeVat, DEFAULT_TARIFFS.includeVat)
  writeNum('vat_rate', tariffs.vatRate, DEFAULT_TARIFFS.vatRate)
  writeNum('ess_capacity', tariffs.essCapacityKwh, DEFAULT_TARIFFS.essCapacityKwh)
}
