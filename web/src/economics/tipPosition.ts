const TIP_OFFSET = 14
const TIP_MARGIN = 8

// tipLeft places a pointer-following tooltip at most `width` wide to the
// right of the pointer, shifted left as far as it takes to stay inside
// the viewport.
export function tipLeft(clientX: number, width: number): number {
  return Math.max(TIP_MARGIN, Math.min(clientX + TIP_OFFSET, window.innerWidth - width - TIP_MARGIN))
}
