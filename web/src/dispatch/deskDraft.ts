// Desk form and draft persistence: the constraints form keeps the raw
// input strings; the draft survives reloads in this browser until it is
// confirmed or a newer version invalidates it.

import type { Constraints } from './dispatchClient'
import type { DeskModel } from './model'

export type CfgForm = {
  reserve: string
  grid: boolean
  essSale: boolean
  blockExport: boolean
  importCap: string
  exportCap: string
}

export const cfgToForm = (c: Constraints): CfgForm => ({
  reserve: String(c.reserve_pct),
  grid: c.grid_charge,
  essSale: c.ess_sale,
  blockExport: c.block_export,
  importCap: String(c.import_cap_kw),
  exportCap: c.export_cap_kw === null ? '' : String(c.export_cap_kw),
})

export const formToCfg = (f: CfgForm): Constraints => ({
  reserve_pct: Number(f.reserve),
  grid_charge: f.grid,
  ess_sale: f.essSale,
  block_export: f.blockExport,
  import_cap_kw: Number(f.importCap),
  export_cap_kw: f.exportCap.trim() === '' ? null : Number(f.exportCap),
})

export type Selection = { start: number; end: number; anchor: number; inspect: number }

export type SavedDraft = { start_hour: string; base_version: number; working: DeskModel; history: DeskModel[] }

export const HISTORY_LIMIT = 30

const draftKey = (site: string) => 'dispatch-desk:' + site

export function readDraft(site: string): SavedDraft | null {
  try {
    const raw = window.localStorage.getItem(draftKey(site))
    return raw ? (JSON.parse(raw) as SavedDraft) : null
  } catch {
    return null
  }
}

export function writeDraft(site: string, d: SavedDraft | null) {
  try {
    if (d) window.localStorage.setItem(draftKey(site), JSON.stringify(d))
    else window.localStorage.removeItem(draftKey(site))
  } catch {
    /* storage full or blocked — the draft lives in memory only */
  }
}
