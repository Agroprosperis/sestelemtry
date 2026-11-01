import { useCallback, useEffect, useState } from 'react'
import '../alerts/alerts.css'
import { useAuth } from '../auth/authContext'
import { useOrganizationParam } from '../dashboard/hooks/useOrganizationParam'
import { ModeTopBar } from '../shell/ModeTopBar'
import {
  fetchAISettings,
  saveAISettings,
  testAISettings,
  type AISettings,
  type AISettingsState,
  type AITestResult,
  type ReasoningEffort,
} from './aiSettingsClient'

type TestState =
  | { status: 'running' }
  | { status: 'ok'; result: AITestResult }
  | { status: 'error'; message: string }
  | null

function settingsOf(s: AISettingsState): AISettings {
  return {
    enabled: s.enabled,
    model: s.model,
    base_url: s.base_url,
    reasoning_effort: s.reasoning_effort,
    daily_limit: s.daily_limit,
    follow_ups: s.follow_ups,
  }
}

function sameSettings(a: AISettings, b: AISettings): boolean {
  return (
    a.enabled === b.enabled &&
    a.model.trim() === b.model.trim() &&
    a.base_url.trim().replace(/\/+$/, '') === b.base_url.trim().replace(/\/+$/, '') &&
    a.reasoning_effort === b.reasoning_effort &&
    a.daily_limit === b.daily_limit &&
    a.follow_ups === b.follow_ups
  )
}

const errorText = (e: unknown, fallback: string) => (e instanceof Error && e.message ? e.message : fallback)

// AISettingsPage is where an administrator of every organization
// connects the AI assistant: the OpenAI key, the model and the per-user
// limit. The settings serve every object and apply on save.
export function AISettingsPage() {
  const { organizationID, options, change } = useOrganizationParam('service')
  const auth = useAuth()
  const [stored, setStored] = useState<AISettingsState | null>(null)
  const [form, setForm] = useState<AISettings | null>(null)
  // key null keeps the stored key; '' clears it on save.
  const [key, setKey] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [test, setTest] = useState<TestState>(null)

  const apply = useCallback((state: AISettingsState) => {
    setStored(state)
    setForm(settingsOf(state))
    setKey(null)
  }, [])

  useEffect(() => {
    const ac = new AbortController()
    fetchAISettings(ac.signal)
      .then(apply)
      .catch((e: unknown) => {
        if (e instanceof DOMException && e.name === 'AbortError') return
        setError(errorText(e, 'Не вдалося завантажити налаштування'))
      })
    return () => ac.abort()
  }, [apply])

  const update = (patch: Partial<AISettings>) => {
    setForm((f) => (f ? { ...f, ...patch } : f))
    setSaved(false)
  }

  const dirty = !!stored && !!form && (key !== null || !sameSettings(form, settingsOf(stored)))
  const keyConfigured = !!stored?.api_key_configured
  const addressChanged =
    !!stored && !!form && form.base_url.trim().replace(/\/+$/, '') !== stored.base_url

  const save = async () => {
    if (!form) return
    setSaving(true)
    setError(null)
    try {
      await saveAISettings(form, key)
      apply(await fetchAISettings())
      setSaved(true)
      // The top bar shows the assistant by /auth/me's ai_chat.
      await auth?.refreshMe?.()
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
      setTest({ status: 'ok', result: await testAISettings(form, key) })
    } catch (e: unknown) {
      setTest({ status: 'error', message: errorText(e, 'Перевірка не вдалася') })
    }
  }

  return (
    <main className="alerts-page">
      <ModeTopBar
        mode="none"
        organizationID={organizationID}
        options={options}
        onOrganizationChange={change}
        title="ШІ-асистент"
      />

      <p className="alerts-subtitle">
        Асистент відповідає на питання про обʼєкти за даними консолі через OpenAI. Налаштування
        спільні для всіх обʼєктів і діють одразу після збереження.
      </p>

      {error ? <div className="alerts-banner alerts-banner-error">{error}</div> : null}
      {!form && !error ? <p className="alerts-muted">Завантаження…</p> : null}

      {form && stored ? (
        <>
          <section className="alerts-card">
            <span className="alerts-card-accent" />
            <div className="alerts-card-head">
              <h2 className="alerts-section-title">Підключення до OpenAI</h2>
              <label className="alerts-switch">
                <input type="checkbox" checked={form.enabled} onChange={(e) => update({ enabled: e.target.checked })} />
                <span>Увімкнути асистента</span>
              </label>
            </div>

            <div className="alerts-grid">
              <label className="alerts-field alerts-field-wide">
                <span>Ключ API</span>
                <input
                  className="alerts-input"
                  type="password"
                  autoComplete="new-password"
                  value={key ?? ''}
                  placeholder={keyConfigured && key === null ? 'збережено — введіть новий, щоб замінити' : 'sk-…'}
                  onChange={(e) => {
                    setKey(e.target.value === '' ? null : e.target.value)
                    setSaved(false)
                  }}
                />
              </label>
              <label className="alerts-field">
                <span>Модель</span>
                <input
                  className="alerts-input"
                  value={form.model}
                  placeholder="ідентифікатор моделі"
                  onChange={(e) => update({ model: e.target.value })}
                />
              </label>
              <label className="alerts-field">
                <span>Reasoning</span>
                <select
                  className="alerts-input"
                  value={form.reasoning_effort}
                  onChange={(e) => update({ reasoning_effort: e.target.value as ReasoningEffort })}
                >
                  <option value="">вимкнено (звичайна модель)</option>
                  <option value="minimal">minimal</option>
                  <option value="low">low</option>
                  <option value="medium">medium</option>
                  <option value="high">high</option>
                </select>
              </label>
              <label className="alerts-field alerts-field-wide">
                <span>Адреса API</span>
                <input
                  className="alerts-input"
                  value={form.base_url}
                  placeholder="https://api.openai.com"
                  onChange={(e) => update({ base_url: e.target.value })}
                />
              </label>
              <label className="alerts-field">
                <span>Запитів на користувача за добу</span>
                <input
                  className="alerts-input"
                  type="number"
                  min={1}
                  max={10000}
                  value={form.daily_limit}
                  onChange={(e) => update({ daily_limit: Number(e.target.value) || 0 })}
                />
              </label>
            </div>

            {keyConfigured && key === null && addressChanged ? (
              <p className="alerts-section-sub">
                Адресу API змінено: введіть ключ ще раз, щоб збережений ключ не пішов на іншу адресу.
              </p>
            ) : null}
            {keyConfigured && key === null && !addressChanged ? (
              <p className="alerts-section-sub">
                Ключ збережено. Порожнє поле лишає його без змін.{' '}
                <button
                  type="button"
                  className="alerts-secondary"
                  onClick={() => {
                    setKey('')
                    update({ enabled: false })
                  }}
                >
                  Видалити ключ
                </button>
              </p>
            ) : null}
            {keyConfigured && key === '' ? (
              <p className="alerts-section-sub">Ключ буде видалено після збереження, асистента вимкнено.</p>
            ) : null}
            <p className="alerts-section-sub">
              Модель має підтримувати виклик функцій (function calling). Reasoning вмикайте лише для
              reasoning-моделей: інші відхиляють цей параметр.
            </p>
            <label className="alerts-checkbox">
              <input
                type="checkbox"
                checked={form.follow_ups}
                onChange={(e) => update({ follow_ups: e.target.checked })}
              />
              <span>Пропонувати наступні питання кнопками під кожною відповіддю</span>
            </label>
            <p className="alerts-section-sub">
              Це ще один короткий запит до моделі після кожної відповіді; питання зважають на те, які
              дані доступні ролі користувача.
            </p>
          </section>

          <div className="alerts-actions">
            {dirty ? <span className="alerts-dirty-note">Є незбережені зміни.</span> : null}
            <button
              type="button"
              className="alerts-secondary"
              disabled={test?.status === 'running' || !form.model.trim()}
              title="Одне коротке питання до моделі з налаштуваннями, як їх введено"
              onClick={() => void runTest()}
            >
              {test?.status === 'running' ? 'Перевіряємо…' : 'Перевірити зʼєднання'}
            </button>
            <button type="button" className="alerts-save" disabled={saving || !dirty} onClick={() => void save()}>
              {saving ? 'Збереження…' : 'Зберегти'}
            </button>
            {!dirty && saved ? <span className="alerts-saved-note">Збережено</span> : null}
          </div>
          {test?.status === 'ok' ? (
            <div className="alerts-banner alerts-banner-ok">
              {test.result.model} відповіла: «{test.result.reply}» ({test.result.usage.input_tokens} +{' '}
              {test.result.usage.output_tokens} токенів)
            </div>
          ) : null}
          {test?.status === 'error' ? <div className="alerts-banner alerts-banner-error">{test.message}</div> : null}

          <p className="alerts-hint">
            <strong>Що йде в OpenAI.</strong> Питання, попередні репліки розмови і зведені цифри,
            які читають інструменти асистента, з <code>store: false</code>. Тексти розмов у базі не
            зберігаються — лише журнал використання для денного ліміту.
          </p>
          <p className="alerts-hint">
            <strong>Ключ.</strong> Зберігається в базі у відкритому вигляді, як пароль SMTP, і ніколи
            не повертається на сторінку. Після зміни адреси API його треба ввести заново.
          </p>
        </>
      ) : null}
    </main>
  )
}
