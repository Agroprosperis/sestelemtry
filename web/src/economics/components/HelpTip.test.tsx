import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { HelpTip } from './HelpTip'

afterEach(cleanup)

function renderTip() {
  return render(
    <div>
      <HelpTip label="Ціна пального">Ціна літра без ПДВ.</HelpTip>
      <span>поза підказкою</span>
    </div>,
  )
}

describe('HelpTip', () => {
  it('opens on click and closes on a click outside', () => {
    renderTip()
    const trigger = screen.getByRole('button', { name: 'Пояснення: Ціна пального' })
    expect(screen.queryByRole('tooltip')).toBeNull()
    fireEvent.click(trigger)
    expect(screen.getByRole('tooltip')).toHaveTextContent('Ціна літра без ПДВ.')
    fireEvent.pointerDown(screen.getByText('поза підказкою'))
    expect(screen.queryByRole('tooltip')).toBeNull()
  })

  it('opens from the keyboard and closes on Esc without letting Esc through', () => {
    const onWindowKey = vi.fn()
    window.addEventListener('keydown', onWindowKey)
    renderTip()
    const trigger = screen.getByRole('button', { name: 'Пояснення: Ціна пального' })
    fireEvent.keyDown(trigger, { key: 'Enter' })
    expect(screen.getByRole('tooltip')).toBeInTheDocument()
    fireEvent.keyDown(trigger, { key: 'Escape' })
    expect(screen.queryByRole('tooltip')).toBeNull()
    expect(onWindowKey).not.toHaveBeenCalledWith(expect.objectContaining({ key: 'Escape' }))
    window.removeEventListener('keydown', onWindowKey)
  })

  it('opens on mouse hover only, not on a touch', () => {
    renderTip()
    const wrap = screen.getByRole('button', { name: 'Пояснення: Ціна пального' }).parentElement as HTMLElement
    fireEvent.pointerEnter(wrap, { pointerType: 'touch' })
    expect(screen.queryByRole('tooltip')).toBeNull()
    fireEvent.pointerEnter(wrap, { pointerType: 'mouse' })
    expect(screen.getByRole('tooltip')).toBeInTheDocument()
    fireEvent.pointerLeave(wrap, { pointerType: 'mouse' })
    expect(screen.queryByRole('tooltip')).toBeNull()
  })

  it('stays usable in a read-only (disabled) fieldset', () => {
    render(
      <fieldset disabled>
        <HelpTip label="Витрата">Літри на кВт·год.</HelpTip>
      </fieldset>,
    )
    fireEvent.click(screen.getByRole('button', { name: 'Пояснення: Витрата' }))
    expect(screen.getByRole('tooltip')).toHaveTextContent('Літри на кВт·год.')
  })
})
