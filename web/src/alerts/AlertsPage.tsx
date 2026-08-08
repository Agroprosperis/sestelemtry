import { useCallback, useEffect, useState } from 'react'
import { fetchOrganizations } from '../api'
import { KNOWN_ORGANIZATIONS, formatOrganizationLabel } from '../dashboard/config'
import './alerts.css'
import {
  type AlertSettings,
  type SmtpTlsMode,
  parseRecipients,
  sendAlertTestEmail,
} from './alertsClient'
import { useAlertSettings } from './useAlertSettings'

function backToDashboard() {
  if (typeof window === 'undefined') return
  const url = new URL(window.location.href)
  url.searchParams.delete('view')
  window.history.pushState({}, '', url.toString())
  window.dispatchEvent(new PopStateEvent('popstate'))
}

type OrgOption = { id: string; name: string }

// useOrganizationList reads the deployment's real site list so the page
// covers every organization the collector polls, not just the ids the
// frontend happens to know about. Falls back to the bundled list when
// the endpoint is unavailable.
function useOrganizationList(): OrgOption[] {
  const [orgs, setOrgs] = useState<OrgOption[]>([])

  useEffect(() => {
    const ac = new AbortController()
    fetchOrganizations(ac.signal)
      .then((res) => {
        const list = (res.organizations ?? []).map((o) => ({
          id: o.id,
          name: o.name || formatOrganizationLabel(o.id),
        }))
        setOrgs(list)
      })
      .catch(() => {
        setOrgs(
          KNOWN_ORGANIZATIONS.map((id) => ({ id, name: formatOrganizationLabel(id) })),
        )
      })
    return () => ac.abort()
  }, [])

  return orgs
}

type TestState =
  | { target: string; status: 'sending' }
  | { target: string; status: 'ok'; recipients: string[] }
  | { target: string; status: 'error'; message: string }
  | null

export function AlertsPage() {
  const {
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
    setPassword,
    password,
    save,
  } = useAlertSettings()
  const orgs = useOrganizationList()
  const [test, setTest] = useState<TestState>(null)

  // The test email is sent by the server from the stored settings, so
  // an address that is only typed into the form would produce a
  // baffling "no recipients configured". Make saving the precondition
  // instead of letting the operator discover it from an error.
  const testHint = dirty
    ? 'Спочатку збережіть налаштування — тест використовує збережені значення'
    : 'Надіслати на фактичний список адрес'

  const runTest = useCallback(async (organizationID?: string) => {
    const target = organizationID ?? ''
    setTest({ target, status: 'sending' })
    try {
      const recipients = await sendAlertTestEmail(organizationID)
      setTest({ target, status: 'ok', recipients })
    } catch (e: unknown) {
      setTest({
        target,
        status: 'error',
        message: e instanceof Error ? e.message : 'Не вдалося надіслати лист',
      })
    }
  }, [])

  return (
    <main className="alerts-page">
      <header className="alerts-header">
        <button type="button" className="alerts-back" onClick={backToDashboard}>
          ← Дашборд
        </button>
        <div className="alerts-heading">
          <h1>Сповіщення</h1>
          <p className="alerts-subtitle">
            Лист на пошту, коли обладнання перестає надсилати дані
          </p>
        </div>
      </header>

      {loading ? <p className="alerts-muted">Завантаження…</p> : null}
      {error ? <div className="alerts-banner alerts-banner-error">{error}</div> : null}

      {settings ? (
        <>
          {!saved ? (
            <div className="alerts-banner alerts-banner-info">
              Показано значення з <code>config.yaml</code>. Після збереження
              налаштування зберігаються в базі, і служба сповіщень підхоплює їх
              без перезапуску.
            </div>
          ) : null}

          <GeneralCard
            settings={settings}
            passwordConfigured={passwordConfigured}
            password={password}
            savedAt={savedAt}
            update={update}
            updateSmtp={updateSmtp}
            setPassword={setPassword}
          />

          <section className="alerts-card">
            <span className="alerts-card-accent alerts-card-accent-violet" />
            <div className="alerts-card-head">
              <h2 className="alerts-section-title">Організації</h2>
            </div>
            <p className="alerts-section-sub">
              Власний список адрес <strong>замінює</strong> загальний. Порожнє
              поле означає, що організація отримує листи на загальний список.
            </p>
            <table className="alerts-orgs">
              <thead>
                <tr>
                  <th>Організація</th>
                  <th className="alerts-col-toggle">Сповіщати</th>
                  <th>Отримувачі</th>
                  <th className="alerts-col-test" />
                </tr>
              </thead>
              <tbody>
                {orgs.map((org) => {
                  const entry = organizations[org.id]
                  const enabled = entry?.enabled ?? true
                  const recipients = entry?.recipients ?? []
                  return (
                    <tr key={org.id}>
                      <td>
                        <span className="alerts-org-name">{org.name}</span>
                        <span className="alerts-org-id">{org.id}</span>
                      </td>
                      <td className="alerts-col-toggle">
                        <input
                          type="checkbox"
                          checked={enabled}
                          aria-label={`Сповіщати про ${org.name}`}
                          onChange={(e) =>
                            updateOrganization(org.id, { enabled: e.target.checked })
                          }
                        />
                      </td>
                      <td>
                        <input
                          key={`${org.id}-${savedAt ?? 0}`}
                          type="text"
                          className="alerts-input"
                          defaultValue={recipients.join(', ')}
                          placeholder="порожньо = загальний список"
                          aria-label={`Отримувачі для ${org.name}`}
                          onChange={(e) =>
                            updateOrganization(org.id, {
                              recipients: parseRecipients(e.target.value),
                            })
                          }
                        />
                      </td>
                      <td className="alerts-col-test">
                        <button
                          type="button"
                          className="alerts-secondary"
                          disabled={
                            dirty || (test?.target === org.id && test.status === 'sending')
                          }
                          title={testHint}
                          onClick={() => runTest(org.id)}
                        >
                          Тест
                        </button>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
            {test && test.target !== '' ? <TestResult test={test} /> : null}
          </section>

          <div className="alerts-actions">
            {dirty ? (
              <span className="alerts-dirty-note">
                Є незбережені зміни. Тестовий лист надсилає сервер за
                збереженими налаштуваннями.
              </span>
            ) : null}
            <button
              type="button"
              className="alerts-secondary"
              disabled={dirty || (test?.target === '' && test.status === 'sending')}
              title={testHint}
              onClick={() => runTest()}
            >
              {test?.target === '' && test.status === 'sending'
                ? 'Надсилаємо…'
                : 'Надіслати тестовий лист'}
            </button>
            <button
              type="button"
              className="alerts-save"
              disabled={saving || !dirty}
              onClick={() => void save()}
            >
              {saving ? 'Збереження…' : 'Зберегти'}
            </button>
            {!dirty && savedAt ? (
              <span className="alerts-saved-note">Збережено</span>
            ) : null}
          </div>
          {test && test.target === '' ? <TestResult test={test} /> : null}

          <p className="alerts-hint">
            Тестовий лист надсилає сервер за збереженими налаштуваннями, на
            фактичний список адрес і незалежно від вмикачів — саме щоб
            перевірити доставку до того, як станеться аварія. Тому спершу
            «Зберегти», потім «Тест».
          </p>
          <p className="alerts-hint">
            <strong>Доступ.</strong> API дашборда не має автентифікації, а пароль
            SMTP зберігається в базі у відкритому вигляді. Не публікуйте порт
            API назовні — тримайте його у внутрішній мережі або за проксі з
            авторизацією.
          </p>
        </>
      ) : null}
    </main>
  )
}

function TestResult({ test }: { test: NonNullable<TestState> }) {
  if (test.status === 'sending') {
    return <p className="alerts-muted">Надсилаємо тестовий лист…</p>
  }
  if (test.status === 'error') {
    return <div className="alerts-banner alerts-banner-error">{test.message}</div>
  }
  return (
    <div className="alerts-banner alerts-banner-ok">
      Лист надіслано: {test.recipients.join(', ')}
    </div>
  )
}

function GeneralCard({
  settings,
  passwordConfigured,
  password,
  savedAt,
  update,
  updateSmtp,
  setPassword,
}: {
  settings: AlertSettings
  passwordConfigured: boolean
  password: string | null
  savedAt: number | null
  update: (patch: Partial<AlertSettings>) => void
  updateSmtp: (patch: Partial<AlertSettings['smtp']>) => void
  setPassword: (value: string | null) => void
}) {
  return (
    <section className="alerts-card">
      <span className="alerts-card-accent" />
      <div className="alerts-card-head">
        <h2 className="alerts-section-title">Загальні налаштування</h2>
        <label className="alerts-switch">
          <input
            type="checkbox"
            checked={settings.enabled}
            onChange={(e) => update({ enabled: e.target.checked })}
          />
          <span>Надсилати сповіщення</span>
        </label>
      </div>

      <h3 className="alerts-group-title">Пороги</h3>
      <div className="alerts-grid">
        <label className="alerts-field">
          <span>Період перевірки</span>
          <input
            className="alerts-input"
            value={settings.check_interval}
            placeholder="1m"
            onChange={(e) => update({ check_interval: e.target.value })}
          />
        </label>
        <label className="alerts-field">
          <span>Тиша = аварія</span>
          <input
            className="alerts-input"
            value={settings.stale_after}
            placeholder="10m"
            onChange={(e) => update({ stale_after: e.target.value })}
          />
        </label>
        <label className="alerts-field">
          <span>Нагадувати кожні</span>
          <input
            className="alerts-input"
            value={settings.repeat_interval}
            placeholder="6h"
            onChange={(e) => update({ repeat_interval: e.target.value })}
          />
        </label>
      </div>
      <p className="alerts-section-sub">
        Тривалості у форматі <code>10m</code>, <code>6h</code>.{' '}
        <code>0s</code> у полі нагадувань вимикає повторні листи про ту саму
        аварію.
      </p>
      <label className="alerts-checkbox">
        <input
          type="checkbox"
          checked={settings.notify_recovery}
          onChange={(e) => update({ notify_recovery: e.target.checked })}
        />
        <span>Повідомляти про відновлення звʼязку</span>
      </label>

      <h3 className="alerts-group-title">Поштовий сервер</h3>
      <div className="alerts-grid">
        <label className="alerts-field alerts-field-wide">
          <span>Хост</span>
          <input
            className="alerts-input"
            value={settings.smtp.host}
            placeholder="smtp.gmail.com"
            onChange={(e) => updateSmtp({ host: e.target.value })}
          />
        </label>
        <label className="alerts-field">
          <span>Порт</span>
          <input
            className="alerts-input"
            type="number"
            value={settings.smtp.port}
            onChange={(e) => updateSmtp({ port: Number(e.target.value) || 0 })}
          />
        </label>
        <label className="alerts-field">
          <span>Шифрування</span>
          <select
            className="alerts-input"
            value={settings.smtp.tls}
            onChange={(e) => updateSmtp({ tls: e.target.value as SmtpTlsMode })}
          >
            <option value="starttls">STARTTLS (587)</option>
            <option value="implicit">SSL/TLS (465)</option>
            <option value="none">Без шифрування</option>
          </select>
        </label>
        <label className="alerts-field">
          <span>Користувач</span>
          <input
            className="alerts-input"
            value={settings.smtp.username}
            placeholder="alerts@example.com"
            onChange={(e) => updateSmtp({ username: e.target.value })}
          />
        </label>
        <label className="alerts-field">
          <span>Пароль</span>
          <input
            key={`password-${savedAt ?? 0}`}
            className="alerts-input"
            type="password"
            autoComplete="new-password"
            value={password ?? ''}
            placeholder={passwordConfigured ? 'збережено — введіть новий, щоб замінити' : ''}
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>
        <label className="alerts-field alerts-field-wide">
          <span>Відправник</span>
          <input
            className="alerts-input"
            value={settings.smtp.from}
            placeholder="СЕС Моніторинг <alerts@example.com>"
            onChange={(e) => updateSmtp({ from: e.target.value })}
          />
        </label>
      </div>
      {settings.smtp.tls === 'none' ? (
        <p className="alerts-section-sub">
          Без шифрування логін і пароль ідуть мережею у відкритому вигляді —
          лише для внутрішнього релея. Якщо релей пропускає пошту за IP,
          лишіть «Користувач» порожнім.
        </p>
      ) : null}
      {passwordConfigured && password === null ? (
        <p className="alerts-section-sub">
          Пароль уже збережено. Порожнє поле лишає його без змін.
        </p>
      ) : null}

      <h3 className="alerts-group-title">Отримувачі за замовчуванням</h3>
      <textarea
        key={`recipients-${savedAt ?? 0}`}
        className="alerts-input alerts-textarea"
        rows={3}
        defaultValue={settings.recipients.join(', ')}
        placeholder="ops@example.com, boss@example.com"
        aria-label="Отримувачі за замовчуванням"
        onChange={(e) => update({ recipients: parseRecipients(e.target.value) })}
      />
      <p className="alerts-section-sub">
        Адреси через кому, крапку з комою або з нового рядка. Ці отримувачі
        дізнаються про всі організації, крім тих, що мають власний список.
      </p>
    </section>
  )
}
