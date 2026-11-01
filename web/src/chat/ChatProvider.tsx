import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { useAuth } from '../auth/authContext'
import { ChatDrawer } from './ChatDrawer'
import { ChatContext, type ChatControls, type ChatTarget } from './chatContext'
import { useConversation } from './useConversation'

type Pending = { id: number; question: string }

// ChatProvider hosts the AI assistant above the pages: the pages come
// and go with the mode switch, the panel and a running answer stay.
// Nothing renders when /auth/me says the API has no assistant.
export function ChatProvider({ children }: { children: ReactNode }) {
  const enabled = useAuth()?.me.ai_chat === true
  const [open, setOpen] = useState(false)
  const [target, setTarget] = useState<ChatTarget | null>(null)
  const [pending, setPending] = useState<Pending | null>(null)
  const toggle = useCallback(() => setOpen((v) => !v), [])
  const close = useCallback(() => setOpen(false), [])
  const ask = useCallback((question: string) => {
    setOpen(true)
    setPending({ id: Date.now(), question })
  }, [])
  const asked = useCallback((id: number) => setPending((p) => (p?.id === id ? null : p)), [])
  const controls = useMemo<ChatControls | null>(
    () => (enabled ? { open, toggle, setTarget, ask } : null),
    [enabled, open, toggle, ask],
  )
  return (
    <ChatContext.Provider value={controls}>
      {children}
      {enabled && target?.organizationID ? (
        <ChatSession
          key={target.organizationID}
          target={target}
          open={open}
          pending={pending}
          onAsked={asked}
          onClose={close}
        />
      ) : null}
    </ChatContext.Provider>
  )
}

type SessionProps = {
  target: ChatTarget
  open: boolean
  pending: Pending | null
  onAsked: (id: number) => void
  onClose: () => void
}

// ChatSession keeps one object's conversation alive while the panel is
// closed; another object remounts it (key).
function ChatSession({ target, open, pending, onAsked, onClose }: SessionProps) {
  const conversation = useConversation(target.organizationID)
  const { busy, send } = conversation
  useEffect(() => {
    if (!pending || busy) return
    onAsked(pending.id)
    send(pending.question)
  }, [pending, busy, send, onAsked])
  if (!open) return null
  return <ChatDrawer target={target} conversation={conversation} onClose={onClose} />
}
