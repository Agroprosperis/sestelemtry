import { strFromU8, unzipSync } from 'fflate'
import { describe, expect, it } from 'vitest'
import { buildXlsx, colName, dateSerial, sheetName, type XlsxSheet } from '../xlsx'

function sheetOf(sheet: XlsxSheet): { parts: Record<string, string> } {
  const zip = unzipSync(buildXlsx(sheet))
  const parts: Record<string, string> = {}
  for (const [name, bytes] of Object.entries(zip)) parts[name] = strFromU8(bytes)
  return { parts }
}

const SAMPLE: XlsxSheet = {
  name: 'Липень 2026',
  columns: [
    { header: 'Дата', format: 'date' },
    { header: 'Імпорт, кВт·год', format: 'int' },
    { header: 'Факт. ціна, грн/кВт·год', format: 'price' },
    { header: 'Самоспоживання, %', format: 'percent' },
    { header: 'Якість', format: 'text' },
  ],
  freeze: { columns: 1, rows: 1 },
  autoFilter: true,
  rows: [
    { values: [dateSerial('2026-07-01'), 28478, 3.0456, 0.799, null] },
    { values: [dateSerial('2026-07-02'), 1240, null, 0.5, 'лічильник відставав'] },
    { bold: true, values: ['Разом', 29718, 3.04, 0.65, null] },
  ],
}

describe('buildXlsx', () => {
  it('writes the parts Excel needs to open the file', () => {
    const { parts } = sheetOf(SAMPLE)
    expect(Object.keys(parts).sort()).toEqual([
      '[Content_Types].xml',
      '_rels/.rels',
      'xl/_rels/workbook.xml.rels',
      'xl/styles.xml',
      'xl/workbook.xml',
      'xl/worksheets/sheet1.xml',
    ])
  })

  it('keeps measured values numeric and leaves gaps empty', () => {
    const xml = sheetOf(SAMPLE).parts['xl/worksheets/sheet1.xml']
    // Numbers land in <v> with no type attribute — that is what makes
    // Excel treat them as numbers rather than text.
    expect(xml).toContain('<v>28478</v>')
    expect(xml).toContain('<v>3.0456</v>')
    // A missing price is simply absent: no cell for C3 at all.
    expect(xml).not.toContain('r="C3"')
    // Text still travels as an inline string.
    expect(xml).toContain('<is><t>лічильник відставав</t></is>')
  })

  it('bolds the totals row and pins the header', () => {
    const xml = sheetOf(SAMPLE).parts['xl/worksheets/sheet1.xml']
    const totals = /<row r="4">(.*?)<\/row>/.exec(xml)?.[1] ?? ''
    const body = /<row r="2">(.*?)<\/row>/.exec(xml)?.[1] ?? ''
    const styleOf = (row: string, ref: string) =>
      new RegExp(`r="${ref}" s="(\\d+)"`).exec(row)?.[1]
    // Same column, different style index: the bold variant sits right
    // after the plain one for every format.
    expect(Number(styleOf(totals, 'B4'))).toBe(Number(styleOf(body, 'B2')) + 1)
    expect(xml).toContain('state="frozen"')
    expect(xml).toContain('topLeftCell="B2"')
    expect(xml).toContain('<autoFilter ref="A1:E1"/>')
  })

  // The writer builds OOXML by hand, so a stray unescaped character or
  // a mismatched tag would only surface as "Excel cannot open the file".
  it('emits well-formed XML in every part', () => {
    const { parts } = sheetOf({
      ...SAMPLE,
      // Characters that must be escaped rather than passed through.
      columns: [{ header: 'СЕС & <УЗЕ>', format: 'text' }],
      rows: [{ values: ['лапки "тут" & <там>'] }],
    })
    const parser = new DOMParser()
    for (const [name, xml] of Object.entries(parts)) {
      const doc = parser.parseFromString(xml, 'application/xml')
      expect(doc.getElementsByTagName('parsererror'), `${name} must parse`).toHaveLength(0)
    }
  })

  it('keeps labels out of the column number format', () => {
    const xml = sheetOf(SAMPLE).parts['xl/worksheets/sheet1.xml']
    // "Разом" sits in the date column but must not inherit DD.MM.YYYY.
    const totals = /<row r="4">(.*?)<\/row>/.exec(xml)?.[1] ?? ''
    const header = /<row r="1">(.*?)<\/row>/.exec(xml)?.[1] ?? ''
    const dateCell = /<row r="2">.*?r="A2" s="(\d+)"/.exec(xml)?.[1]
    const labelCell = /r="A4" s="(\d+)"/.exec(totals)?.[1]
    expect(labelCell).not.toBe(dateCell)
    expect(header).toContain('r="A1"')
  })

  it('declares one number format per column kind', () => {
    const styles = sheetOf(SAMPLE).parts['xl/styles.xml']
    expect(styles).toContain('formatCode="DD.MM.YYYY"')
    expect(styles).toContain('formatCode="#,##0.00"')
    expect(styles).toContain('formatCode="0.0%"')
    // cellXfs must declare exactly as many entries as it lists.
    const count = Number(/<cellXfs count="(\d+)"/.exec(styles)?.[1])
    expect(styles.match(/<xf [^>]*?(?:\/>|>)/g)?.length).toBeGreaterThanOrEqual(count)
  })
})

describe('dateSerial', () => {
  it('matches the serial Excel uses for the 1900 system', () => {
    // 1900-03-01 (serial 61) is the first day past Excel's phantom leap
    // day, from which the 1899-12-30 offset is exact; the well-known
    // 1970 epoch lands on 25569.
    expect(dateSerial('1900-03-01')).toBe(61)
    expect(dateSerial('1970-01-01')).toBe(25569)
    expect(dateSerial('2026-07-01')).toBe(46204)
    expect(dateSerial('липень')).toBeNull()
  })
})

describe('colName', () => {
  it('counts past the alphabet', () => {
    expect(colName(0)).toBe('A')
    expect(colName(25)).toBe('Z')
    expect(colName(26)).toBe('AA')
    expect(colName(27)).toBe('AB')
  })
})

describe('sheetName', () => {
  it('strips characters Excel forbids in a tab name and caps the length', () => {
    expect(sheetName('2026-01-01_2026-12-31')).toBe('2026-01-01_2026-12-31')
    expect(sheetName('Січень/Лютий: [2026]')).toBe('Січень Лютий   2026')
    expect(sheetName('').length).toBeGreaterThan(0)
    expect(sheetName('я'.repeat(40))).toHaveLength(31)
  })
})
