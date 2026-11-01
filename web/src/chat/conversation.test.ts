import { beforeEach, describe, expect, it } from 'vitest'
import { answerBlocks, historyFor, loadConversation, saveConversation, type ChatEntry } from './conversation'

describe('answerBlocks', () => {
  it('splits paragraphs and groups list lines', () => {
    expect(answerBlocks('СЕС виробила 1,2 МВт·год.\nЕкспорт — 300 кВт·год.\n\n- імпорт 200\n• розряд 150\n\nПідсумок.')).toEqual([
      { kind: 'p', lines: ['СЕС виробила 1,2 МВт·год.', 'Експорт — 300 кВт·год.'] },
      { kind: 'ul', items: ['імпорт 200', 'розряд 150'] },
      { kind: 'p', lines: ['Підсумок.'] },
    ])
  })

  it('drops markdown emphasis and headings instead of showing the marks', () => {
    expect(answerBlocks('## Підсумок\n**Ефект** — 12 тис. ₴')).toEqual([
      { kind: 'p', lines: ['Підсумок', 'Ефект — 12 тис. ₴'] },
    ])
  })
})

describe('historyFor', () => {
  it('sends answered turns only, the last eleven, cut to 6000 characters', () => {
    const entries: ChatEntry[] = [
      { id: 1, role: 'user', text: 'перше' },
      { id: 2, role: 'assistant', text: '', error: 'Сервіс OpenAI тимчасово недоступний.' },
      { id: 3, role: 'user', text: 'друге' },
      { id: 4, role: 'assistant', text: 'я'.repeat(7000) },
      { id: 5, role: 'assistant', text: '', pending: true },
    ]
    const history = historyFor(entries)
    expect(history.map((m) => m.role)).toEqual(['user', 'user', 'assistant'])
    expect(history[2].text).toHaveLength(6000)

    const many: ChatEntry[] = Array.from({ length: 20 }, (_, i) => ({ id: i, role: 'user', text: `q${i}` }))
    expect(historyFor(many).map((m) => m.text)).toEqual(Array.from({ length: 11 }, (_, i) => `q${i + 9}`))
  })
})

describe('conversation storage', () => {
  beforeEach(() => window.sessionStorage.clear())

  it('keeps one conversation per object and marks an interrupted answer', () => {
    saveConversation('ze', [
      { id: 1, role: 'user', text: 'Як станція?' },
      { id: 2, role: 'assistant', text: 'Працює', pending: true, status: 'Читаю поточні показники' },
      { id: 3, role: 'assistant', text: '', pending: true },
    ])
    expect(loadConversation('pe')).toEqual([])
    expect(loadConversation('ze')).toEqual([
      { id: 1, role: 'user', text: 'Як станція?' },
      { id: 2, role: 'assistant', text: 'Працює', stopped: true },
    ])
    saveConversation('ze', [])
    expect(window.sessionStorage.getItem('ses.chat.ze')).toBeNull()
  })

  it('ignores a corrupted entry', () => {
    window.sessionStorage.setItem('ses.chat.ze', '[{"role":"system","text":"x"},{"id":1,"role":"user","text":"ok"}]')
    expect(loadConversation('ze')).toEqual([{ id: 1, role: 'user', text: 'ok' }])
    window.sessionStorage.setItem('ses.chat.ze', 'not json')
    expect(loadConversation('ze')).toEqual([])
  })
})
