// Minimal OOXML (.xlsx) writer for the dashboard's detail tables.
//
// The tables used to be exported as an HTML <table> served with an
// .xls name, which Excel opens only after a "the format does not match
// the extension" warning and which lands every number as text — the
// grouped thousands separator and the em-dash placeholders make sure of
// that. Nothing sums, nothing sorts, no number formats survive.
//
// A real workbook is a zip of a handful of XML parts, so writing one
// costs less than pulling in a spreadsheet library: values stay numeric,
// each column carries an Excel number format, headers and the totals row
// are bold, panes are frozen and the header row gets an autofilter.

import { zipSync, strToU8 } from 'fflate'

// XlsxFormat maps a column (or row) onto an Excel number format:
//   text  — inline string, no format
//   date  — a real date serial, rendered DD.MM.YYYY
//   int   — whole units (kWh)
//   money — whole UAH
//   price — UAH/kWh, two decimals
//   ratio — equivalent cycles and other unitless decimals
//   decimal1 — one decimal, as the hourly energy rows are shown
//   percent — stored as a fraction (0.939), rendered 93,9%
export type XlsxFormat =
  | 'text'
  | 'date'
  | 'int'
  | 'money'
  | 'price'
  | 'ratio'
  | 'decimal1'
  | 'percent'

// XlsxValue is one cell. null is written as a genuinely empty cell
// rather than a dash, so Excel's own aggregates skip it.
export type XlsxValue = number | string | null

export type XlsxColumn = {
  header: string
  format?: XlsxFormat
  // width in Excel character units; derived from the content when absent.
  width?: number
}

export type XlsxRow = {
  values: XlsxValue[]
  bold?: boolean
  // format overrides the per-column format for this whole row — the
  // hourly pivot needs it, since there each row (not each column) is
  // one metric with one unit.
  format?: XlsxFormat
}

export type XlsxSheet = {
  name: string
  columns: XlsxColumn[]
  rows: XlsxRow[]
  // freeze keeps the given number of leading columns / rows visible
  // while scrolling. The header row is row 1, so rows: 1 pins it.
  freeze?: { columns?: number; rows?: number }
  // autoFilter puts the sort/filter dropdowns on the header row.
  autoFilter?: boolean
}

const NUMBER_FORMATS: Record<Exclude<XlsxFormat, 'text'>, string> = {
  date: 'DD.MM.YYYY',
  int: '#,##0',
  money: '#,##0',
  price: '#,##0.00',
  ratio: '#,##0.00',
  decimal1: '#,##0.0',
  percent: '0.0%',
}

// FORMAT_ORDER fixes the style-index layout so xfIndex can compute an
// offset instead of carrying a lookup table through the writer.
const FORMAT_ORDER: XlsxFormat[] = [
  'text',
  'date',
  'int',
  'money',
  'price',
  'ratio',
  'decimal1',
  'percent',
]

// Style indices: 0 default, 1 header, then two per format (plain, bold).
const HEADER_XF = 1
const FIRST_FORMAT_XF = 2

function xfIndex(format: XlsxFormat, bold: boolean): number {
  const i = FORMAT_ORDER.indexOf(format)
  return FIRST_FORMAT_XF + (i < 0 ? 0 : i) * 2 + (bold ? 1 : 0)
}

function esc(s: string): string {
  return (
    s
      // XML 1.0 forbids raw control characters outright, and one stray
      // byte would make Excel reject the whole workbook rather than the
      // one cell — hence the deliberate control-character class.
      // eslint-disable-next-line no-control-regex
      .replace(/[\u0000-\u0008\u000b\u000c\u000e-\u001f]/g, '')
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
  )
}

// colName turns a 0-based column index into A, B … Z, AA, AB …
export function colName(index: number): string {
  let n = index
  let out = ''
  do {
    out = String.fromCharCode(65 + (n % 26)) + out
    n = Math.floor(n / 26) - 1
  } while (n >= 0)
  return out
}

// EPOCH_DAYS is the 1900 date system's offset: Excel counts from
// 1899-12-30 because it also keeps Lotus's phantom 1900-02-29. That
// phantom day makes the offset exact only from 1900-03-01 on; January
// and February 1900 would come out one day late. Telemetry starts in
// 2025, so the two dead months are not worth branching for.
const EPOCH_DAYS = Date.UTC(1899, 11, 30)

// dateSerial converts an ISO calendar day (YYYY-MM-DD) into the serial
// Excel stores, so exported days sort and filter as dates. Returns null
// for anything that is not a plain calendar day.
export function dateSerial(iso: string): number | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso)
  if (!m) return null
  const utc = Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3]))
  return Math.round((utc - EPOCH_DAYS) / 86400000)
}

function columnWidth(col: XlsxColumn, rows: XlsxRow[], index: number): number {
  if (col.width !== undefined) return col.width
  // The header may hold a label plus its unit ("Споживання, кВт·год"),
  // which is usually the widest thing in the column; sampling the body
  // catches the exceptions (long month names, big money figures).
  let widest = col.header.length
  for (const row of rows) {
    const v = row.values[index]
    if (v === null || v === undefined) continue
    const len = typeof v === 'number' ? Math.round(v).toString().length + 4 : v.length
    if (len > widest) widest = len
  }
  return Math.min(Math.max(widest + 2, 8), 40)
}

function cellXml(ref: string, value: XlsxValue, s: number): string {
  if (value === null || value === undefined) return ''
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) return ''
    return `<c r="${ref}" s="${s}"><v>${value}</v></c>`
  }
  const text = value.trim()
  if (text === '') return ''
  return `<c r="${ref}" s="${s}" t="inlineStr"><is><t>${esc(text)}</t></is></c>`
}

function sheetXml(sheet: XlsxSheet): string {
  const lastCol = colName(Math.max(sheet.columns.length - 1, 0))
  const lastRow = sheet.rows.length + 1

  const header = sheet.columns.map((c, i) => cellXml(`${colName(i)}1`, c.header, HEADER_XF)).join('')

  const body = sheet.rows
    .map((row, r) => {
      const cells = sheet.columns
        .map((col, i) => {
          const value = row.values[i] ?? null
          // A label never takes the column's (or row's) number format —
          // otherwise the "Разом" cell in a date column would carry
          // DD.MM.YYYY and reformat anything typed over it.
          const format = typeof value === 'string' ? 'text' : (row.format ?? col.format ?? 'text')
          return cellXml(`${colName(i)}${r + 2}`, value, xfIndex(format, row.bold === true))
        })
        .join('')
      return `<row r="${r + 2}">${cells}</row>`
    })
    .join('')

  const cols = sheet.columns
    .map((c, i) => `<col min="${i + 1}" max="${i + 1}" width="${columnWidth(c, sheet.rows, i)}" customWidth="1"/>`)
    .join('')

  const fCols = sheet.freeze?.columns ?? 0
  const fRows = sheet.freeze?.rows ?? 0
  const pane =
    fCols > 0 || fRows > 0
      ? `<pane${fCols > 0 ? ` xSplit="${fCols}"` : ''}${fRows > 0 ? ` ySplit="${fRows}"` : ''} topLeftCell="${colName(fCols)}${fRows + 1}" activePane="bottomRight" state="frozen"/>`
      : ''

  // Element order follows the CT_Worksheet sequence: dimension,
  // sheetViews, sheetFormatPr, cols, sheetData, then autoFilter.
  return (
    `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
    `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
    `<dimension ref="A1:${lastCol}${lastRow}"/>` +
    `<sheetViews><sheetView workbookViewId="0">${pane}</sheetView></sheetViews>` +
    `<sheetFormatPr defaultRowHeight="15"/>` +
    `<cols>${cols}</cols>` +
    `<sheetData><row r="1">${header}</row>${body}</sheetData>` +
    (sheet.autoFilter ? `<autoFilter ref="A1:${lastCol}1"/>` : '') +
    `</worksheet>`
  )
}

function stylesXml(): string {
  const codes = FORMAT_ORDER.filter((f) => f !== 'text') as Exclude<XlsxFormat, 'text'>[]
  const numFmts = codes
    .map((f, i) => `<numFmt numFmtId="${164 + i}" formatCode="${esc(NUMBER_FORMATS[f])}"/>`)
    .join('')
  const numFmtId = (f: XlsxFormat): number => {
    const i = codes.indexOf(f as Exclude<XlsxFormat, 'text'>)
    return i < 0 ? 0 : 164 + i
  }
  // Two xfs per format (plain, bold) laid out in FORMAT_ORDER, matching
  // xfIndex above.
  const formatXfs = FORMAT_ORDER.flatMap((f) => [
    `<xf numFmtId="${numFmtId(f)}" fontId="0" fillId="0" borderId="0" applyNumberFormat="1"/>`,
    `<xf numFmtId="${numFmtId(f)}" fontId="1" fillId="0" borderId="2" applyNumberFormat="1" applyFont="1" applyBorder="1"/>`,
  ]).join('')

  return (
    `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
    `<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
    `<numFmts count="${codes.length}">${numFmts}</numFmts>` +
    `<fonts count="2">` +
    `<font><sz val="11"/><name val="Calibri"/></font>` +
    `<font><b/><sz val="11"/><name val="Calibri"/></font>` +
    `</fonts>` +
    // fills[0] none and fills[1] gray125 are required placeholders.
    `<fills count="3">` +
    `<fill><patternFill patternType="none"/></fill>` +
    `<fill><patternFill patternType="gray125"/></fill>` +
    `<fill><patternFill patternType="solid"><fgColor rgb="FFF1F5F9"/><bgColor indexed="64"/></patternFill></fill>` +
    `</fills>` +
    `<borders count="3">` +
    `<border/>` +
    `<border><bottom style="thin"><color rgb="FF94A3B8"/></bottom></border>` +
    `<border><top style="thin"><color rgb="FF94A3B8"/></top></border>` +
    `</borders>` +
    `<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>` +
    `<cellXfs count="${2 + FORMAT_ORDER.length * 2}">` +
    `<xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>` +
    `<xf numFmtId="0" fontId="1" fillId="2" borderId="1" xfId="0" applyFont="1" applyFill="1" applyBorder="1" applyAlignment="1">` +
    `<alignment vertical="bottom" wrapText="1"/></xf>` +
    formatXfs +
    `</cellXfs>` +
    // The Normal named style is optional for Excel but readers that
    // follow the schema (openpyxl among them) warn without it.
    `<cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles>` +
    `</styleSheet>`
  )
}

// buildXlsx serialises one sheet into .xlsx bytes.
export function buildXlsx(sheet: XlsxSheet): Uint8Array {
  const files: Record<string, Uint8Array> = {
    '[Content_Types].xml': strToU8(
      `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
        `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
        `<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
        `<Default Extension="xml" ContentType="application/xml"/>` +
        `<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>` +
        `<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>` +
        `<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>` +
        `</Types>`,
    ),
    '_rels/.rels': strToU8(
      `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
        `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
        `<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>` +
        `</Relationships>`,
    ),
    'xl/workbook.xml': strToU8(
      `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
        `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" ` +
        `xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
        `<sheets><sheet name="${esc(sheetName(sheet.name))}" sheetId="1" r:id="rId1"/></sheets>` +
        `</workbook>`,
    ),
    'xl/_rels/workbook.xml.rels': strToU8(
      `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
        `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
        `<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>` +
        `<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>` +
        `</Relationships>`,
    ),
    'xl/styles.xml': strToU8(stylesXml()),
    'xl/worksheets/sheet1.xml': strToU8(sheetXml(sheet)),
  }
  return zipSync(files, { level: 6 })
}

// sheetName trims a title to what Excel accepts as a tab name: 31
// characters, none of : \ / ? * [ ].
export function sheetName(raw: string): string {
  const cleaned = raw.replace(/[:\\/?*[\]]/g, ' ').trim()
  return (cleaned === '' ? 'Дані' : cleaned).slice(0, 31)
}

// downloadXlsx saves the workbook. Revocation is deferred a tick for the
// same reason downloadCsv defers it: Safari and Firefox start the save
// asynchronously and a blob URL revoked synchronously after click() has
// already 404'd by the time they get there.
export function downloadXlsx(filename: string, sheet: XlsxSheet): void {
  const blob = new Blob([buildXlsx(sheet) as BlobPart], {
    type: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
  })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  a.rel = 'noopener'
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 0)
}
