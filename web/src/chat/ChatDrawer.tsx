import { CircleNotch, PaperPlaneRight, Sparkle, Trash, X } from '@phosphor-icons/react'
import { useEffect, useRef, useState, type KeyboardEvent } from 'react'
import { formatOrganizationLabel } from '../dashboard/config'
import type { ChatMode, ChatTarget } from './chatContext'
import { answerBlocks, type ChatEntry } from './conversation'
import type { Conversation } from './useConversation'
import './chat.css'

const STARTERS: Record<ChatMode, string[]> = {
  analytics: [
    'Як зараз працює станція?',
    'Скільки виробила СЕС сьогодні і куди пішла енергія?',
    'Чому сьогодні був імпорт з мережі?',
  ],
  economics: [
    'Який ефект проєкту за цей місяць?',
    'Де головний резерв за цей місяць?',
    'Який день місяця був найслабшим і чому?',
  ],
  control: [
    'Що мала б робити УЗЕ сьогодні за оптимальним розкладом?',
    'Коли сьогодні найдорожчі години РДН?',
    'Який зараз SOC і потужність УЗЕ?',
  ],
  none: ['Як зараз працює станція?', 'Підсумуй учорашній день.'],
}

type Props = {
  target: ChatTarget
  conversation: Conversation
  onClose: () => void
}

// ChatDrawer is the assistant's side panel: the conversation about the
// selected object, starter questions for the current mode, the input.
export function ChatDrawer({ target, conversation, onClose }: Props) {
  const { entries, busy, send, stop, clear } = conversation
  const [draft, setDraft] = useState('')
  const logRef = useRef<HTMLDivElement | null>(null)
  const inputRef = useRef<HTMLTextAreaElement | null>(null)

  useEffect(() => {
    inputRef.current?.focus()
  }, [])

  useEffect(() => {
    const log = logRef.current
    if (log) log.scrollTop = log.scrollHeight
  }, [entries])

  const submit = (text: string) => {
    if (busy || !text.trim()) return
    send(text)
    setDraft('')
  }

  const onInputKey = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault()
      submit(draft)
    }
  }

  const org = target.organizationID
  return (
    <aside
      className="chat-drawer"
      aria-label="ШІ-асистент"
      onKeyDown={(e) => {
        if (e.key === 'Escape') onClose()
      }}
    >
      <header className="chat-head">
        <div className="chat-head-title">
          <h2>
            <Sparkle size={16} weight="fill" aria-hidden="true" /> ШІ-асистент
          </h2>
          <span>
            {formatOrganizationLabel(org)} · {org.toUpperCase()}
          </span>
        </div>
        <button
          type="button"
          className="chat-icon-btn"
          onClick={clear}
          disabled={entries.length === 0}
          title="Очистити розмову"
          aria-label="Очистити розмову"
        >
          <Trash size={16} aria-hidden="true" />
        </button>
        <button type="button" className="chat-icon-btn" onClick={onClose} title="Закрити" aria-label="Закрити">
          <X size={16} aria-hidden="true" />
        </button>
      </header>

      <div className="chat-log" ref={logRef}>
        {entries.length === 0 ? (
          <div className="chat-starters">
            <p>Запитайте про цей об'єкт: роботу станції, енергобаланс, ціни РДН чи економіку.</p>
            {STARTERS[target.mode].map((q) => (
              <button key={q} type="button" className="chat-starter" onClick={() => submit(q)}>
                {q}
              </button>
            ))}
          </div>
        ) : (
          entries.map((entry, i) => (
            <ChatBubble
              key={entry.id}
              entry={entry}
              // Only the latest answer offers its next questions, and
              // not while another one is on its way.
              onAsk={i === entries.length - 1 && !busy ? submit : undefined}
            />
          ))
        )}
      </div>

      <form
        className="chat-input"
        onSubmit={(e) => {
          e.preventDefault()
          submit(draft)
        }}
      >
        <textarea
          ref={inputRef}
          value={draft}
          rows={2}
          maxLength={4000}
          placeholder="Ваше питання…"
          aria-label="Питання до ШІ-асистента"
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={onInputKey}
        />
        {busy ? (
          <button type="button" className="chat-send stop" onClick={stop}>
            Зупинити
          </button>
        ) : (
          <button type="submit" className="chat-send" disabled={!draft.trim()} aria-label="Надіслати">
            <PaperPlaneRight size={16} weight="fill" aria-hidden="true" />
          </button>
        )}
      </form>
      <p className="chat-note">Відповідає модель OpenAI за даними консолі. Звіряйте цифри з картками.</p>
    </aside>
  )
}

function ChatBubble({ entry, onAsk }: { entry: ChatEntry; onAsk?: (question: string) => void }) {
  if (entry.role === 'user') {
    return <div className="chat-msg user">{entry.text}</div>
  }
  return (
    <>
      <AnswerBubble entry={entry} />
      {onAsk && entry.suggestions && entry.suggestions.length > 0 && (
        <div className="chat-suggestions" aria-label="Наступні питання">
          {entry.suggestions.map((q) => (
            <button key={q} type="button" className="chat-suggestion" onClick={() => onAsk(q)}>
              {q}
            </button>
          ))}
        </div>
      )}
    </>
  )
}

function AnswerBubble({ entry }: { entry: ChatEntry }) {
  return (
    <div className="chat-msg assistant" aria-busy={entry.pending === true}>
      {entry.text && <AnswerText text={entry.text} />}
      {entry.pending && (
        <div className="chat-status" role="status">
          <CircleNotch size={14} weight="bold" className="chat-spin" aria-hidden="true" />
          {entry.status ?? (entry.text ? 'Пишу відповідь…' : 'Думаю…')}
        </div>
      )}
      {entry.stopped && !entry.pending && <div className="chat-status">Відповідь зупинено.</div>}
      {entry.error && <div className="chat-error">{entry.error}</div>}
      {entry.sources && entry.sources.length > 0 && (
        <ul className="chat-sources" aria-label="Джерела">
          {entry.sources.map((s) => (
            <li key={s.label}>{s.label}</li>
          ))}
        </ul>
      )}
    </div>
  )
}

function AnswerText({ text }: { text: string }) {
  return (
    <>
      {answerBlocks(text).map((block, i) =>
        block.kind === 'ul' ? (
          <ul key={i}>
            {block.items.map((item, j) => (
              <li key={j}>{item}</li>
            ))}
          </ul>
        ) : (
          <p key={i}>
            {block.lines.map((line, j) => (
              <span key={j}>
                {j > 0 && <br />}
                {line}
              </span>
            ))}
          </p>
        ),
      )}
    </>
  )
}
