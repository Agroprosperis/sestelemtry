import { useEffect, useId, useRef, useState, type ReactNode } from 'react'

// HelpTip is a «?» beside a form field that opens its explanation on mouse
// hover, on click or tap, and from the keyboard (focus + Enter or Space);
// Esc or a click outside closes it. The cards' info dots open on hover
// only, which a keyboard or a phone cannot do. The trigger is no <button>
// so a read-only (disabled) fieldset still lets it open.
export function HelpTip({ label, children }: { label: string; children: ReactNode }) {
  const [pinned, setPinned] = useState(false)
  const [hovered, setHovered] = useState(false)
  const ref = useRef<HTMLSpanElement>(null)
  const id = useId()
  const open = pinned || hovered

  useEffect(() => {
    if (!open) return
    // Capture, so Esc closes the tip and not the dialog around it.
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return
      e.stopPropagation()
      setPinned(false)
      setHovered(false)
    }
    const onPointer = (e: PointerEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setPinned(false)
        setHovered(false)
      }
    }
    document.addEventListener('keydown', onKey, true)
    document.addEventListener('pointerdown', onPointer)
    return () => {
      document.removeEventListener('keydown', onKey, true)
      document.removeEventListener('pointerdown', onPointer)
    }
  }, [open])

  return (
    <span
      ref={ref}
      className="help-tip"
      onPointerEnter={(e) => {
        if (e.pointerType === 'mouse') setHovered(true)
      }}
      onPointerLeave={(e) => {
        if (e.pointerType === 'mouse') setHovered(false)
      }}
    >
      <span
        role="button"
        tabIndex={0}
        className="help-tip-button"
        aria-label={`Пояснення: ${label}`}
        aria-expanded={open}
        aria-describedby={open ? id : undefined}
        onClick={(e) => {
          // A label around the field must not take the click as its own.
          e.preventDefault()
          setPinned((v) => !v)
        }}
        onKeyDown={(e) => {
          if (e.key !== 'Enter' && e.key !== ' ') return
          e.preventDefault()
          setPinned((v) => !v)
        }}
      >
        ?
      </span>
      {open && (
        <span id={id} role="tooltip" className="help-tip-bubble">
          {children}
        </span>
      )}
    </span>
  )
}
