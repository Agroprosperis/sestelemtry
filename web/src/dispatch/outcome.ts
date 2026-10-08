import type { CommandType, DeskHour, SimResult } from './dispatchClient'
import { fmt, unknownForecastText, type Clock, type DeskModel } from './model'

export type CommandOutcome = { label: string; value: string; detail: string; comparison: string }

// commandOutcome is the preview metric for the selected interval: energy
// moved by the command, the import peak under a cap, or the SOC reached.
export function commandOutcome(
  clock: Clock,
  result: SimResult,
  model: DeskModel,
  hours: DeskHour[],
  scenario: CommandType,
  paramRaw: string,
  direction: 'charge' | 'discharge',
  start: number,
  end: number,
): CommandOutcome {
  const selected = result.hours.slice(start, end)
  const complete = selected.every((h) => !h.unknown)
  const charged = selected.reduce((n, x) => n + Math.max(0, -(x.p_kw ?? 0)), 0)
  const discharged = selected.reduce((n, x) => n + Math.max(0, x.p_kw ?? 0), 0)
  const exported = selected.reduce((n, x) => n + (x.export_kw ?? 0), 0)
  const endSoc = result.hours[end - 1]?.soc_pct ?? null

  let label: string
  let value: string
  let detail: string
  let comparison = ''
  if (!complete) {
    label = 'Результат команди'
    value = 'Немає прогнозу'
    detail = unknownForecastText(clock, result, model, hours)
    if (scenario === 'export' || scenario === 'fixed') comparison = 'Запитано ' + fmt(Number(paramRaw) * (end - start)) + ' кВт·год'
  } else if (scenario === 'cap') {
    const peak = Math.max(...selected.map((x) => x.import_kw ?? 0))
    const limit = Number(paramRaw)
    label = 'AUTO · пік імпорту'
    value = fmt(peak) + ' кВт'
    detail =
      'Заданий ліміт ' + fmt(limit) + ' кВт' + (peak > limit + 0.05 ? ' · перевищення ' + fmt(peak - limit) + ' кВт' : ' · дотримано') +
      '. AUTO: заряд ' + fmt(charged) + ' · розряд ' + fmt(discharged) + ' кВт·год'
  } else if (scenario === 'target') {
    label = 'SOC наприкінці команди'
    value = fmt(endSoc) + '%'
    detail = 'Ціль ' + fmt(Number(paramRaw)) + '% · заряд ' + fmt(charged) + ' кВт·год'
  } else {
    label =
      scenario === 'export' ? 'Експорт PCC за інтервал' : discharged > 0 ? 'Розряд УЗЕ за інтервал' : charged > 0 ? 'Заряд УЗЕ за інтервал' : 'УЗЕ за інтервал'
    value = fmt(scenario === 'export' ? exported : discharged > 0 ? discharged : charged) + ' кВт·год'
    detail =
      scenario === 'export'
        ? 'УЗЕ: заряд ' + fmt(charged) + ' · розряд ' + fmt(discharged) + ' кВт·год'
        : charged > 0 && discharged > 0
          ? 'Заряд УЗЕ за інтервал: ' + fmt(charged) + ' кВт·год'
          : discharged > 0
            ? 'Енергія, віддана УЗЕ'
            : charged > 0
              ? 'Енергія, отримана УЗЕ'
              : 'Заряд і розряд відсутні'
    if (scenario === 'export' || scenario === 'fixed') {
      const requested = Number(paramRaw) * (end - start)
      const actual = scenario === 'export' ? exported : direction === 'charge' ? charged : discharged
      const shortfall = Math.max(0, requested - actual)
      comparison = 'Запитано ' + fmt(requested) + ' кВт·год' + (shortfall > 0.05 ? ' · бракує ' + fmt(shortfall) + ' кВт·год' : ' · виконується повністю')
    }
  }
  return { label, value, detail, comparison }
}
