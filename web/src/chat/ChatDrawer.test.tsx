import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AuthContext } from '../auth/authContext'
import { accessFor, type AuthMe } from '../auth/permissions'
import { ModeTopBar } from '../shell/ModeTopBar'
import { ChatProvider } from './ChatProvider'
import { ExplainButton } from './ExplainButton'

const me: AuthMe = {
  user: { id: 3, email: 'ceo@example.com', name: 'Ірина' },
  global_admin: false,
  must_change_password: false,
  grants: [{ role: 'ceo', organization_id: null }],
  organizations: [
    { id: 'ze', name: 'ZE', permissions: ['analytics.day', 'analytics.full', 'economics.read', 'control'] },
    { id: 'pe', name: 'PE', permissions: ['analytics.day', 'analytics.full', 'economics.read', 'control'] },
  ],
  ai_chat: true,
}

function app(user: AuthMe, organizationID: string, mode: 'analytics' | 'economics' = 'economics'): ReactNode {
  return (
    <AuthContext.Provider value={{ me: user, access: accessFor(user), logout: vi.fn(async () => {}) }}>
      <ChatProvider>
        <ModeTopBar
          mode={mode}
          organizationID={organizationID}
          options={['ze', 'pe']}
          onOrganizationChange={() => {}}
          status={null}
        />
        <ExplainButton question="Поясни картку «Батарея за день»." />
      </ChatProvider>
    </AuthContext.Provider>
  )
}

function ndjson(...events: object[]): Response {
  return new Response(events.map((e) => JSON.stringify(e)).join('\n') + '\n', {
    status: 200,
    headers: { 'Content-Type': 'application/x-ndjson' },
  })
}

describe('AI assistant panel', () => {
  beforeEach(() => {
    window.sessionStorage.clear()
    window.history.replaceState({}, '', '/?view=economics&range=month&anchor=2026-05-01&organization_id=ze')
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('stays hidden when the API has no assistant', () => {
    render(app({ ...me, ai_chat: false }, 'ze'))
    expect(screen.queryByRole('button', { name: /ШІ-асистент/ })).not.toBeInTheDocument()
  })

  it('asks about a card from its «Пояснити» button', async () => {
    const fetchSpy = vi.fn<typeof fetch>(async () => ndjson({ type: 'delta', text: 'УЗЕ розрядилась у вечірній пік.' }, { type: 'done' }))
    vi.stubGlobal('fetch', fetchSpy)
    render(app(me, 'ze', 'analytics'))

    fireEvent.click(screen.getByRole('button', { name: 'Пояснити з ШІ-асистентом' }))

    expect(await screen.findByText('УЗЕ розрядилась у вечірній пік.')).toBeInTheDocument()
    expect(screen.getByRole('complementary', { name: 'ШІ-асистент' })).toBeInTheDocument()
    const body = JSON.parse(String(fetchSpy.mock.calls[0][1]?.body))
    expect(body.messages).toEqual([{ role: 'user', text: 'Поясни картку «Батарея за день».' }])
  })

  it('streams an answer with its sources and keeps the page context', async () => {
    const fetchSpy = vi.fn<typeof fetch>(async () =>
      ndjson(
        { type: 'tool', name: 'economics_month', label: 'Читаю підсумок місяця за травень 2026' },
        { type: 'delta', text: 'Головний резерв — таймінг УЗЕ:\n' },
        { type: 'delta', text: '- 41 тис. ₴ за травень' },
        { type: 'sources', sources: [{ label: 'Підсумок за травень 2026 (31 з 31 днів)' }] },
        { type: 'done', usage: { input_tokens: 900, output_tokens: 60 } },
      ),
    )
    vi.stubGlobal('fetch', fetchSpy)
    render(app(me, 'ze'))

    fireEvent.click(screen.getByRole('button', { name: 'ШІ-асистент' }))
    fireEvent.click(screen.getByRole('button', { name: 'Де головний резерв за цей місяць?' }))

    expect(await screen.findByText('41 тис. ₴ за травень')).toBeInTheDocument()
    expect(screen.getByText('Головний резерв — таймінг УЗЕ:')).toBeInTheDocument()
    expect(screen.getByText('Підсумок за травень 2026 (31 з 31 днів)')).toBeInTheDocument()

    const [url, init] = fetchSpy.mock.calls[0]
    expect(String(url)).toContain('/api/v1/chat?organization_id=ze')
    expect(init?.method).toBe('POST')
    expect(new Headers(init?.headers).get('X-Requested-With')).toBe('fetch')
    const body = JSON.parse(String(init?.body))
    expect(body.messages).toEqual([{ role: 'user', text: 'Де головний резерв за цей місяць?' }])
    expect(body.context).toMatchObject({ view: 'economics', range: 'month', anchor: '2026-05-01', operation_start: '2024-08-01' })
  })

  it('offers the next questions under the latest answer and asks one on click', async () => {
    const fetchSpy = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(
        ndjson(
          { type: 'delta', text: 'СЕС виробила 1,2 МВт·год.' },
          { type: 'status', label: 'Добираю наступні питання…' },
          { type: 'suggestions', suggestions: ['Як було вчора?', 'Куди пішов експорт?'] },
          { type: 'done' },
        ),
      )
      .mockResolvedValueOnce(ndjson({ type: 'delta', text: 'Учора — 1,0 МВт·год.' }, { type: 'done' }))
    vi.stubGlobal('fetch', fetchSpy)
    render(app(me, 'ze', 'analytics'))
    fireEvent.click(screen.getByRole('button', { name: 'ШІ-асистент' }))
    fireEvent.click(screen.getByRole('button', { name: 'Як зараз працює станція?' }))

    fireEvent.click(await screen.findByRole('button', { name: 'Як було вчора?' }))

    expect(await screen.findByText('Учора — 1,0 МВт·год.')).toBeInTheDocument()
    const body = JSON.parse(String(fetchSpy.mock.calls[1][1]?.body))
    expect(body.messages).toEqual([
      { role: 'user', text: 'Як зараз працює станція?' },
      { role: 'assistant', text: 'СЕС виробила 1,2 МВт·год.' },
      { role: 'user', text: 'Як було вчора?' },
    ])
    // The answered turn's buttons are gone; the new answer has none.
    expect(screen.queryByRole('button', { name: 'Куди пішов експорт?' })).not.toBeInTheDocument()
  })

  it('shows the API refusal and keeps one conversation per object', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async () => new Response('Денний ліміт ШІ-асистента (100 запитів) вичерпано.', { status: 429 })),
    )
    const { rerender } = render(app(me, 'ze'))
    fireEvent.click(screen.getByRole('button', { name: 'ШІ-асистент' }))
    fireEvent.change(screen.getByRole('textbox', { name: 'Питання до ШІ-асистента' }), {
      target: { value: 'Скільки виробила СЕС?' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Надіслати' }))
    expect(await screen.findByText(/Денний ліміт ШІ-асистента/)).toBeInTheDocument()

    rerender(app(me, 'pe'))
    await waitFor(() => expect(screen.queryByText('Скільки виробила СЕС?')).not.toBeInTheDocument())
    expect(screen.getByRole('button', { name: 'Де головний резерв за цей місяць?' })).toBeInTheDocument()

    rerender(app(me, 'ze'))
    expect(await screen.findByText('Скільки виробила СЕС?')).toBeInTheDocument()
  })
})
