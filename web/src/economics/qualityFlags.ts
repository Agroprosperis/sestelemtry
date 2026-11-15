// The generator's day flags (gen_cost_missing, gen_shared, gen_estimated,
// supply_unknown:<хв>, supply_transition:<хв>, offgrid_import:<кВт·год>)
// are no discrepancy with FusionSolar, so the reconciliation line leaves
// them out.
const GENERATOR_FLAG_PREFIXES = ['gen_', 'supply_', 'offgrid_']

// discrepancyFlags is a day's quality flags without the generator's.
export function discrepancyFlags(flags: string[]): string[] {
  return flags.filter((f) => !GENERATOR_FLAG_PREFIXES.some((p) => f.startsWith(p)))
}
