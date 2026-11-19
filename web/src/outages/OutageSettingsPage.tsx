import { useCallback, useEffect, useState } from 'react'
import '../alerts/alerts.css'
import { useOrganizationParam } from '../dashboard/hooks/useOrganizationParam'
import { ModeTopBar } from '../shell/ModeTopBar'
import {
  fetchOutageSettings,
  saveOutageSettings,
  testOutageAccount,
  type OutageAccount,
  type OutageSettingsState,
  type OutageTestResult,
} from './outageSettingsClient'

type AccountForm = {
  login: string
  // password null keeps the stored one; '' clears it on save.
  password: string | null
  // eic is only for «Перевірити зʼєднання»; it is not saved here (the
  // point's EIC lives on the station passport).
  eic: string
}

type TestState =
  | { status: 'running' }
  | { status: 'ok'; result: OutageTestResult }
  | { status: 'error'; message: string }

const errorText = (e: unknown, fallback: string) => (e instanceof Error && e.message ? e.message : fallback)

function formFrom(accounts: OutageAccount[]): Record<string, AccountForm> {
  const out: Record<string, AccountForm> = {}
  for (const a of accounts) out[a.host] = { login: a.login, password: null, eic: '' }
  return out
}

function formatMoment(iso?: string): string {
  if (!iso || iso.startsWith('0001-')) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return new Intl.DateTimeFormat('uk-UA', {
    timeZone: 'Europe/Kyiv',
    day: '2-digit',
    month: '2-digit',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  }).format(d)
}

const KIND_LABELS = { schedule: 'за графіком', other: 'інше' } as const

function intervalText(start: string, end: string, kind: keyof typeof KIND_LABELS): string {
  return `${formatMoment(start)} — ${formatMoment(end)} (${KIND_LABELS[kind] ?? kind})`
}

// OutageSettingsPage is where an administrator of every organization
// enters the e-svitlo cabinets (one per grid operator) the planned
// outages are read from. The supply point's EIC is not here: it is set on
// each station's passport.
export function OutageSettingsPage() {
  const { organizationID, options, change } = useOrganizationParam('economics.read')
  const [stored, setStored] = useState<OutageSettingsState | null>(null)
  const [forms, setForms] = useState<Record<string, AccountForm>>({})
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [tests, setTests] = useState<Record<string, TestState>>({})

  const apply = useCallback((state: OutageSettingsState) => {
    setStored(state)
    setForms((prev) => {
      // Keep what was typed into the test EIC fields across a reload.
      const next = formFrom(state.accounts)
      for (const host of Object.keys(next)) next[host].eic = prev[host]?.eic ?? ''
      return next
    })
  }, [])

  useEffect(() => {
    const ac = new AbortController()
    fetchOutageSettings(ac.signal)
      .then(apply)
      .catch((e: unknown) => {
        if (ac.signal.aborted) return
        setError(errorText(e, 'Не вдалося завантажити налаштування'))
      })
    return () => ac.abort()
  }, [apply])

  const update = (host: string, patch: Partial<AccountForm>) => {
    setForms((f) => ({ ...f, [host]: { ...f[host], ...patch } }))
    setSaved(false)
  }

  const dirty =
    !!stored &&
    stored.accounts.some((a) => {
      const f = forms[a.host]
      return !!f && (f.password !== null || f.login.trim() !== a.login.trim())
    })

  const save = async () => {
    if (!stored) return
    setSaving(true)
    setError(null)
    try {
      await saveOutageSettings(
        stored.accounts.map((a) => {
          const f = forms[a.host]
          const input: { host: string; login: string; password?: string } = {
            host: a.host,
            login: f?.login.trim() ?? a.login,
          }
          if (f && f.password !== null) input.password = f.password
          return input
        }),
      )
      apply(await fetchOutageSettings())
      setSaved(true)
    } catch (e: unknown) {
      setError(errorText(e, 'Не вдалося зберегти налаштування'))
    } finally {
      setSaving(false)
    }
  }

  const runTest = async (host: string) => {
    const f = forms[host]
    if (!f) return
    setTests((t) => ({ ...t, [host]: { status: 'running' } }))
    try {
      const result = await testOutageAccount({
        host,
        login: f.login.trim(),
        password: f.password ?? undefined,
        eic: f.eic.trim() || undefined,
      })
      setTests((t) => ({ ...t, [host]: { status: 'ok', result } }))
    } catch (e: unknown) {
      setTests((t) => ({ ...t, [host]: { status: 'error', message: errorText(e, 'Перевірка не вдалася') } }))
    }
  }

  const sync = stored?.sync

  return (
    <main className="alerts-page">
      <ModeTopBar
        mode="none"
        organizationID={organizationID}
        options={options}
        onOrganizationChange={change}
        title="Відключення e-svitlo"
      />

      <p className="alerts-subtitle">
        Заплановані відключення (графіки та ГПВ) беруться з особистих кабінетів e-svitlo обленерго. Тут
        вводяться логін і пароль кабінету кожного оператора, а код точки обліку (EIC) задається в паспорті
        станції. Налаштування спільні для всіх обʼєктів і діють одразу після збереження.
      </p>

      {error ? <div className="alerts-banner alerts-banner-error">{error}</div> : null}
      {!stored && !error ? <p className="alerts-muted">Завантаження…</p> : null}

      {stored ? (
        <>
          {stored.accounts.map((a, i) => {
            const f = forms[a.host]
            if (!f) return null
            const test = tests[a.host]
            const result = test?.status === 'ok' ? test.result : null
            const passwordConfigured = a.password_configured
            return (
              <section className="alerts-card" key={a.host}>
                <span className={`alerts-card-accent${i % 2 ? ' alerts-card-accent-violet' : ''}`} />
                <div className="alerts-card-head">
                  <h2 className="alerts-section-title">{a.name}</h2>
                  <span className="alerts-muted">{a.host}</span>
                </div>
                <div className="alerts-grid">
                  <label className="alerts-field">
                    <span>Логін</span>
                    <input
                      className="alerts-input"
                      autoComplete="off"
                      value={f.login}
                      onChange={(e) => update(a.host, { login: e.target.value })}
                    />
                  </label>
                  <label className="alerts-field">
                    <span>Пароль</span>
                    <input
                      className="alerts-input"
                      type="password"
                      autoComplete="new-password"
                      value={f.password ?? ''}
                      placeholder={
                        passwordConfigured && f.password === null ? 'збережено — введіть новий, щоб замінити' : ''
                      }
                      onChange={(e) => update(a.host, { password: e.target.value === '' ? null : e.target.value })}
                    />
                  </label>
                  <label className="alerts-field">
                    <span>EIC для перевірки (необовʼязково)</span>
                    <input
                      className="alerts-input"
                      autoComplete="off"
                      maxLength={16}
                      placeholder="16 символів"
                      value={f.eic}
                      onChange={(e) => update(a.host, { eic: e.target.value })}
                    />
                  </label>
                </div>
                {passwordConfigured && f.password === null ? (
                  <p className="alerts-section-sub">
                    Пароль збережено. Порожнє поле лишає його без змін.{' '}
                    <button type="button" className="alerts-secondary" onClick={() => update(a.host, { password: '' })}>
                      Видалити пароль
                    </button>
                  </p>
                ) : null}
                {passwordConfigured && f.password === '' ? (
                  <p className="alerts-section-sub">Пароль буде видалено після збереження.</p>
                ) : null}
                {!passwordConfigured && f.password === null ? (
                  <p className="alerts-section-sub">Пароль ще не задано.</p>
                ) : null}
                <div className="alerts-actions">
                  <button
                    type="button"
                    className="alerts-secondary"
                    disabled={test?.status === 'running' || !f.login.trim()}
                    title="Вхід у кабінет із введеними даними; з EIC — ще й читання відключень точки"
                    onClick={() => void runTest(a.host)}
                  >
                    {test?.status === 'running' ? 'Перевіряємо…' : 'Перевірити зʼєднання'}
                  </button>
                </div>
                {result ? (
                  <div className={`alerts-banner ${result.ok ? 'alerts-banner-ok' : 'alerts-banner-error'}`}>
                    {result.ok ? (
                      <div>
                        Кабінет відповів {formatMoment(result.at)}: вхід виконано
                        {f.eic.trim()
                          ? (result.outages ?? []).length > 0
                            ? `, відключень: ${(result.outages ?? []).length}`
                            : ', відключень немає'
                          : ''}
                        .
                        {(result.outages ?? []).map((o) => (
                          <div key={`${o.start}-${o.end}`}>{intervalText(o.start, o.end, o.kind)}</div>
                        ))}
                      </div>
                    ) : (
                      <div>
                        {result.error ?? 'Помилка'}
                        {!result.signed_in ? ' (вхід не виконано)' : ''}
                      </div>
                    )}
                  </div>
                ) : null}
                {test?.status === 'error' ? (
                  <div className="alerts-banner alerts-banner-error">{test.message}</div>
                ) : null}
              </section>
            )
          })}

          <div className="alerts-actions">
            {dirty ? <span className="alerts-dirty-note">Є незбережені зміни.</span> : null}
            <button type="button" className="alerts-save" disabled={saving || !dirty} onClick={() => void save()}>
              {saving ? 'Збереження…' : 'Зберегти все'}
            </button>
            {!dirty && saved ? <span className="alerts-saved-note">Збережено</span> : null}
          </div>

          {sync ? (
            <section className="alerts-card">
              <span className="alerts-card-accent alerts-card-accent-violet" />
              <div className="alerts-card-head">
                <h2 className="alerts-section-title">Синхронізація</h2>
              </div>
              {!sync.configured ? (
                <p className="alerts-section-sub">Синхронізація чекає на логін і пароль хоча б одного кабінету.</p>
              ) : null}
              {sync.configured && sync.last_error ? (
                <p className="alerts-section-sub">
                  Остання спроба {formatMoment(sync.last_attempt)} не вдалася: {sync.last_error}
                </p>
              ) : null}
              {sync.configured && !sync.last_error && formatMoment(sync.last_success) ? (
                <p className="alerts-section-sub">Остання синхронізація: {formatMoment(sync.last_success)}.</p>
              ) : null}
            </section>
          ) : null}

          <p className="alerts-hint">
            <strong>Пароль.</strong> Зберігається в базі у відкритому вигляді, як пароль SMTP, і ніколи не
            повертається на сторінку.
          </p>
        </>
      ) : null}
    </main>
  )
}
