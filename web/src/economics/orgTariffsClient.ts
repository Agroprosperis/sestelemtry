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
  supplier_margin_mode?: 'abs' | 'pct'
  supplier_margin_pct?: number
  other_fees_uah_per_kwh: number
  export_discount: number
  degradation_uah_per_kwh: number
  include_vat: boolean
  vat_rate: number
  ess_capacity_kwh: number
  ess_power_limit_kw?: number
  roundtrip_efficiency?: number
  capex_uah?: number
  // seed_ess_cost_uah_per_kwh is the legacy fallback cost-basis
  // tariff. Kept here as a tolerated optional field so legacy DB
  // rows still hydrate without DisallowUnknownFields complaints,
  // but the value is never read by the cost-basis pipeline anymore
  // (the algorithm anchors on the last ≤10% SOC drop instead).
  // Dropped from `tariffsToApi` so we stop persisting it; the
  // server's struct no longer carries the field either.
  seed_ess_cost_uah_per_kwh?: number
}

function tariffsToApi(t: Tariffs): OrgTariffsApi {
  return {
    distribution_uah_per_kwh: t.distributionUahPerKwh,
    transmission_uah_per_kwh: t.transmissionUahPerKwh,
    supplier_margin_uah_per_kwh: t.supplierMarginUahPerKwh,
    supplier_margin_mode: t.supplierMarginMode,
    supplier_margin_pct: t.supplierMarginPct,
    other_fees_uah_per_kwh: t.otherFeesUahPerKwh,
    export_discount: t.exportDiscount,
    degradation_uah_per_kwh: t.degradationUahPerKwh,
    include_vat: t.includeVat,
    vat_rate: t.vatRate,
    ess_capacity_kwh: t.essCapacityKwh,
    ess_power_limit_kw: t.essPowerLimitKw,
    roundtrip_efficiency: t.roundtripEfficiency,
    capex_uah: t.capexUah,
  }
}

function tariffsFromApi(api: OrgTariffsApi): Tariffs {
  return {
    distributionUahPerKwh: api.distribution_uah_per_kwh,
    transmissionUahPerKwh: api.transmission_uah_per_kwh,
    supplierMarginUahPerKwh: api.supplier_margin_uah_per_kwh,
    supplierMarginMode: api.supplier_margin_mode === 'pct' ? 'pct' : 'abs',
    supplierMarginPct: api.supplier_margin_pct ?? 0,
    otherFeesUahPerKwh: api.other_fees_uah_per_kwh,
    exportDiscount: api.export_discount,
    degradationUahPerKwh: api.degradation_uah_per_kwh,
    includeVat: api.include_vat,
    vatRate: api.vat_rate,
    essCapacityKwh: api.ess_capacity_kwh,
    essPowerLimitKw: api.ess_power_limit_kw ?? 0,
    roundtripEfficiency: api.roundtrip_efficiency ?? 0,
    capexUah: api.capex_uah ?? 0,
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

// TariffScheduleVersion is one effective-dated tariff version managed
// by the schedule endpoints. effectiveFrom is a YYYY-MM-DD civil date.
export type TariffScheduleVersion = {
  effectiveFrom: string
  tariffs: Tariffs
}

// fetchTariffSchedule lists the org's date-versioned tariffs (ascending
// by effective_from). The server resolves the effective version per day
// when it computes economics; this list lets the operator review/edit
// the schedule.
export async function fetchTariffSchedule(
  organizationID: string,
  signal?: AbortSignal,
): Promise<TariffScheduleVersion[]> {
  const url = buildURL('/api/v1/organization-tariff-schedule', {
    organization_id: organizationID,
  })
  const res = await fetch(url, { signal })
  if (!res.ok) {
    throw new Error(`tariff-schedule request failed: ${res.status}`)
  }
  const body = (await res.json()) as {
    versions: { effective_from: string; tariffs: OrgTariffsApi }[]
  }
  return (body.versions ?? []).map((v) => ({
    effectiveFrom: v.effective_from,
    tariffs: tariffsFromApi(v.tariffs),
  }))
}

// saveTariffScheduleVersion upserts one effective-dated tariff version.
export async function saveTariffScheduleVersion(
  organizationID: string,
  effectiveFrom: string,
  tariffs: Tariffs,
  signal?: AbortSignal,
): Promise<void> {
  const url = buildURL('/api/v1/organization-tariff-schedule', {
    organization_id: organizationID,
  })
  const res = await fetch(url, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ effective_from: effectiveFrom, tariffs: tariffsToApi(tariffs) }),
    signal,
  })
  if (!res.ok) {
    const body = await res.text().catch(() => '')
    const trimmed = body.trim()
    throw new Error(
      `tariff-schedule save failed: ${res.status}${trimmed ? ` ${trimmed}` : ''}`,
    )
  }
}

// deleteTariffScheduleVersion removes one effective-dated tariff version.
export async function deleteTariffScheduleVersion(
  organizationID: string,
  effectiveFrom: string,
  signal?: AbortSignal,
): Promise<void> {
  const url = buildURL('/api/v1/organization-tariff-schedule', {
    organization_id: organizationID,
    effective_from: effectiveFrom,
  })
  const res = await fetch(url, { method: 'DELETE', signal })
  if (!res.ok && res.status !== 404) {
    throw new Error(`tariff-schedule delete failed: ${res.status}`)
  }
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
