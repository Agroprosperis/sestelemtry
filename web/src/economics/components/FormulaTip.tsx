import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import type { Formula } from '../formula'

// The bubble stays this far from the «i» and from the window's edges.
const GAP = 6
const MARGIN = 8
// A hover that leaves the «i» closes the bubble after this long, so the
// pointer can cross the gap into it.
const CLOSE_DELAY_MS = 120

// FormulaTip is the «i» beside a figure that opens how it is computed:
// what it means, the rule in words, and the same rule with the figure's
// own numbers. It opens on hover, on click or tap, and from the keyboard
// (focus + Enter or Space); Esc or a click outside closes it. The bubble
// is portaled to <body> so neither a card's edge nor a scrolling table
// cuts it off. `label` names the figure: the bubble's title and the
// button's accessible name.
export function FormulaTip({ label, formula }: { label?: string; formula: Formula }) {
  const [pinned, setPinned] = useState(false)
  const [hovered, setHovered] = useState(false)
  const anchor = useRef<HTMLButtonElement>(null)
  const bubble = useRef<HTMLDivElement | null>(null)
  const closeTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const id = useId()
  const open = pinned || hovered

  const cancelClose = useCallback(() => {
    if (closeTimer.current !== null) {
      clearTimeout(closeTimer.current)
      closeTimer.current = null
    }
  }, [])
  const closeSoon = useCallback(() => {
    cancelClose()
    closeTimer.current = setTimeout(() => {
      closeTimer.current = null
      setHovered(false)
    }, CLOSE_DELAY_MS)
  }, [cancelClose])
  const close = useCallback(() => {
    cancelClose()
    setPinned(false)
    setHovered(false)
  }, [cancelClose])

  useEffect(() => cancelClose, [cancelClose])

  // place puts the bubble below the «i» when it fits, above it otherwise,
  // never past the window's edges. It writes the position straight onto
  // the node: it follows every scroll, and the bubble renders hidden
  // until it has one.
  const place = useCallback(() => {
    const node = bubble.current
    const a = anchor.current?.getBoundingClientRect()
    if (!node || !a) return
    const b = node.getBoundingClientRect()
    const left = Math.max(MARGIN, Math.min(a.left + a.width / 2 - b.width / 2, window.innerWidth - b.width - MARGIN))
    const below = a.bottom + GAP
    const above = a.top - GAP - b.height
    const top = below + b.height <= window.innerHeight - MARGIN || above < MARGIN ? below : above
    node.style.left = `${Math.round(left)}px`
    node.style.top = `${Math.round(top)}px`
    node.style.visibility = 'visible'
  }, [])

  useLayoutEffect(() => {
    if (open) place()
  }, [open, place, formula, label])

  useEffect(() => {
    if (!open) return
    // Capture, so Esc closes the bubble and not a dialog around it.
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return
      e.stopPropagation()
      close()
      anchor.current?.focus()
    }
    const onPointer = (e: PointerEvent) => {
      const t = e.target as Node
      if (anchor.current?.contains(t) || bubble.current?.contains(t)) return
      close()
    }
    document.addEventListener('keydown', onKey, true)
    document.addEventListener('pointerdown', onPointer)
    window.addEventListener('scroll', place, true)
    window.addEventListener('resize', place)
    return () => {
      document.removeEventListener('keydown', onKey, true)
      document.removeEventListener('pointerdown', onPointer)
      window.removeEventListener('scroll', place, true)
      window.removeEventListener('resize', place)
    }
  }, [open, close, place])

  return (
    <>
      <button
        ref={anchor}
        type="button"
        className="eco-formula-trigger"
        aria-label={label ? `Як рахується: ${label}` : 'Як рахується'}
        aria-expanded={open}
        aria-describedby={open ? id : undefined}
        onPointerEnter={(e) => {
          if (e.pointerType !== 'mouse') return
          cancelClose()
          setHovered(true)
        }}
        onPointerLeave={(e) => {
          if (e.pointerType === 'mouse') closeSoon()
        }}
        onClick={(e) => {
          // A card, a row or a table header around the «i» must not take
          // the click as its own.
          e.preventDefault()
          e.stopPropagation()
          cancelClose()
          setPinned((v) => !v)
        }}
      >
        i
      </button>
      {open &&
        createPortal(
          <div
            ref={bubble}
            id={id}
            role="tooltip"
            className="eco-formula"
            style={{ left: 0, top: 0, visibility: 'hidden' }}
            onPointerEnter={(e) => {
              if (e.pointerType === 'mouse') cancelClose()
            }}
            onPointerLeave={(e) => {
              if (e.pointerType === 'mouse' && !pinned) closeSoon()
            }}
          >
            {label ? <div className="eco-formula-title">{label}</div> : null}
            {formula.text ? <p className="eco-formula-text">{formula.text}</p> : null}
            {formula.rule ? <div className="eco-formula-rule">{formula.rule}</div> : null}
            {formula.lines && formula.lines.length > 0 ? (
              <div className="eco-formula-lines">
                {formula.lines.map((l, i) => (
                  <div className={`eco-formula-line${l.op === '=' ? ' eco-formula-result' : ''}`} key={i}>
                    <span className="eco-formula-op">{l.op ?? ''}</span>
                    <span className="eco-formula-label">
                      {l.label}
                      {l.detail ? <small>{l.detail}</small> : null}
                    </span>
                    <b className="eco-formula-value">{l.value}</b>
                  </div>
                ))}
              </div>
            ) : null}
            {formula.note ? <p className="eco-formula-note">{formula.note}</p> : null}
          </div>,
          document.body,
        )}
    </>
  )
}
