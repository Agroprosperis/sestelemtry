import type { ChatMessage, ChatRole, ChatSource } from './chatClient'

// ChatEntry is one bubble of the conversation.
export type ChatEntry = {
  id: number
  role: ChatRole
  text: string
  // status is the running lookup's progress line while an answer streams.
  status?: string
  sources?: ChatSource[]
  error?: string
  // stopped: the user hung up before the answer finished.
  stopped?: boolean
  pending?: boolean
}

// The API keeps the last 12 messages and cuts earlier ones to 6000
// characters; sending more would only cost bandwidth.
const HISTORY_MESSAGES = 11
const HISTORY_CHARS = 6000
const STORED_ENTRIES = 40
const STORAGE_PREFIX = 'ses.chat.'

// historyFor is what the assistant gets as the conversation before the
// next question: answered turns only, no failed or empty bubbles.
export function historyFor(entries: ChatEntry[]): ChatMessage[] {
  return entries
    .filter((e) => !e.pending && e.text.trim() !== '' && !(e.role === 'assistant' && e.error && !e.text))
    .slice(-HISTORY_MESSAGES)
    .map((e) => ({ role: e.role, text: e.text.slice(0, HISTORY_CHARS) }))
}

// The conversation lives in the tab (sessionStorage), one per object:
// nothing about it is kept on the server.
export function loadConversation(organizationID: string): ChatEntry[] {
  try {
    const raw = window.sessionStorage.getItem(STORAGE_PREFIX + organizationID)
    if (!raw) return []
    const parsed = JSON.parse(raw) as unknown
    if (!Array.isArray(parsed)) return []
    return parsed.filter(
      (e): e is ChatEntry =>
        typeof e === 'object' && e !== null && (e.role === 'user' || e.role === 'assistant') && typeof e.text === 'string',
    )
  } catch {
    return []
  }
}

export function saveConversation(organizationID: string, entries: ChatEntry[]) {
  try {
    const key = STORAGE_PREFIX + organizationID
    // An answer still streaming is saved as interrupted: a reload
    // drops the request it belonged to.
    const kept = entries
      .map((e) => (e.pending ? { ...e, pending: undefined, status: undefined, stopped: true } : e))
      .filter((e) => e.text !== '' || e.error)
      .slice(-STORED_ENTRIES)
    if (kept.length === 0) window.sessionStorage.removeItem(key)
    else window.sessionStorage.setItem(key, JSON.stringify(kept))
  } catch {
    // Storage full or disabled: the conversation just won't survive a reload.
  }
}

export type AnswerBlock = { kind: 'p'; lines: string[] } | { kind: 'ul'; items: string[] }

const LIST_ITEM = /^\s*(?:[-•*]|\d+[.)])\s+/

// answerBlocks splits an answer into paragraphs and bullet lists. The
// prompt asks for plain text with «- » lists; stray markdown emphasis is
// dropped rather than shown as asterisks.
export function answerBlocks(text: string): AnswerBlock[] {
  const blocks: AnswerBlock[] = []
  let current: AnswerBlock | null = null
  for (const raw of text.replace(/\*\*(.+?)\*\*/g, '$1').split('\n')) {
    const line = raw.trimEnd()
    if (line.trim() === '') {
      current = null
      continue
    }
    if (LIST_ITEM.test(line)) {
      const item = line.replace(LIST_ITEM, '')
      if (current?.kind === 'ul') current.items.push(item)
      else blocks.push((current = { kind: 'ul', items: [item] }))
      continue
    }
    const plain = line.replace(/^#+\s+/, '')
    if (current?.kind === 'p') current.lines.push(plain)
    else blocks.push((current = { kind: 'p', lines: [plain] }))
  }
  return blocks
}
