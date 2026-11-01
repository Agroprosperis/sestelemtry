import { createContext, useContext, useEffect } from 'react'

// ChatMode is the page the assistant is opened from (ModeTopBar's mode).
export type ChatMode = 'analytics' | 'economics' | 'control' | 'none'

export type ChatTarget = { mode: ChatMode; organizationID: string }

export type ChatControls = {
  open: boolean
  toggle: () => void
  setTarget: (target: ChatTarget) => void
  // ask opens the panel and sends question, once the answer in flight
  // (if any) has finished.
  ask: (question: string) => void
}

// ChatContext is null when the API has no assistant configured and
// outside the signed-in app (component tests).
export const ChatContext = createContext<ChatControls | null>(null)

export function useChat(): ChatControls | null {
  return useContext(ChatContext)
}

// useChatTarget points the assistant at the page's object and mode.
// Every page renders its own top bar, which calls this.
export function useChatTarget(mode: ChatMode, organizationID: string) {
  const setTarget = useContext(ChatContext)?.setTarget
  useEffect(() => {
    setTarget?.({ mode, organizationID })
  }, [setTarget, mode, organizationID])
}
