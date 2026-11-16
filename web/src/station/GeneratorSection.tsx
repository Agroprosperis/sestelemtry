import { Engine, GasPump, Gauge, WifiHigh } from '@phosphor-icons/react'
import { useEffect, useState, type ReactNode } from 'react'
import { fetchGeneratorPassport, type GeneratorPassport } from '../api'
import { fuelMethodLabel } from '../economics/generatorText'
import { fetchTariffSchedule } from '../economics/orgTariffsClient'
import { fuelCostUah, fuelSet, type Tariffs } from '../economics/tariffs'
import { navigateEconomicsTariffs } from '../shell/navigation'

// Encombi readings older than this mean the link is down (the collector
// polls the controller every 5 s; handoff §4.1).
const LINK_FRESH_MS = 30 * 1000

function isAbortError(e: unknown): boolean {
  return e instanceof DOMException && e.name === 'AbortError'
}

function formatNumber(value: number, digits = 2): string {
  return value.toLocaleString('uk-UA', { maximumFractionDigits: digits })
}

function formatWhen(iso: string | undefined): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (!Number.isFinite(d.getTime())) return '—'
  return d.toLocaleString('uk-UA', {
    day: '2-digit',
    month: '2-digit',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

function todayISO(now: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`
}

type PassportAnswer = {
  data: GeneratorPassport | null
  error: string | null
  // linkUp is the Encombi link judged when the answer came: the page
  // does not poll.
  linkUp: boolean
}

const NO_ANSWER: PassportAnswer = { data: null, error: null, linkUp: false }

// useGeneratorPassport loads the block; data is null until it answers,
// and for an organization whose request failed.
function useGeneratorPassport(organizationID: string): PassportAnswer {
  const [answer, setAnswer] = useState<(PassportAnswer & { org: string }) | null>(null)
  useEffect(() => {
    if (!organizationID) return
    const ac = new AbortController()
    fetchGeneratorPassport(organizationID, ac.signal)
      .then((data) => {
        const at = data.last_sample_at ? new Date(data.last_sample_at).getTime() : NaN
        const linkUp = Number.isFinite(at) && Date.now() - at < LINK_FRESH_MS
        setAnswer({ org: organizationID, data, error: null, linkUp })
      })
      .catch((e: unknown) => {
        if (isAbortError(e)) return
        setAnswer({
          org: organizationID,
          data: null,
          error: e instanceof Error ? e.message : 'request failed',
          linkUp: false,
        })
      })
    return () => ac.abort()
  }, [organizationID])
  return answer?.org === organizationID ? answer : NO_ANSWER
}

// useCurrentFuel reads the tariff version in force today, for an
// organization whose economics the user may see.
function useCurrentFuel(organizationID: string, enabled: boolean): Tariffs | null {
  const [answer, setAnswer] = useState<{ org: string; tariffs: Tariffs | null } | null>(null)
  useEffect(() => {
    if (!enabled || !organizationID) return
    const ac = new AbortController()
    fetchTariffSchedule(organizationID, ac.signal)
      .then((versions) => {
        const today = todayISO(new Date())
        const current = versions.filter((v) => v.effectiveFrom <= today).at(-1) ?? null
        setAnswer({ org: organizationID, tariffs: current?.tariffs ?? null })
      })
      .catch((e: unknown) => {
        if (isAbortError(e)) return
        setAnswer({ org: organizationID, tariffs: null })
      })
    return () => ac.abort()
  }, [organizationID, enabled])
  return enabled && answer?.org === organizationID ? answer.tariffs : null
}

function GenCard({ icon, label, value, children }: { icon: ReactNode; label: string; value: ReactNode; children?: ReactNode }) {
  return (
    <article className="station-card station-card-static">
      <div className="station-card-main static">
        <span className="station-card-icon" data-icon="gen">
          {icon}
        </span>
        <span className="station-card-body">
          <span className="station-card-label">{label}</span>
          <span className="station-card-value station-card-value-sm">{value}</span>
          {children}
        </span>
      </div>
    </article>
  )
}

function FuelCard({ organizationID, tariffs }: { organizationID: string; tariffs: Tariffs | null }) {
  const set = tariffs !== null && fuelSet(tariffs)
  const perHour = tariffs?.genFuelMethod === 'per_hour'
  return (
    <GenCard
      icon={<GasPump size={22} />}
      label="Пальне"
      value={
        set && tariffs
          ? `${formatNumber(fuelCostUah(tariffs))} ${perHour ? 'грн/год' : 'грн/кВт·год'}`
          : 'Не задане'
      }
    >
      <dl className="station-meta-dl">
        {set && tariffs ? (
          <>
            <div>
              <dt>Ціна літра</dt>
              <dd>
                {formatNumber(tariffs.genFuelPriceUahPerL)} грн/л {tariffs.includeVat ? 'з ПДВ' : 'без ПДВ'}
              </dd>
            </div>
            <div>
              <dt>Спосіб обліку</dt>
              <dd>{fuelMethodLabel(tariffs.genFuelMethod)}</dd>
            </div>
            <div>
              <dt>Витрата</dt>
              <dd>
                {perHour
                  ? `${formatNumber(tariffs.genFuelConsumptionLPerH)} л/год`
                  : `${formatNumber(tariffs.genFuelConsumptionLPerKwh, 3)} л/кВт·год`}
              </dd>
            </div>
          </>
        ) : (
          <div>
            <dd>Без ціни пального години на генераторі не рахуються в економіці.</dd>
          </div>
        )}
      </dl>
      <button type="button" className="station-link" onClick={() => navigateEconomicsTariffs(organizationID)}>
        Розклад тарифів
      </button>
    </GenCard>
  )
}

// GeneratorSection is the passport's read-only «Генератор» block (ems-spec
// generator handoff §5.4), shown only for a site with an Encombi. The fuel
// card needs economics.read, since it reads the tariff schedule.
export function GeneratorSection({ organizationID, canSeeFuel }: { organizationID: string; canSeeFuel: boolean }) {
  const { data, error, linkUp } = useGeneratorPassport(organizationID)
  const hasGenerator = data?.has_generator === true
  const tariffs = useCurrentFuel(organizationID, canSeeFuel && hasGenerator)

  if (error) {
    return (
      <div className="station-error" role="alert">
        Не вдалося завантажити дані генератора: {error}
      </div>
    )
  }
  if (!data || !hasGenerator) return null

  return (
    <section className="station-section" aria-labelledby="station-gen-title">
      <h2 id="station-gen-title">Генератор</h2>
      <div className="station-grid">
        <GenCard
          icon={<Engine size={22} />}
          label="Номінал"
          value={data.rated_kw != null ? `${formatNumber(data.rated_kw, 1)} кВт` : 'Ще не працював'}
        />
        {canSeeFuel ? <FuelCard organizationID={organizationID} tariffs={tariffs} /> : null}
        <GenCard
          icon={<Gauge size={22} />}
          label="Лічильник генератора"
          value={data.meter_kwh != null ? `${formatNumber(data.meter_kwh, 1)} кВт·год` : '—'}
        >
          <dl className="station-meta-dl">
            <div>
              <dt>Показник на</dt>
              <dd>{formatWhen(data.meter_at)}</dd>
            </div>
            <div>
              <dt>Остання робота</dt>
              <dd>{data.last_run_at ? formatWhen(data.last_run_at) : 'не зафіксована'}</dd>
            </div>
            {data.meter_alive != null ? (
              <div>
                <dt>Лічильник</dt>
                <dd>{data.meter_alive ? 'на зв’язку' : 'не відповідає'}</dd>
              </div>
            ) : null}
          </dl>
        </GenCard>
        <GenCard
          icon={<WifiHigh size={22} />}
          label="Encombi"
          value={linkUp ? 'На зв’язку' : 'Немає зв’язку'}
        >
          <dl className="station-meta-dl">
            <div>
              <dt>Адреса</dt>
              <dd>{data.device_host || '—'}</dd>
            </div>
            <div>
              <dt>Версія ПЗ</dt>
              <dd>{data.sw_version != null ? formatNumber(data.sw_version, 2) : '—'}</dd>
            </div>
            <div>
              <dt>Останні дані</dt>
              <dd>{formatWhen(data.last_sample_at)}</dd>
            </div>
          </dl>
        </GenCard>
      </div>
    </section>
  )
}
