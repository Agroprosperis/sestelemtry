import { describe, expect, it } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { FormulaTip } from '../components/FormulaTip'
import { residual, sumLines } from '../formula'

const fmt = (v: number) => `${v} грн`

describe('FormulaTip', () => {
  it('opens the formula on click and closes it on Esc', () => {
    render(
      <FormulaTip
        label="Економічний ефект"
        formula={{
          text: 'Наскільки дешевше обійшлася енергія.',
          rule: 'Базова вартість − Фактична вартість',
          lines: sumLines(
            [
              { label: 'Базова вартість', amount: 1200 },
              { label: 'Фактична вартість', amount: -800 },
            ],
            { label: 'Економічний ефект', amount: 400 },
            fmt,
          ),
        }}
      />,
    )
    const button = screen.getByRole('button', { name: 'Як рахується: Економічний ефект' })
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()

    fireEvent.click(button)
    const tip = screen.getByRole('tooltip')
    expect(button).toHaveAttribute('aria-expanded', 'true')
    expect(tip).toHaveTextContent('Базова вартість − Фактична вартість')
    expect(tip).toHaveTextContent('1200 грн')
    expect(tip).toHaveTextContent('800 грн')
    expect(tip).toHaveTextContent('400 грн')

    fireEvent.keyDown(document, { key: 'Escape' })
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
  })

  it('closes on a click outside', () => {
    render(
      <div>
        <FormulaTip label="Ефект" formula={{ text: 'Пояснення' }} />
        <span>поза</span>
      </div>,
    )
    fireEvent.click(screen.getByRole('button', { name: 'Як рахується: Ефект' }))
    expect(screen.getByRole('tooltip')).toHaveTextContent('Пояснення')
    fireEvent.pointerDown(screen.getByText('поза'))
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
  })
})

describe('sumLines', () => {
  it('signs every term after the first and ends with the total', () => {
    const lines = sumLines(
      [
        { label: 'a', amount: 10 },
        { label: 'b', amount: -4 },
        { label: 'c', amount: 3 },
      ],
      { label: 'Σ', amount: 9 },
      fmt,
    )
    expect(lines.map((l) => [l.op ?? '', l.value])).toEqual([
      ['', '10 грн'],
      ['−', '4 грн'],
      ['+', '3 грн'],
      ['=', '9 грн'],
    ])
  })

  it('marks a negative first term', () => {
    expect(sumLines([{ label: 'a', amount: -2 }], { label: 'Σ', amount: -2 }, fmt)[0]).toMatchObject({
      op: '−',
      value: '2 грн',
    })
  })
})

describe('residual', () => {
  it('is what the terms leave of the total', () => {
    expect(residual(100, [30, 50])).toBe(20)
  })
})
