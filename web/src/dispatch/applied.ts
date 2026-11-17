// manifestVersion reads the desk version a manifest was built from
// (note "dispatch v6"); null when the manifest does not say.
export function manifestVersion(note: string | undefined): number | null {
  const m = /dispatch v(\d+)/.exec(note ?? '')
  return m ? Number(m[1]) : null
}
