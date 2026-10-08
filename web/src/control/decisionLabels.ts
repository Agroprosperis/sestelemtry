export const PLAN_SOURCE_LABELS: Record<string, string> = {
  manifest: 'План (manifest)',
  fallback: 'Fallback-пресет',
  config: 'Пресет із конфіга',
  override: 'Локальний override',
}

// Reason codes per diagnostics spec §3.2 — including no_plan_* and
// sl_alarm (обов'язкові підписи в Cloud UI).
export const REASON_LABELS: Record<string, string> = {
  plan_discharge: 'розряд за планом',
  plan_charge: 'заряд за планом',
  plan_hold: 'план: утримання (|план| ≤ 2 кВт)',
  no_plan_self_discharge: 'без плану на зараз — розряд у дефіцит',
  no_plan_self_charge: 'без плану на зараз — заряд від СЕС',
  no_plan_hold: 'без плану на зараз — утримання',
  self_charge: 'заряд від надлишку СЕС',
  self_discharge: 'розряд у локальний дефіцит',
  hold: 'утримання',
  sl_alarm: 'аварія SmartLogger — команда 0',
  data_fault: 'неповні дані — команда 0',
  pcs_shutdown: 'PCS вимкнено — команда 0',
  insufficient_data: 'недостатньо даних',
}
