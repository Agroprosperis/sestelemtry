// Look-alikes (0/O, 1/l/I) are left out: an administrator reads these
// passwords out over the phone or retypes them from a message.
const ALPHABET = 'abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789'
// Bytes at or above this bound are dropped, so every character of the
// alphabet is equally likely.
const UNBIASED_LIMIT = 256 - (256 % ALPHABET.length)

// generatePassword returns a temporary password like "hK7p-9qRt-mW3z-xE4b":
// four groups of four characters, about 92 bits of entropy.
export function generatePassword(groups = 4, size = 4): string {
  const chars: string[] = []
  const bytes = new Uint8Array(64)
  while (chars.length < groups * size) {
    crypto.getRandomValues(bytes)
    for (const b of bytes) {
      if (b >= UNBIASED_LIMIT) continue
      chars.push(ALPHABET[b % ALPHABET.length])
      if (chars.length === groups * size) break
    }
  }
  const out: string[] = []
  for (let g = 0; g < groups; g++) out.push(chars.slice(g * size, (g + 1) * size).join(''))
  return out.join('-')
}

// copyText puts text on the clipboard. navigator.clipboard exists only
// over HTTPS, so plain HTTP falls back to the older selection copy.
export async function copyText(text: string): Promise<boolean> {
  if (navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch {
      // fall through to the selection copy
    }
  }
  const area = document.createElement('textarea')
  area.value = text
  area.setAttribute('readonly', '')
  area.style.position = 'fixed'
  area.style.opacity = '0'
  document.body.appendChild(area)
  area.select()
  try {
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    document.body.removeChild(area)
  }
}
