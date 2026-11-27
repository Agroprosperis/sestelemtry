import { useEffect, useState } from 'react'
import {
  fetchOutageAccount,
  saveOutageAccount,
  testOutageAccount,
  type OutageAccount,
  type OutageAccountInput,
  type OutageInterval,
  type OutageTestResult,
} from './outagesClient'
import { formatMoment, outageDays } from './outageText'

type Props = {
  organizationID: string
  // canEdit is the station's admin (service); without it the block only
  // shows how the reading goes.
  canEdit: boolean
}

const EIC_LENGTH = 16

type TestState =
  | { status: 'running' }
  | { status: 'done'; result: OutageTestResult }
  | { status: 'failed'; message: string }
  | null

function formOf(a: OutageAccount): OutageAccountInput {
  return { host: a.host || a.operators[0]?.host || '', login: a.login, eic: a.eic }
}

function sameForm(a: OutageAccountInput, b: OutageAccountInput): boolean {
  return a.host === b.host && a.login.trim() === b.login.trim() && a.eic.trim().toUpperCase() === b.eic.trim().toUpperCase()
}

const errorText = (e: unknown) => (e instanceof Error && e.message ? e.message : String(e))

function OutageList({ intervals }: { intervals: OutageInterval[] }) {
  const days = outageDays(intervals)
  if (days.length === 0) return <p className="pvplan-status">Запланованих відключень немає.</p>
  return (
    <ul className="outage-days">
      {days.map((d) => (
        <li key={d.label}>
          <b>{d.label}:</b> {d.ranges.join(', ')}
        </li>
      ))}
    </ul>
  )
}

// OutageAccountSection is the «Відключення e-svitlo» block of the station
// passport: the elevator's own cabinet the planned outages are read from
// every hour, how the last read went, and the outages ahead. The parent
// keys it by station.
export function OutageAccountSection({ organizationID, canEdit }: Props) {
  const [account, setAccount] = useState<OutageAccount | null>(null)
  const [form, setForm] = useState<OutageAccountInput | null>(null)
  // password null keeps the stored one; the page never holds it.
  const [password, setPassword] = useState<string | null>(null)
  const [loadError, setLoadError] = useState('')
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState('')
  const [error, setError] = useState('')
  const [test, setTest] = useState<TestState>(null)

  const apply = (a: OutageAccount) => {
    setAccount(a)
    setForm(formOf(a))
    setPassword(null)
  }

  useEffect(() => {
    const ac = new AbortController()
    fetchOutageAccount(organizationID, ac.signal)
      .then((a) => {
        setAccount(a)
        setForm(formOf(a))
      })
      .catch((e: unknown) => {
        if (ac.signal.aborted) return
        setLoadError(errorText(e))
      })
    return () => ac.abort()
  }, [organizationID])

  if (loadError) {
    return (
      <section className="station-section pvplan">
        <h2>Відключення e-svitlo</h2>
        <div className="station-error" role="alert">
          Не вдалося завантажити кабінет: {loadError}
        </div>
      </section>
    )
  }
  if (!account || !form) {
    return (
      <section className="station-section pvplan">
        <h2>Відключення e-svitlo</h2>
        <div className="station-loading">Завантаження…</div>
      </section>
    )
  }

  const eic = form.eic.trim().toUpperCase()
  const eicBad = eic !== '' && eic.length !== EIC_LENGTH
  const dirty = password !== null || !sameForm(form, formOf(account))
  const read = account.read
  const succeeded = formatMoment(read.succeeded_at)
  const attempted = formatMoment(read.attempted_at)
  const input = (): OutageAccountInput => ({ host: form.host, login: form.login.trim(), eic })

  const update = (patch: Partial<OutageAccountInput>) => {
    setForm((f) => (f ? { ...f, ...patch } : f))
    setNotice('')
    setError('')
  }

  const save = async (next: OutageAccountInput, nextPassword: string | null, done: string) => {
    setBusy(true)
    setError('')
    setNotice('')
    try {
      apply(await saveOutageAccount(organizationID, next, nextPassword))
      setNotice(done)
    } catch (e) {
      setError(errorText(e))
    } finally {
      setBusy(false)
    }
  }

  const runTest = async () => {
    setTest({ status: 'running' })
    try {
      setTest({ status: 'done', result: await testOutageAccount(organizationID, input(), password) })
    } catch (e) {
      setTest({ status: 'failed', message: errorText(e) })
    }
  }

  const canTest =
    form.login.trim() !== '' && eic !== '' && !eicBad && (account.password_configured || password !== null)

  return (
    <section className="station-section pvplan">
      <h2>Відключення e-svitlo</h2>
      <p className="pvplan-lead">
        Кабінет споживача цього елеватора на e-svitlo. Звідси щогодини читаються заплановані відключення світла, і вони
        показуються смугами на денному графіку Дашборду та в «Керуванні».
      </p>

      {!account.saved ? (
        <p className="pvplan-status">Кабінет не вказано — відключення для цієї станції не читаються.</p>
      ) : !account.password_configured ? (
        <p className="pvplan-status err">Пароль не збережено — читання не йде.</p>
      ) : read.error ? (
        <p className="pvplan-status err" role="alert">
          Остання спроба {attempted} не вдалася: {read.error}
          {succeeded ? ` Останнє вдале читання — ${succeeded}.` : ''}
        </p>
      ) : succeeded ? (
        <p className="pvplan-status">Перевірено {succeeded}. Наступне читання — протягом години.</p>
      ) : (
        <p className="pvplan-status">Ще не читалося — перше читання за кілька хвилин.</p>
      )}

      {account.saved ? (
        <>
          <p className="pvplan-hint">Найближчі 48 годин:</p>
          <OutageList intervals={account.upcoming} />
        </>
      ) : null}

      {canEdit ? (
        <>
          <div className="pvplan-grid">
            <label className="pvplan-field">
              Обленерго
              <select value={form.host} disabled={busy} onChange={(e) => update({ host: e.target.value })}>
                {account.operators.map((o) => (
                  <option key={o.host} value={o.host}>
                    {o.name}
                  </option>
                ))}
              </select>
            </label>
            <label className="pvplan-field">
              Логін кабінету
              <input
                value={form.login}
                placeholder="email або телефон"
                autoComplete="off"
                disabled={busy}
                onChange={(e) => update({ login: e.target.value })}
              />
            </label>
            <label className="pvplan-field">
              Пароль
              <input
                type="password"
                value={password ?? ''}
                placeholder={account.password_configured ? 'збережено — лишіть порожнім' : 'пароль кабінету'}
                autoComplete="new-password"
                disabled={busy}
                onChange={(e) => {
                  setPassword(e.target.value === '' ? null : e.target.value)
                  setNotice('')
                  setError('')
                }}
              />
            </label>
            <label className="pvplan-field">
              EIC точки обліку
              <input
                value={form.eic}
                maxLength={EIC_LENGTH}
                placeholder="62Z…"
                disabled={busy}
                onChange={(e) => update({ eic: e.target.value })}
              />
            </label>
          </div>
          <p className="pvplan-hint">
            EIC точки комерційного обліку з розділу «Площадки комерційного обліку» в кабінеті. Без нього відключення не
            читаються — у кожної точки свій графік.
          </p>

          <div className="pvplan-actions">
            <button
              type="button"
              className="pvplan-btn"
              disabled={busy || !canTest || test?.status === 'running'}
              onClick={() => void runTest()}
            >
              {test?.status === 'running' ? 'Перевіряємо…' : 'Перевірити'}
            </button>
            <button
              type="button"
              className="pvplan-btn primary"
              disabled={busy || !dirty || eic === '' || eicBad || form.login.trim() === ''}
              onClick={() => void save(input(), password, 'Збережено. Відключення прочитаються за кілька хвилин.')}
            >
              {busy ? 'Збереження…' : 'Зберегти'}
            </button>
            {dirty ? (
              <button
                type="button"
                className="pvplan-btn"
                disabled={busy}
                onClick={() => {
                  apply(account)
                  setNotice('')
                  setError('')
                }}
              >
                Скасувати зміни
              </button>
            ) : null}
            {account.saved ? (
              <button
                type="button"
                className="pvplan-btn"
                disabled={busy}
                onClick={() => void save({ host: '', login: '', eic: '' }, null, 'Кабінет прибрано: відключення більше не читаються.')}
              >
                Прибрати кабінет
              </button>
            ) : null}
          </div>
          {eicBad ? <p className="pvplan-notice err">EIC має містити {EIC_LENGTH} символів.</p> : null}

          {test?.status === 'failed' ? (
            <p className="pvplan-notice err" role="alert">
              {test.message}
            </p>
          ) : null}
          {test?.status === 'done' ? (
            test.result.ok ? (
              <div className="pvplan-notice">
                Вхід вдався{test.result.accounts?.length ? ` (рахунок ${test.result.accounts.join(', ')})` : ''}. Кабінет
                показує:
                <OutageList intervals={test.result.outages} />
              </div>
            ) : (
              <p className="pvplan-notice err" role="alert">
                {test.result.error ?? 'Перевірка не вдалася'}
              </p>
            )
          ) : null}
        </>
      ) : (
        <p className="pvplan-hint">Вказати або змінити кабінет може адміністратор елеватора.</p>
      )}

      {notice ? <p className="pvplan-notice">{notice}</p> : null}
      {error ? (
        <p className="pvplan-notice err" role="alert">
          {error}
        </p>
      ) : null}
    </section>
  )
}
