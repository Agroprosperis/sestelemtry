import { Sparkle } from '@phosphor-icons/react'
import { useChat } from './chatContext'
import './chat.css'

// ExplainButton asks the assistant about the card it sits on. It sits
// at the end of a flex header row and renders nothing when the
// assistant is off.
export function ExplainButton({ question }: { question: string }) {
  const chat = useChat()
  if (!chat) return null
  return (
    <button
      type="button"
      className="chat-explain-btn"
      title="Пояснити з ШІ-асистентом"
      aria-label="Пояснити з ШІ-асистентом"
      onClick={() => chat.ask(question)}
    >
      <Sparkle size={15} weight="fill" aria-hidden="true" />
    </button>
  )
}
