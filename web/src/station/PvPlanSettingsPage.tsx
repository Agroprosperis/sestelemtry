import { useCallback, useEffect, useState } from 'react'
import '../alerts/alerts.css'
import { useOrganizationParam } from '../dashboard/hooks/useOrganizationParam'
import { ModeTopBar } from '../shell/ModeTopBar'
import {
  fetchPvPlanSettings,
  savePvPlanSettings,
  testPvPlanSettings,
  type PvPlanSettings,
  type PvPlanSettingsState,
  type PvPlanSettingsTestResult,
  type PvPlanUpstreamCheck,
} from './pvPlanSettingsClient'

type TestState =
  | { status: 'running' }
  | { status: 'ok'; result: PvPlanSettingsTestResult }
  | { status: 'error'; message: string }
  | null

function settingsOf(s: PvPlanSettings): PvPlanSettings {
  return { forecast_url: s.forecast_url, previous_runs_url: s.previous_runs_url, n8n_webhook_url: s.n8n_webhook_url }
}

function sameSettings(a: PvPlanSettings, b: PvPlanSettings): boolean {
  return (
    a.forecast_url.trim() === b.forecast_url.trim() &&
    a.previous_runs_url.trim() === b.previous_runs_url.trim() &&
    a.n8n_webhook_url.trim() === b.n8n_webhook_url.trim()
  )
}

// openMeteoMoved mirrors the server's rule: the stored key only goes to
// the Open-Meteo addresses in force when it was saved.
function openMeteoMoved(form: PvPlanSettings, stored: PvPlanSettingsState): boolean {
  const inForce = (value: string, fallback: string) => value.trim() || fallback
  return (
    inForce(form.forecast_url, stored.defaults.forecast_url) !==
      inForce(stored.forecast_url, stored.defaults.forecast_url) ||
    inForce(form.previous_runs_url, stored.defaults.previous_runs_url) !==
      inForce(stored.previous_runs_url, stored.defaults.previous_runs_url)
  )
}

const errorText = (e: unknown, fallback: string) => (e instanceof Error && e.message ? e.message : fallback)

function checkText(label: string, check: PvPlanUpstreamCheck, unit: string): string {
  return check.ok ? `${label}: працює, ${check.hours} ${unit}` : `${label}: ${check.error ?? 'помилка'}`
}

// PvPlanSettingsPage is where an administrator of every organization
// says where the PV plan comes from: Open-Meteo (and its key) for the
// in-house model, the n8n webhook for the stations still on the flow.
// The settings serve every object and apply on save.
export function PvPlanSettingsPage() {
  const { organizationID, options, change } = useOrganizationParam('service')
  const [stored, setStored] = useState<PvPlanSettingsState | null>(null)
  const [form, setForm] = useState<PvPlanSettings | null>(null)
  // key null keeps the stored key; '' clears it on save.
  const [key, setKey] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [test, setTest] = useState<TestState>(null)

  const apply = useCallback((state: PvPlanSettingsState) => {
    setStored(state)
    setForm(settingsOf(state))
    setKey(null)
  }, [])

  useEffect(() => {
    const ac = new AbortController()
    fetchPvPlanSettings(ac.signal)
      .then(apply)
      .catch((e: unknown) => {
        if (e instanceof DOMException && e.name === 'AbortError') return
        setError(errorText(e, 'Не вдалося завантажити налаштування'))
      })
    return () => ac.abort()
  }, [apply])

  const update = (patch: Partial<PvPlanSettings>) => {
    setForm((f) => (f ? { ...f, ...patch } : f))
    setSaved(false)
  }

  const dirty = !!stored && !!form && (key !== null || !sameSettings(form, settingsOf(stored)))
  const keyConfigured = !!stored?.api_key_configured
  const moved = !!stored && !!form && openMeteoMoved(form, stored)

  const save = async () => {
    if (!form) return
    setSaving(true)
    setError(null)
    try {
      await savePvPlanSettings(form, key)
      apply(await fetchPvPlanSettings())
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
      setTest({ status: 'ok', result: await testPvPlanSettings(form, key) })
    } catch (e: unknown) {
      setTest({ status: 'error', message: errorText(e, 'Перевірка не вдалася') })
    }
  }

  const result = test?.status === 'ok' ? test.result : null
  const resultOK = !!result && result.forecast.ok && result.previous_runs.ok && (result.n8n?.ok ?? true)

  return (
    <main className="alerts-page">
      <ModeTopBar
        mode="none"
        organizationID={organizationID}
        options={options}
        onOrganizationChange={change}
        title="Прогноз СЕС"
      />

      <p className="alerts-subtitle">
        Звідки консоль бере план генерації СЕС: Open-Meteo для власної моделі і вебхук n8n для
        станцій, у паспорті яких ще вибрано n8n. Налаштування спільні для всіх обʼєктів і діють
        одразу після збереження.
      </p>

      {error ? <div className="alerts-banner alerts-banner-error">{error}</div> : null}
      {!form && !error ? <p className="alerts-muted">Завантаження…</p> : null}

      {form && stored ? (
        <>
          <section className="alerts-card">
            <span className="alerts-card-accent" />
            <div className="alerts-card-head">
              <h2 className="alerts-section-title">Open-Meteo</h2>
            </div>
            <div className="alerts-grid">
              <label className="alerts-field alerts-field-wide">
                <span>Адреса прогнозу</span>
                <input
                  className="alerts-input"
                  value={form.forecast_url}
                  placeholder={stored.defaults.forecast_url}
                  onChange={(e) => update({ forecast_url: e.target.value })}
                />
              </label>
              <label className="alerts-field alerts-field-wide">
                <span>Адреса архіву прогнозів</span>
                <input
                  className="alerts-input"
                  value={form.previous_runs_url}
                  placeholder={stored.defaults.previous_runs_url}
                  onChange={(e) => update({ previous_runs_url: e.target.value })}
                />
              </label>
              <label className="alerts-field alerts-field-wide">
                <span>Ключ API</span>
                <input
                  className="alerts-input"
                  type="password"
                  autoComplete="new-password"
                  value={key ?? ''}
                  placeholder={
                    keyConfigured && key === null
                      ? 'збережено — введіть новий, щоб замінити'
                      : 'не потрібен для безкоштовного API'
                  }
                  onChange={(e) => {
                    setKey(e.target.value === '' ? null : e.target.value)
                    setSaved(false)
                  }}
                />
              </label>
            </div>
            {keyConfigured && key === null && moved ? (
              <p className="alerts-section-sub">
                Адресу Open-Meteo змінено: введіть ключ ще раз, щоб збережений ключ не пішов на іншу адресу.
              </p>
            ) : null}
            {keyConfigured && key === null && !moved ? (
              <p className="alerts-section-sub">
                Ключ збережено. Порожнє поле лишає його без змін.{' '}
                <button type="button" className="alerts-secondary" onClick={() => setKey('')}>
                  Видалити ключ
                </button>
              </p>
            ) : null}
            {keyConfigured && key === '' ? (
              <p className="alerts-section-sub">Ключ буде видалено після збереження.</p>
            ) : null}
            <p className="alerts-section-sub">
              Порожня адреса — публічний API Open-Meteo, безкоштовний лише для некомерційного
              використання. Для комерційного тарифу вкажіть адреси й ключ з кабінету Open-Meteo.
              Архів прогнозів потрібен для плану на минулі дні («Перерахувати історію плану» в
              паспорті станції).
            </p>
          </section>

          <section className="alerts-card">
            <span className="alerts-card-accent alerts-card-accent-violet" />
            <div className="alerts-card-head">
              <h2 className="alerts-section-title">n8n</h2>
            </div>
            <div className="alerts-grid">
              <label className="alerts-field alerts-field-wide">
                <span>Адреса вебхука</span>
                <input
                  className="alerts-input"
                  value={form.n8n_webhook_url}
                  placeholder={stored.defaults.n8n_webhook_url}
                  onChange={(e) => update({ n8n_webhook_url: e.target.value })}
                />
              </label>
            </div>
            <p className="alerts-section-sub">
              Потрібен, доки хоч одна станція в паспорті читає план з n8n. Ключ Open-Meteo сюди не
              надсилається.
            </p>
          </section>

          <div className="alerts-actions">
            {dirty ? <span className="alerts-dirty-note">Є незбережені зміни.</span> : null}
            <button
              type="button"
              className="alerts-secondary"
              disabled={test?.status === 'running'}
              title="Один запит до кожного джерела з налаштуваннями, як їх введено"
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
            <div className={`alerts-banner ${resultOK ? 'alerts-banner-ok' : 'alerts-banner-error'}`}>
              <div>{checkText('Прогноз Open-Meteo', result.forecast, 'год')}</div>
              <div>{checkText('Архів прогнозів', result.previous_runs, 'год')}</div>
              {result.n8n ? (
                <div>
                  {checkText(`n8n (${result.n8n_organization_id ?? ''}, сьогодні)`, result.n8n, 'год з генерацією')}
                </div>
              ) : null}
            </div>
          ) : null}
          {test?.status === 'error' ? <div className="alerts-banner alerts-banner-error">{test.message}</div> : null}

          <p className="alerts-hint">
            <strong>Ключ.</strong> Зберігається в базі у відкритому вигляді, як пароль SMTP, і ніколи
            не повертається на сторінку. Після зміни адреси Open-Meteo його треба ввести заново.
          </p>
        </>
      ) : null}
    </main>
  )
}
