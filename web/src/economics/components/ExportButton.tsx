import { useState } from 'react'
import { useMoney } from '../currency'

type Props = {
  // run builds and downloads the workbook from UAH figures: the ones on
  // screen when the page shows UAH, freshly fetched ones otherwise.
  run: (uahOnScreen: boolean) => Promise<void>
}

// ExportButton is the «Вивантажити в Excel» button of the economics
// tables. The workbook stays in UAH whatever the page shows.
export function ExportButton({ run }: Props) {
  const money = useMoney()
  const [state, setState] = useState<'idle' | 'busy' | 'error'>('idle')
  const uahOnScreen = money.currency === 'UAH'
  const label = uahOnScreen ? 'Вивантажити в Excel' : 'Вивантажити в Excel (у гривнях)'
  return (
    <button
      type="button"
      className="economics-export-btn"
      disabled={state === 'busy'}
      onClick={() => {
        setState('busy')
        run(uahOnScreen).then(
          () => setState('idle'),
          () => setState('error'),
        )
      }}
    >
      {state === 'busy' ? 'Вивантажуємо…' : state === 'error' ? 'Не вдалося вивантажити — ще раз' : label}
    </button>
  )
}
