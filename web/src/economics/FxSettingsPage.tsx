import { useCallback, useEffect, useState } from 'react'
import '../alerts/alerts.css'
import { useOrganizationParam } from '../dashboard/hooks/useOrganizationParam'
import { ModeTopBar } from '../shell/ModeTopBar'
import {
  fetchFxSettings,
  saveFxSettings,
  testFxSettings,
  type FxCoverage,
  type FxSettings,
  type FxSettingsState,
  type FxSettingsTestResult,
} from './fxSettingsClient'

type TestState =
  | { status: 'running' }
  | { status: 'ok'; result: FxSettingsTestResult }
  | { status: 'error'; message: string }
  | null

function settingsOf(s: FxSettings): FxSettings {
  return { url: s.url, username: s.username }
}

function sameSettings(a: FxSettings, b: FxSettings): boolean {
  return a.url.trim() === b.url.trim() && a.username.trim() === b.username.trim()
}

// addressMoved mirrors the server's rule: the stored password only goes
// to the address in force when it was saved.
function addressMoved(form: FxSettings, stored: FxSettingsState): boolean {
  const inForce = (value: string) => value.trim() || stored.default_url
  return inForce(form.url) !== inForce(stored.url)
}

const errorText = (e: unknown, fallback: string) => (e instanceof Error && e.message ? e.message : fallback)

const rateFmt = new Intl.NumberFormat('uk-UA', { minimumFractionDigits: 2, maximumFractionDigits: 4 })

function formatDay(iso: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(iso)
  return m ? `${m[3]}.${m[2]}.${m[1]}` : iso
}

function formatMoment(iso: string): string {
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

function coverageText(c: FxCoverage): string {
  if (c.days === 0) return `${c.currency}: курсів ще немає`
  const missing = c.missing_days > 0 ? `, бракує ${c.missing_days} дн.` : ', без пропусків'
  return `${c.currency}: з ${formatDay(c.first_day)} по ${formatDay(c.last_day)} — ${c.days} дн.${missing}`
}

// FxSettingsPage is where an administrator of every organization says
// which ERP account the exchange rates come from. The economics page
// converts each day's UAH at that day's rate; the rates are synced in the
// background and the settings apply on save.
export function FxSettingsPage() {
  const { organizationID, options, change } = useOrganizationParam('economics.read')
  const [stored, setStored] = useState<FxSettingsState | null>(null)
  const [form, setForm] = useState<FxSettings | null>(null)
  // password null keeps the stored one; '' clears it on save.
  const [password, setPassword] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [test, setTest] = useState<TestState>(null)

  const apply = useCallback((state: FxSettingsState) => {
    setStored(state)
    setForm(settingsOf(state))
    setPassword(null)
  }, [])

  const reload = useCallback(
    (signal?: AbortSignal) =>
      fetchFxSettings(signal)
        .then(apply)
        .catch((e: unknown) => {
          if (e instanceof DOMException && e.name === 'AbortError') return
          setError(errorText(e, 'Не вдалося завантажити налаштування'))
        }),
    [apply],
  )

  useEffect(() => {
    const ac = new AbortController()
    void reload(ac.signal)
    return () => ac.abort()
  }, [reload])

  const update = (patch: Partial<FxSettings>) => {
    setForm((f) => (f ? { ...f, ...patch } : f))
    setSaved(false)
  }

  const dirty = !!stored && !!form && (password !== null || !sameSettings(form, settingsOf(stored)))
  const passwordConfigured = !!stored?.password_configured
  const moved = !!stored && !!form && addressMoved(form, stored)

  const save = async () => {
    if (!form) return
    setSaving(true)
    setError(null)
    try {
      await saveFxSettings(form, password)
      apply(await fetchFxSettings())
      setSaved(true)
    } catch (e: unknown) {
      setError(errorText(e, 'Не вдалося зберегти налаштування'))
    } finally {
      setSaving(false)
    }
  }

  const runTest = async () => {
    if (!form) return
    setTest({ status: 'running' })
    try {
      setTest({ status: 'ok', result: await testFxSettings(form, password) })
    } catch (e: unknown) {
      setTest({ status: 'error', message: errorText(e, 'Перевірка не вдалася') })
    }
  }

  const result = test?.status === 'ok' ? test.result : null
  const sync = stored?.sync

  return (
    <main className="alerts-page">
      <ModeTopBar
        mode="none"
        organizationID={organizationID}
        options={options}
        onOrganizationChange={change}
        title="Курси валют ERP"
      />

      <p className="alerts-subtitle">
        Звідки економіка бере курси USD і EUR: сервіс курсів АП Банку в ERP. Кожна доба
        перераховується за курсом на 12:00 своєї дати. Налаштування спільні для всіх обʼєктів і діють
        одразу після збереження.
      </p>

      {error ? <div className="alerts-banner alerts-banner-error">{error}</div> : null}
      {!form && !error ? <p className="alerts-muted">Завантаження…</p> : null}

      {form && stored ? (
        <>
          <section className="alerts-card">
            <span className="alerts-card-accent" />
            <div className="alerts-card-head">
              <h2 className="alerts-section-title">Підключення до ERP</h2>
            </div>
            <div className="alerts-grid">
              <label className="alerts-field alerts-field-wide">
                <span>Адреса сервісу курсів</span>
                <input
                  className="alerts-input"
                  value={form.url}
                  placeholder={stored.default_url}
                  onChange={(e) => update({ url: e.target.value })}
                />
              </label>
              <label className="alerts-field">
                <span>Логін</span>
                <input
                  className="alerts-input"
                  autoComplete="off"
                  value={form.username}
                  onChange={(e) => update({ username: e.target.value })}
                />
              </label>
              <label className="alerts-field">
                <span>Пароль</span>
                <input
                  className="alerts-input"
                  type="password"
                  autoComplete="new-password"
                  value={password ?? ''}
                  placeholder={passwordConfigured && password === null ? 'збережено — введіть новий, щоб замінити' : ''}
                  onChange={(e) => {
                    setPassword(e.target.value === '' ? null : e.target.value)
                    setSaved(false)
                  }}
                />
              </label>
            </div>
            {passwordConfigured && password === null && moved ? (
              <p className="alerts-section-sub">
                Адресу змінено: введіть пароль ще раз, щоб збережений пароль не пішов на іншу адресу.
              </p>
            ) : null}
            {passwordConfigured && password === null && !moved ? (
              <p className="alerts-section-sub">
                Пароль збережено. Порожнє поле лишає його без змін.{' '}
                <button type="button" className="alerts-secondary" onClick={() => setPassword('')}>
                  Видалити пароль
                </button>
              </p>
            ) : null}
            {passwordConfigured && password === '' ? (
              <p className="alerts-section-sub">Пароль буде видалено після збереження.</p>
            ) : null}
            <p className="alerts-section-sub">Порожня адреса — сервіс курсів робочої бази APGroup.</p>
          </section>

          <div className="alerts-actions">
            {dirty ? <span className="alerts-dirty-note">Є незбережені зміни.</span> : null}
            <button
              type="button"
              className="alerts-secondary"
              disabled={test?.status === 'running'}
              title="Один запит до ERP за сьогоднішніми курсами з налаштуваннями, як їх введено"
              onClick={() => void runTest()}
            >
              {test?.status === 'running' ? 'Перевіряємо…' : 'Перевірити зʼєднання'}
            </button>
            <button type="button" className="alerts-save" disabled={saving || !dirty} onClick={() => void save()}>
              {saving ? 'Збереження…' : 'Зберегти'}
            </button>
            {!dirty && saved ? <span className="alerts-saved-note">Збережено</span> : null}
          </div>
          {result ? (
            <div className={`alerts-banner ${result.ok ? 'alerts-banner-ok' : 'alerts-banner-error'}`}>
              {result.ok && result.rates ? (
                <div>
                  ERP відповіла на {formatMoment(result.at)}:{' '}
                  {Object.entries(result.rates)
                    .map(([cur, rate]) => `1 ${cur} = ${rateFmt.format(rate)} грн`)
                    .join(', ')}
                </div>
              ) : (
                <div>{result.error ?? 'Помилка'}</div>
              )}
            </div>
          ) : null}
          {test?.status === 'error' ? <div className="alerts-banner alerts-banner-error">{test.message}</div> : null}

          <section className="alerts-card">
            <span className="alerts-card-accent alerts-card-accent-violet" />
            <div className="alerts-card-head">
              <h2 className="alerts-section-title">Курси в базі</h2>
              <button type="button" className="alerts-secondary" onClick={() => void reload()}>
                Оновити
              </button>
            </div>
            {stored.coverage.map((c) => (
              <p key={c.currency} className="alerts-section-sub">
                {coverageText(c)}
              </p>
            ))}
            {sync && !sync.configured ? (
              <p className="alerts-section-sub">Синхронізація чекає на логін і пароль.</p>
            ) : null}
            {sync && sync.configured && sync.last_error ? (
              <p className="alerts-section-sub">
                Остання спроба {formatMoment(sync.last_attempt)} не вдалася: {sync.last_error}
              </p>
            ) : null}
            {sync && sync.configured && !sync.last_error && formatMoment(sync.last_success) ? (
              <p className="alerts-section-sub">Остання синхронізація: {formatMoment(sync.last_success)}.</p>
            ) : null}
            <p className="alerts-section-sub">
              Курси зберігаються з {formatDay(stored.first_day)}. Минулі дні дозаповнюються один раз, сьогоднішній
              курс оновлюється щогодини.
            </p>
          </section>

          <p className="alerts-hint">
            <strong>Пароль.</strong> Зберігається в базі у відкритому вигляді, як пароль SMTP, і ніколи не
            повертається на сторінку. Після зміни адреси його треба ввести заново.
          </p>
        </>
      ) : null}
    </main>
  )
}
