import type { Command, CommandType, Issue, SiteInfo } from './dispatchClient'
import { FUTURE_HOURS, commandDesc, fmt, intents, optionTime, paramLabel, scenarioOptions, timeLabel, type Clock } from './model'
import type { CommandOutcome } from './outcome'

const fromOptions = Array.from({ length: FUTURE_HOURS }, (_, i) => i)

export function IntervalHead({
  clock,
  span,
  start,
  end,
  onStart,
  onEnd,
}: {
  clock: Clock
  span: string
  start: number
  end: number
  onStart: (start: number) => void
  onEnd: (end: number) => void
}) {
  return (
    <div className="d-edit-head">
      <strong aria-live="polite">
        Інтервал · {span} · {end - start} год
      </strong>
      <div className="d-range">
        <label>
          Від
          <select aria-label="Початок ручного інтервалу" value={start} onChange={(e) => onStart(Number(e.target.value))}>
            {fromOptions.map((i) => (
              <option key={i} value={i}>
                {optionTime(clock, i)}
              </option>
            ))}
          </select>
        </label>
        <label>
          До
          <select aria-label="Кінець ручного інтервалу" value={end} onChange={(e) => onEnd(Number(e.target.value))}>
            {fromOptions.map((i) => (
              <option key={i + 1} value={i + 1}>
                {optionTime(clock, i + 1)}
              </option>
            ))}
          </select>
        </label>
      </div>
    </div>
  )
}

export function LoadEditor({
  value,
  uniform,
  inDraft,
  canClear,
  error,
  onChange,
  onSet,
  onClear,
}: {
  value: string
  uniform: boolean
  inDraft: boolean
  canClear: boolean
  error: string
  onChange: (value: string) => void
  onSet: () => void
  onClear: () => void
}) {
  return (
    <section className="d-load-editor" aria-label="Споживання у вибрані години">
      <label className="d-field">
        Очікуване споживання
        <span className="d-input-unit">
          <input
            type="number"
            min={0}
            step={10}
            placeholder={uniform ? 'Не задано' : 'Різні значення'}
            value={value}
            onChange={(e) => onChange(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault()
                onSet()
              }
            }}
            aria-label="Споживання для вибраних годин"
          />
          <span>кВт</span>
        </span>
      </label>
      <div className="d-load-actions">
        <button type="button" aria-label="Задати споживання для вибраного інтервалу" onClick={onSet}>
          Задати
        </button>
        <button type="button" className="d-quiet" disabled={!canClear} onClick={onClear}>
          Очистити
        </button>
      </div>
      <p className="d-small" aria-live="polite">
        {inDraft ? 'У чернетці. ' : ''}
        Порожньо = невідомо; 0 = споживання немає.
      </p>
      {error && (
        <p className="d-small d-error" role="alert">
          {error}
        </p>
      )}
    </section>
  )
}

export function CommandFields({
  site,
  exportCapKw,
  scenario,
  onScenario,
  param,
  onParam,
  reserve,
  onReserve,
  direction,
  onDirection,
}: {
  site: SiteInfo
  exportCapKw: number | null
  scenario: CommandType
  onScenario: (s: CommandType) => void
  param: string
  onParam: (value: string) => void
  reserve: string
  onReserve: (value: string) => void
  direction: 'charge' | 'discharge'
  onDirection: (d: 'charge' | 'discharge') => void
}) {
  return (
    <div className="d-fields">
      <label className="d-field">
        Що потрібно зробити
        <select value={scenario} onChange={(e) => onScenario(e.target.value as CommandType)}>
          {scenarioOptions.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
      </label>
      {scenario !== 'hold' && scenario !== 'auto' && (
        <label className="d-field">
          <span>{paramLabel(scenario)}</span>
          <span className="d-input-unit">
            <input
              type="number"
              min={0}
              step={scenario === 'target' ? 5 : 10}
              max={
                scenario === 'target'
                  ? site.soc_max_pct
                  : scenario === 'cap'
                    ? site.import_kw
                    : scenario === 'export'
                      ? (exportCapKw ?? undefined)
                      : Math.max(site.charge_kw, site.discharge_kw)
              }
              value={param}
              onChange={(e) => onParam(e.target.value)}
            />
            <span>{scenario === 'target' ? '%' : 'кВт'}</span>
          </span>
        </label>
      )}
      <label className="d-field">
        Резерв SOC для чернетки
        <span className="d-input-unit">
          <input
            type="number"
            min={site.soc_min_pct}
            max={site.soc_max_pct}
            step={5}
            value={reserve}
            onChange={(e) => onReserve(e.target.value)}
            aria-label="Операційний резерв SOC"
          />
          <span>%</span>
        </span>
      </label>
      {scenario === 'fixed' && (
        <label className="d-field">
          Напрямок потужності
          <select value={direction} onChange={(e) => onDirection(e.target.value as 'charge' | 'discharge')}>
            <option value="discharge">Розряд</option>
            <option value="charge">Заряд</option>
          </select>
        </label>
      )}
    </div>
  )
}

// loadKw is null when some selected hour has no load.
export function CommandHelp({ scenario, pvKw, loadKw, prices }: { scenario: CommandType; pvKw: number; loadKw: number | null; prices: number[] }) {
  return (
    <details className="d-help">
      <summary>Пояснення дії та прогноз інтервалу</summary>
      <p className="d-intent">{intents[scenario]}</p>
      <p className="d-small">
        Після завершення ручної команди AUTO перерахує план від фактичного стану. Інші ручні інтервали залишаються заданими. Раніші ручні
        команди мають пріоритет: пізніша команда не зменшує їх виконання.
      </p>
      <div className="d-small">
        У вибрані години: СЕС у середньому {fmt(pvKw)} кВт · споживання{' '}
        {loadKw !== null ? fmt(loadKw) + ' кВт' : 'задано не для всіх годин'} · РДН{' '}
        {prices.length ? fmt(Math.min(...prices)) + '–' + fmt(Math.max(...prices)) : '—'} грн/кВт·год
      </div>
    </details>
  )
}

function IssueGroup({ label, items, clock, commands }: { label: string; items: Issue[]; clock: Clock; commands: (Command | null)[] }) {
  if (!items.length) return null
  const rows = (xs: Issue[]) =>
    xs.map((x) => (
      <li key={x.hour + x.text}>
        {timeLabel(clock, x.hour)}–{timeLabel(clock, x.hour + 1)} · {commandDesc(commands[x.hour])}: {x.text}
      </li>
    ))
  return (
    <>
      <div className="d-warning-group">{label}</div>
      <ul>{rows(items.slice(0, 2))}</ul>
      {items.length > 2 && (
        <details>
          <summary>Ще {items.length - 2} інтервалів</summary>
          <ul>{rows(items.slice(2))}</ul>
        </details>
      )}
    </>
  )
}

// CommandPreview shows the selected interval's outcome, or why the
// command is invalid and which hours fall short.
export function CommandPreview({
  invalid,
  outcome,
  issues,
  clock,
  commands,
  start,
  end,
}: {
  invalid: string
  outcome: CommandOutcome
  issues: Issue[]
  clock: Clock
  commands: (Command | null)[]
  start: number
  end: number
}) {
  const commandIssues = issues.filter((x) => x.hour >= start && x.hour < end)
  const laterIssues = issues.filter((x) => x.hour >= end)
  const earlierIssues = issues.filter((x) => x.hour < start)
  return (
    <>
      {!invalid && (
        <div className="d-preview" aria-live="polite">
          <div className="d-metric">
            <span>{outcome.label}</span>
            <strong>{outcome.value}</strong>
            {outcome.comparison && <span className="d-compare">{outcome.comparison}</span>}
            <span>{outcome.detail}</span>
          </div>
        </div>
      )}
      {(invalid || issues.length > 0) && (
        <div className="d-warning" role="alert">
          {invalid ? (
            invalid
          ) : (
            <>
              <strong>{commandIssues.length ? 'Команда не виконується повністю.' : 'Є невиконані умови в інших годинах.'}</strong>
              <IssueGroup label="У вибраному інтервалі" items={commandIssues} clock={clock} commands={commands} />
              <IssueGroup label={'Інші інтервали після ' + timeLabel(clock, end)} items={laterIssues} clock={clock} commands={commands} />
              <IssueGroup label={'Інші інтервали до ' + timeLabel(clock, start)} items={earlierIssues} clock={clock} commands={commands} />
              <p>Можна додати у чернетку й продовжити редагування. Перед застосуванням потрібно виправити позначені інтервали.</p>
            </>
          )}
        </div>
      )}
    </>
  )
}
