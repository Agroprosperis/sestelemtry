import { useCallback, useEffect, useRef, useState } from 'react'
import { readPageContext, streamChat } from './chatClient'
import { historyFor, loadConversation, saveConversation, type ChatEntry } from './conversation'

export type Conversation = {
  entries: ChatEntry[]
  busy: boolean
  send: (question: string) => void
  stop: () => void
  clear: () => void
}

let lastEntryID = Date.now()
const nextEntryID = () => ++lastEntryID

// useConversation is the assistant conversation about one object. The
// owner remounts it for another object; unmounting hangs up a running
// answer.
export function useConversation(organizationID: string): Conversation {
  const [entries, setEntries] = useState<ChatEntry[]>(() => loadConversation(organizationID))
  const [busy, setBusy] = useState(false)
  const entriesRef = useRef(entries)
  const abortRef = useRef<AbortController | null>(null)

  useEffect(() => {
    entriesRef.current = entries
    saveConversation(organizationID, entries)
  }, [organizationID, entries])

  useEffect(() => {
    const aborts = abortRef
    return () => aborts.current?.abort()
  }, [])

  const send = useCallback(
    (question: string) => {
      const text = question.trim()
      if (!text || abortRef.current) return
      const history = historyFor(entriesRef.current)
      const answerID = nextEntryID()
      setEntries((es) => [
        ...es,
        { id: nextEntryID(), role: 'user', text },
        { id: answerID, role: 'assistant', text: '', pending: true },
      ])
      const patch = (fn: (e: ChatEntry) => ChatEntry) =>
        setEntries((es) => es.map((e) => (e.id === answerID ? fn(e) : e)))
      const ac = new AbortController()
      abortRef.current = ac
      setBusy(true)
      streamChat(
        organizationID,
        { messages: [...history, { role: 'user', text }], context: readPageContext(organizationID) },
        (ev) => {
          if (ev.type === 'tool' || ev.type === 'status') patch((e) => ({ ...e, status: ev.label }))
          else if (ev.type === 'delta') patch((e) => ({ ...e, text: e.text + ev.text, status: undefined }))
          else if (ev.type === 'sources') patch((e) => ({ ...e, sources: ev.sources }))
          else if (ev.type === 'suggestions') patch((e) => ({ ...e, suggestions: ev.suggestions }))
          else if (ev.type === 'error') patch((e) => ({ ...e, error: ev.error }))
        },
        ac.signal,
      )
        .catch((err: unknown) => {
          if (ac.signal.aborted) patch((e) => ({ ...e, stopped: true }))
          else patch((e) => ({ ...e, error: err instanceof Error && err.message ? err.message : 'ШІ-асистент недоступний' }))
        })
        .finally(() => {
          patch((e) => ({ ...e, pending: undefined, status: undefined }))
          abortRef.current = null
          setBusy(false)
        })
    },
    [organizationID],
  )

  const stop = useCallback(() => abortRef.current?.abort(), [])

  const clear = useCallback(() => {
    abortRef.current?.abort()
    setEntries([])
  }, [])

  return { entries, busy, send, stop, clear }
}
