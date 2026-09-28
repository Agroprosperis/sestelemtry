import { describe, expect, it } from 'vitest'
import { generatePassword } from './password'

describe('generatePassword', () => {
  it('reads out easily: four groups of four, no look-alike characters', () => {
    for (let i = 0; i < 200; i++) {
      const pw = generatePassword()
      expect(pw).toMatch(/^[a-zA-Z2-9]{4}(-[a-zA-Z2-9]{4}){3}$/)
      expect(pw).not.toMatch(/[0O1lI]/)
    }
  })

  it('passes the server length policy and does not repeat', () => {
    const seen = new Set<string>()
    for (let i = 0; i < 200; i++) {
      const pw = generatePassword()
      expect([...pw].length).toBeGreaterThanOrEqual(10)
      seen.add(pw)
    }
    expect(seen.size).toBe(200)
  })
})
