import { useState } from 'react'
import { copyText } from './password'
import type { Issued } from './types'

export function IssuedCredentials({ issued, onClose }: { issued: Issued; onClose: () => void }) {
  const [copied, setCopied] = useState<boolean | null>(null)
  const address = window.location.origin
  const text = `Адреса: ${address}\nЛогін: ${issued.email}\nТимчасовий пароль: ${issued.password}`
  return (
    <section className="alerts-card" role="status">
      <span className="alerts-card-accent" />
      <div className="alerts-card-head">
        <h2 className="alerts-section-title">{issued.created ? 'Користувача створено' : 'Пароль змінено'}</h2>
      </div>
      <p className="alerts-section-sub">
        Передайте ці дані користувачу. При першому вході дашборд попросить задати власний
        пароль; цей більше ніде не показується.
      </p>
      <dl className="users-credentials">
        <dt>Адреса</dt>
        <dd>
          <code>{address}</code>
        </dd>
        <dt>Логін</dt>
        <dd>
          <code>{issued.email}</code>
        </dd>
        <dt>Пароль</dt>
        <dd>
          <code>{issued.password}</code>
        </dd>
      </dl>
      <div className="alerts-actions">
        {copied === false ? (
          <span className="alerts-dirty-note">Не вдалося скопіювати — виділіть дані й скопіюйте вручну.</span>
        ) : null}
        <button type="button" className="alerts-secondary" onClick={() => void copyText(text).then(setCopied)}>
          {copied ? 'Скопійовано' : 'Копіювати'}
        </button>
        <button type="button" className="alerts-save" onClick={onClose}>
          Готово
        </button>
      </div>
    </section>
  )
}
