import { buildURL } from '../api'
import type { Tariffs } from './tariffs'

// OrgTariffsApi mirrors the snake_case JSON shape served by
// /api/v1/organization-tariffs (see internal/api/types.go:OrgTariffs).
// We map between this and the camelCase Tariffs domain type at the
// API boundary so the rest of the frontend stays in TS-idiomatic
// style and the wire shape can evolve independently.
//
// This module lives next to the economics feature (rather than in
// the global web/src/api.ts) because the mapping is domain-specific:
// only the economics page reads/writes tariffs, and the snake↔camel
// adapter has no value to other features. Mirrors the
// dashboard/transforms/weatherAdapter.ts split.
type OrgTariffsApi = {
  distribution_uah_per_kwh: number
  transmission_uah_per_kwh: number
  supplier_margin_uah_per_kwh: number
  other_fees_uah_per_kwh: number
  export_discount: number
  degradation_uah_per_kwh: number
  include_vat: boolean
  vat_rate: number
  ess_capacity_kwh: number
  // seed_ess_cost_uah_per_kwh is read as optional so legacy DB
  // rows (saved before the field existed) still hydrate without
  // throwing; we coalesce missing values to 0 in `tariffsFromApi`.
  // The PUT direction always sends the field so backend
  // DisallowUnknownFields stays compatible after its struct
  // gains the matching tag.
  seed_ess_cost_uah_per_kwh?: number
}

function tariffsToApi(t: Tariffs): OrgTariffsApi {
  return {
    distribution_uah_per_kwh: t.distributionUahPerKwh,
    transmission_uah_per_kwh: t.transmissionUahPerKwh,
    supplier_margin_uah_per_kwh: t.supplierMarginUahPerKwh,
    other_fees_uah_per_kwh: t.otherFeesUahPerKwh,
    export_discount: t.exportDiscount,
    degradation_uah_per_kwh: t.degradationUahPerKwh,
    include_vat: t.includeVat,
    vat_rate: t.vatRate,
    ess_capacity_kwh: t.essCapacityKwh,
    seed_ess_cost_uah_per_kwh: t.seedEssCostUahPerKwh,
  }
}

function tariffsFromApi(api: OrgTariffsApi): Tariffs {
  return {
    distributionUahPerKwh: api.distribution_uah_per_kwh,
    transmissionUahPerKwh: api.transmission_uah_per_kwh,
    supplierMarginUahPerKwh: api.supplier_margin_uah_per_kwh,
    otherFeesUahPerKwh: api.other_fees_uah_per_kwh,
    exportDiscount: api.export_discount,
    degradationUahPerKwh: api.degradation_uah_per_kwh,
    includeVat: api.include_vat,
    vatRate: api.vat_rate,
    essCapacityKwh: api.ess_capacity_kwh,
    seedEssCostUahPerKwh:
      typeof api.seed_ess_cost_uah_per_kwh === 'number' &&
      Number.isFinite(api.seed_ess_cost_uah_per_kwh)
        ? api.seed_ess_cost_uah_per_kwh
        : 0,
  }
}

// fetchOrgTariffs reads the persisted economics tariff bundle for one
// organization. Resolves to `null` (not throw) on 404 so the caller
// can switch to bundled defaults — that's the natural "no settings
// saved yet" signal for a fresh org. Other non-OK statuses still
// throw because they indicate a real backend failure.
export async function fetchOrgTariffs(
  organizationID: string,
  signal?: AbortSignal,
): Promise<Tariffs | null> {
  const url = buildURL('/api/v1/organization-tariffs', {
    organization_id: organizationID,
  })
  const res = await fetch(url, { signal })
  if (res.status === 404) return null
  if (!res.ok) {
    throw new Error(`organization-tariffs request failed: ${res.status}`)
  }
  const body = (await res.json()) as OrgTariffsApi
  return tariffsFromApi(body)
}

// saveOrgTariffs upserts the tariff bundle for one organization. The
// backend validates each numeric field (range + finite-ness) and
// rejects unknown JSON fields; we surface the server's text body in
// the thrown error so the dashboard's "Помилка" indicator can show a
// usable hint without a separate /errors channel.
export async function saveOrgTariffs(
  organizationID: string,
  tariffs: Tariffs,
  signal?: AbortSignal,
): Promise<void> {
  const url = buildURL('/api/v1/organization-tariffs', {
    organization_id: organizationID,
  })
  const res = await fetch(url, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(tariffsToApi(tariffs)),
    signal,
  })
  if (!res.ok) {
    const body = await res.text().catch(() => '')
    const trimmed = body.trim()
    throw new Error(
      `organization-tariffs save failed: ${res.status}${trimmed ? ` ${trimmed}` : ''}`,
    )
  }
}
