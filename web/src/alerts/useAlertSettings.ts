import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  type AlertSettings,
  type OrgAlertSettings,
  fetchAlertSettings,
  fetchOrgAlertSettings,
  saveAlertSettings,
  saveOrgAlertSettings,
} from './alertsClient'

function isAbortError(e: unknown): boolean {
  return e instanceof DOMException && e.name === 'AbortError'
}

function message(e: unknown, fallback: string): string {
  return e instanceof Error ? e.message : fallback
}

export type UseAlertSettings = {
  settings: AlertSettings | null
  /** False while the page is still showing the config.yaml fallback. */
  saved: boolean
  passwordConfigured: boolean
  organizations: Record<string, OrgAlertSettings>
  loading: boolean
  saving: boolean
  error: string | null
  /** Set after a successful save so the page can confirm it. */
  savedAt: number | null
  dirty: boolean
  update: (patch: Partial<AlertSettings>) => void
  updateSmtp: (patch: Partial<AlertSettings['smtp']>) => void
  updateOrganization: (organizationID: string, patch: Partial<OrgAlertSettings>) => void
  /** null leaves the stored password alone; '' clears it. */
  setPassword: (value: string | null) => void
  password: string | null
  save: () => Promise<void>
  reload: () => void
}

// useAlertSettings loads the notification settings and holds the edits
// until the operator presses Save.
//
// Unlike useOrgTariffs, which autosaves, this page is explicitly
// save-on-demand: it carries mail credentials and a switch that decides
// whether anyone gets told about an outage, so a half-typed SMTP host
// must not reach the watchdog.
export function useAlertSettings(): UseAlertSettings {
  const [settings, setSettings] = useState<AlertSettings | null>(null)
  const [organizations, setOrganizations] = useState<Record<string, OrgAlertSettings>>({})
  const [passwordConfigured, setPasswordConfigured] = useState(false)
  const [saved, setSaved] = useState(false)
  const [password, setPassword] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [savedAt, setSavedAt] = useState<number | null>(null)
  const [reloadKey, setReloadKey] = useState(0)
  const [dirty, setDirty] = useState(false)

  // Only organizations the operator actually touched are written back,
  // so opening the page never creates rows for sites that are happy
  // inheriting the defaults.
  const touchedOrgs = useRef<Set<string>>(new Set())

  // Nothing is set synchronously here: the loading flag starts true and
  // reload() raises it again from the click handler, which keeps this
  // effect from triggering a cascading render on every mount.
  useEffect(() => {
    const ac = new AbortController()
    Promise.all([fetchAlertSettings(ac.signal), fetchOrgAlertSettings(ac.signal)])
      .then(([state, orgs]) => {
        const { smtp_password_configured, saved: isSaved, ...rest } = state
        setSettings(rest)
        setPasswordConfigured(smtp_password_configured)
        setSaved(isSaved)
        setOrganizations(orgs)
        setPassword(null)
        touchedOrgs.current = new Set()
        setDirty(false)
        setLoading(false)
      })
      .catch((e: unknown) => {
        if (isAbortError(e)) return
        setError(message(e, 'Не вдалося завантажити налаштування'))
        setLoading(false)
      })
    return () => ac.abort()
  }, [reloadKey])

  const update = useCallback((patch: Partial<AlertSettings>) => {
    setSettings((prev) => (prev ? { ...prev, ...patch } : prev))
    setDirty(true)
  }, [])

  const updateSmtp = useCallback((patch: Partial<AlertSettings['smtp']>) => {
    setSettings((prev) => (prev ? { ...prev, smtp: { ...prev.smtp, ...patch } } : prev))
    setDirty(true)
  }, [])

  const updateOrganization = useCallback(
    (organizationID: string, patch: Partial<OrgAlertSettings>) => {
      setOrganizations((prev) => {
        const current = prev[organizationID] ?? { enabled: true, recipients: [] }
        return { ...prev, [organizationID]: { ...current, ...patch } }
      })
      touchedOrgs.current.add(organizationID)
      setDirty(true)
    },
    [],
  )

  const changePassword = useCallback((value: string | null) => {
    setPassword(value)
    setDirty(true)
  }, [])

  const save = useCallback(async () => {
    if (!settings) return
    setSaving(true)
    setError(null)
    try {
      await saveAlertSettings(settings, password)
      for (const organizationID of touchedOrgs.current) {
        const entry = organizations[organizationID]
        if (!entry) continue
        await saveOrgAlertSettings(organizationID, entry)
      }
      touchedOrgs.current = new Set()
      if (password !== null) setPasswordConfigured(password !== '')
      setPassword(null)
      setSaved(true)
      setDirty(false)
      setSavedAt(Date.now())
    } catch (e: unknown) {
      setError(message(e, 'Не вдалося зберегти налаштування'))
    } finally {
      setSaving(false)
    }
  }, [settings, organizations, password])

  const reload = useCallback(() => {
    setLoading(true)
    setError(null)
    setReloadKey((k) => k + 1)
  }, [])

  return useMemo(
    () => ({
      settings,
      saved,
      passwordConfigured,
      organizations,
      loading,
      saving,
      error,
      savedAt,
      dirty,
      update,
      updateSmtp,
      updateOrganization,
      setPassword: changePassword,
      password,
      save,
      reload,
    }),
    [
      settings,
      saved,
      passwordConfigured,
      organizations,
      loading,
      saving,
      error,
      savedAt,
      dirty,
      update,
      updateSmtp,
      updateOrganization,
      changePassword,
      password,
      save,
      reload,
    ],
  )
}
