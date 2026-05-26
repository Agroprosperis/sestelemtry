// Tariffs encapsulates every per-kWh number the daily economics
// model needs. The spec [daily_economic_model_mvp.md] uses these
// inputs to derive the hourly import/export prices that turn raw
// energy flows into UAH. All values are UAH/kWh except `vatRate`
// (fraction) and the two booleans.
//
// Defaults reflect the operator-supplied numbers for 2026-05-09:
//   - `distributionUahPerKwh`: 2-class tariff for ВІННИЦЯОБЛЕНЕРГО
//     (2752.18 грн/МВт·год без ПДВ; 1-class is 0.48810 if needed)
//   - `transmissionUahPerKwh`: НЕК "Укренерго" 2-й етап (з 01.04.2026)
//   - `exportDiscount`: 5% знижка на експортну сторону (per ТЗ)
//   - `degradationUahPerKwh`: вартість циклів УЗЕ (питомий знос)
//   - `supplierMargin` / `otherFees`: оператор-специфічні; зазвичай 0
//   - `includeVat=false`: моделюємо нетто; UI окремо показує brutto
//   - `essCapacityKwh`: для розрахунку equivalent cycles на годину
//
// `essCapacityKwh` is technically not a tariff but it lives in the
// same form on the page (and is persisted in the same JSONB blob), so
// we co-locate it here to keep the domain type symmetrical with the
// API DTO.
//
// Persistence: per-org tariffs live on the backend
// (`/api/v1/organization-tariffs`, see `useOrgTariffs`). DEFAULT_TARIFFS
// is the seed shown until the first GET resolves and the fallback for
// orgs that have never saved.
export type Tariffs = {
  distributionUahPerKwh: number
  transmissionUahPerKwh: number
  supplierMarginUahPerKwh: number
  otherFeesUahPerKwh: number
  exportDiscount: number
  degradationUahPerKwh: number
  includeVat: boolean
  vatRate: number
  essCapacityKwh: number
  // seedEssCostUahPerKwh is the фолбек собівартості залишку УЗЕ
  // на 00:00 коли вчорашні погодинні потоки/ціни недоступні
  // (новий пристрій, відсутній DAM, помилка fetch). Використовується
  // лише cost-basis алгоритмом у `costBasis.ts`; не впливає на
  // спот-розрахунок hourEconomics. Значення 0 трактує seed-кВт·год
  // як "вільну" енергію — занижує перші розряди дня, поки залишок
  // не витече, тому оператор може поставити сюди середню ціну
  // заряду минулого тижня, якщо хоче консервативніший облік.
  seedEssCostUahPerKwh: number
}

export const DEFAULT_TARIFFS: Tariffs = {
  distributionUahPerKwh: 2.75218,
  transmissionUahPerKwh: 0.74291,
  supplierMarginUahPerKwh: 0,
  otherFeesUahPerKwh: 0,
  exportDiscount: 0.05,
  degradationUahPerKwh: 0.6,
  includeVat: false,
  vatRate: 0.2,
  essCapacityKwh: 215,
  seedEssCostUahPerKwh: 0,
}
