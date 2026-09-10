/**
 * One-shot CSS theme migration: replace hardcoded hex/rgba with CSS vars.
 * Run: node scripts/migrate-theme-tokens.mjs
 *
 * Property-aware for whites/blacks (color vs background).
 */
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(__dirname, '../src')

const CSS_FILES = [
  'shell/shell.css',
  'dashboard/dashboard.css',
  'dashboard/components/PeriodPicker.css',
  'economics/economics.css',
  'control/control.css',
  'planner/planner.css',
  'station/station.css',
  'alerts/alerts.css',
  'import/import.css',
]

/** Normalize #fff → #ffffff, lowercase. */
function normHex(h) {
  let x = h.toLowerCase()
  if (x.length === 4) {
    x = `#${x[1]}${x[1]}${x[2]}${x[2]}${x[3]}${x[3]}`
  }
  return x
}

/**
 * Value-only map: safe regardless of property (same semantic in all contexts).
 * Order doesn't matter; applied as whole-token replace of hex literals.
 */
const VALUE_MAP = {
  // Text neutrals
  '#0f172a': 'var(--text)',
  '#334155': 'var(--text-secondary)',
  '#475569': 'var(--text-muted)',
  '#64748b': 'var(--text-subtle)',
  '#94a3b8': 'var(--text-faint)',
  '#98a2b3': 'var(--text-disabled)',
  '#667085': 'var(--text-label)',
  '#8a94a6': 'var(--chart-tick)',

  // Surfaces / borders (mostly bg/border — hex rarely used as text)
  '#f4f6f8': 'var(--bg-page)',
  '#f8fafc': 'var(--bg-sunken)',
  '#f1f5f9': 'var(--bg-muted)',
  '#fbfcfe': 'var(--bg-subtle)',
  '#edf1f5': 'var(--bg-panel)',
  '#eef1f6': 'var(--bg-panel-2)',
  '#eef2f7': 'var(--bg-panel-3)',
  '#f1f4f8': 'var(--bg-stripe)',
  '#e9edf3': 'var(--bg-table-head)',
  '#eef2f8': 'var(--bg-panel-2)',
  '#f5dfbd': 'var(--warn-peach)',

  '#e2e8f0': 'var(--border)',
  '#cbd5e1': 'var(--border-strong)',
  '#e4e9f0': 'var(--border-muted)',
  '#e6eaf1': 'var(--border-soft)',
  '#d6dce5': 'var(--border-hairline)',
  '#e7ecf2': 'var(--chart-grid)',

  // Accent / indigo
  '#6366f1': 'var(--accent)',
  '#eef2ff': 'var(--accent-soft)',
  '#4338ca': 'var(--accent-text)',
  '#c7d2fe': 'var(--accent-border)',

  // OK greens
  '#22c55e': 'var(--ok)',
  '#15803d': 'var(--ok-text)',
  '#166534': 'var(--ok-text-strong)',
  '#f0fdf4': 'var(--ok-bg)',
  '#ecfdf5': 'var(--ok-bg-soft)',
  '#bbf7d0': 'var(--ok-border)',
  '#16a34a': 'var(--ok-strong)',
  '#047857': 'var(--ok-deep)',
  '#dcfce7': 'var(--ok-chip)',
  '#86efac': 'var(--ok-tint)',
  '#a7f3d0': 'var(--ok-light)',

  // Warn
  '#f59e0b': 'var(--warn)',
  '#b45309': 'var(--warn-text)',
  '#92400e': 'var(--warn-text-strong)',
  '#fffbeb': 'var(--warn-bg)',
  '#fef3c7': 'var(--warn-bg-soft)',
  '#fde68a': 'var(--warn-border)',
  '#d97706': 'var(--warn-strong)',
  '#ea580c': 'var(--warn-deep)',
  '#ffedd5': 'var(--warn-chip)',
  '#f97316': 'var(--warn-orange)',
  '#fdba74': 'var(--warn-amber)',
  '#fed7aa': 'var(--warn-peach)',
  '#fff7ed': 'var(--warn-surface)',
  '#fb923c': 'var(--warn-orange)',

  // Err
  '#ef4444': 'var(--err)',
  '#b91c1c': 'var(--err-text)',
  '#dc2626': 'var(--err-strong)',
  '#fef2f2': 'var(--err-bg)',
  '#fee2e2': 'var(--err-bg-soft)',
  '#fecaca': 'var(--err-border)',
  '#fca5a5': 'var(--err-tint)',

  // Info / blue
  '#2563eb': 'var(--info)',
  '#3b82f6': 'var(--info-mid)',
  '#1d4ed8': 'var(--info-text)',
  '#1e40af': 'var(--info-deep)',
  '#2f6fed': 'var(--info-link)',
  '#eff6ff': 'var(--info-bg)',
  '#dbeafe': 'var(--info-bg-soft)',
  '#bfdbfe': 'var(--info-border)',
  '#93c5fd': 'var(--info-tint)',
  '#e0f2fe': 'var(--info-sky)',
  '#e8effd': 'var(--info-wash)',

  // Violet
  '#7c3aed': 'var(--violet)',
  '#5b21b6': 'var(--violet-deep)',
  '#8b5cf6': 'var(--violet-soft)',
  '#f5f3ff': 'var(--violet-bg)',
  '#ede9fe': 'var(--violet-bg-soft)',
  '#ddd6fe': 'var(--violet-border)',
  '#c4b5fd': 'var(--violet-tint)',

  '#0d9488': 'var(--teal)',
  '#0369a1': 'var(--sky)',
}

/** rgba / special string replacements (exact match on value). */
const RGBA_MAP = {
  'rgba(15, 23, 42, 0.04)': 'rgba(var(--shadow-color), 0.04)',
  'rgba(15, 23, 42, 0.05)': 'rgba(var(--shadow-color), 0.05)',
  'rgba(15, 23, 42, 0.12)': 'rgba(var(--shadow-color), 0.12)',
  'rgba(15, 23, 42, 0.14)': 'rgba(var(--shadow-color), 0.14)',
  'rgba(15, 23, 42, 0.16)': 'rgba(var(--shadow-color), 0.16)',
  'rgba(15, 23, 42, 0.22)': 'rgba(var(--shadow-color), 0.22)',
  'rgba(15, 23, 42, 0.25)': 'rgba(var(--shadow-color), 0.25)',
  'rgba(15, 23, 42, 0.28)': 'rgba(var(--shadow-color), 0.28)',
  'rgba(15, 23, 42, 0.35)': 'rgba(var(--shadow-color), 0.35)',
  'rgba(15, 23, 42, 0.45)': 'var(--overlay)',
  'rgba(15, 23, 42, 0.55)': 'var(--overlay-strong)',
  'rgba(16, 24, 40, 0.05)': 'rgba(var(--shadow-color), 0.05)',
  'rgba(16, 24, 40, 0.12)': 'rgba(var(--shadow-color), 0.12)',
  'rgba(16, 24, 40, 0.14)': 'rgba(var(--shadow-color), 0.14)',
  'rgba(99, 102, 241, 0.18)': 'var(--accent-ring)',
  'rgba(59, 130, 246, 0.05)': 'var(--info-soft-fill)',
  'rgba(30, 64, 175, 0.08)': 'var(--info-deep-fill)',
  'rgba(29, 78, 216, 0.08)': 'var(--info-deep-fill)',
  'rgba(30, 58, 138, 0.05)': 'var(--info-soft-fill)',
  'rgba(124, 58, 237, 0.08)': 'var(--violet-fill)',
  'rgba(148, 163, 184, 0.18)': 'var(--chart-cursor)',
  'rgba(255, 255, 255, 0.96)': 'var(--bg-elevated)',
  'rgba(255, 255, 255, 0.45)': 'color-mix(in srgb, var(--bg-elevated) 45%, transparent)',
}

/**
 * Property-aware rules: [propPattern, hex, token]
 * Applied before blind VALUE_MAP for whites / switch colors.
 */
const PROP_RULES = [
  // White as text / fill on dark controls
  { prop: /^(color|fill|-webkit-text-fill-color)$/i, hex: '#ffffff', token: 'var(--text-inverse)' },
  { prop: /^(color|fill|-webkit-text-fill-color)$/i, hex: '#fff', token: 'var(--text-inverse)' },
  // White as border (spinner etc.)
  { prop: /^border(-[a-z]+)?(-color)?$/i, hex: '#ffffff', token: 'var(--text-inverse)' },
  { prop: /^border(-[a-z]+)?(-color)?$/i, hex: '#fff', token: 'var(--text-inverse)' },
  // White as background / box-shadow color contexts → elevated
  { prop: /^(background|background-color)$/i, hex: '#ffffff', token: 'var(--bg-elevated)' },
  { prop: /^(background|background-color)$/i, hex: '#fff', token: 'var(--bg-elevated)' },
  // Primary button hover (darker slate)
  { prop: /^(background|background-color)$/i, hex: '#1e293b', token: 'var(--btn-primary-hover)' },
  // #1e293b as text → secondary
  { prop: /^color$/i, hex: '#1e293b', token: 'var(--text-secondary)' },
]

function replaceInDeclaration(prop, value) {
  let v = value.trim()

  // Exact rgba maps
  for (const [from, to] of Object.entries(RGBA_MAP)) {
    if (v === from) return to
    v = v.split(from).join(to)
  }

  // Property-aware hex
  const hexMatch = v.match(/#([0-9a-fA-F]{3,8})\b/)
  if (hexMatch) {
    const raw = `#${hexMatch[1]}`
    const n = normHex(raw)
    for (const rule of PROP_RULES) {
      if (rule.prop.test(prop) && (normHex(rule.hex) === n || rule.hex.toLowerCase() === raw.toLowerCase())) {
        return v.replace(raw, rule.token)
      }
    }
  }

  // Blind value map for remaining hex tokens in the value
  v = v.replace(/#([0-9a-fA-F]{3,8})\b/g, (m) => {
    const n = normHex(m)
    if (n === '#ffffff' || m.toLowerCase() === '#fff') {
      // leftover white in shorthand (box-shadow etc.) → elevated surface
      return 'var(--bg-elevated)'
    }
    return VALUE_MAP[n] ?? m
  })

  return v
}

function migrateCss(css) {
  // Remove the light-only prefers-color-scheme comment block in economics if present
  css = css.replace(
    /\/\*\s*The app uses a single light theme[\s\S]*?\*\//,
    '/* Theme tokens come from web/src/index.css (data-theme). */',
  )

  // Walk declaration blocks: property: value;
  // Avoid rewriting inside comments by stripping comments temporarily… keep simple: line-based + regex
  return css.replace(
    /(^|[{;/])(\s*)([a-zA-Z-]+)\s*:\s*([^;{}]+);/gm,
    (full, prefix, ws, prop, value) => {
      // skip if value already uses var(
      if (value.includes('var(--')) return full
      const next = replaceInDeclaration(prop, value)
      if (next === value.trim()) return full
      return `${prefix}${ws}${prop}: ${next};`
    },
  )
}

let total = 0
for (const rel of CSS_FILES) {
  const file = path.join(SRC, rel)
  const before = fs.readFileSync(file, 'utf8')
  const after = migrateCss(before)
  if (after !== before) {
    fs.writeFileSync(file, after)
    const beforeHex = (before.match(/#[0-9a-fA-F]{3,8}/g) || []).length
    const afterHex = (after.match(/#[0-9a-fA-F]{3,8}/g) || []).length
    console.log(`${rel}: ${beforeHex} → ${afterHex} hex literals`)
    total += beforeHex - afterHex
  } else {
    console.log(`${rel}: unchanged`)
  }
}
console.log(`Done. Removed ~${total} hex literals.`)
