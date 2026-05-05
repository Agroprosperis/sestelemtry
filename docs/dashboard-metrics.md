# Перелік метрик дашборду SESTelemetry

Документ описує всі поля (`metric_key`), якими оперує дашборд: переклад,
логічну суть, одиниці виміру, прив'язку до Modbus-регістрів Huawei
SmartLogger та спосіб використання у фронтенді/беку.

Джерела істини:

- Реєстр Modbus: `[registers/huawei_smartlogger.yaml](../registers/huawei_smartlogger.yaml)`
- Серверний default-конфіг дашборду: `DefaultDashboardConfig` у
`[internal/api/types.go](../internal/api/types.go)`
- Фронтендний fallback: `FALLBACK_DASHBOARD_CONFIG` у
`[web/src/dashboard/config.ts](../web/src/dashboard/config.ts)`
- Метрики для `EnergySummary`: `EnergySummaryAccumulators` у
`[internal/api/types.go](../internal/api/types.go)`

> Усі регістри читаються Modbus FC3 з прямою адресацією
> (`holding_address_base: 0`), порядок байтів `ABCD_BE`. Щоб отримати
> значення в інженерних одиницях, сире число множиться на `gain`.

## 1. Зведена таблиця


| `metric_key`                            | UA-переклад                           | EN-переклад                              | Од. | Modbus адреса | Тип    | Gain  |
| --------------------------------------- | ------------------------------------- | ---------------------------------------- | --- | ------------- | ------ | ----- |
| `active_pv_power_kw`                    | Активна потужність СЕС                | Active PV power                          | kW  | 40388         | UINT32 | 0.001 |
| `active_ess_power_kw`                   | Активна потужність УЗЕ                | Active ESS power                         | kW  | 40392         | INT32  | 0.001 |
| `power_supply_from_grid_day_kwh`        | Постачання з мережі за день           | Power supply from grid today             | kWh | 40438         | UINT32 | 0.01  |
| `pv_energy_yield_day_kwh`               | Виробіток СЕС за день                 | PV energy yield of the day               | kWh | 40444         | UINT32 | 0.01  |
| `accumulated_pv_energy_yield_kwh`       | Накопичений виробіток СЕС             | Accumulated PV energy yield              | kWh | 40446         | INT64  | 0.01  |
| `accumulated_electricity_purchased_kwh` | Накопичене споживання з мережі        | Accumulated electricity purchased        | kWh | 40450         | INT64  | 0.01  |
| `accumulated_electricity_sold_kwh`      | Накопичений відпуск у мережу          | Accumulated electricity sold             | kWh | 40454         | INT64  | 0.01  |
| `accumulated_power_consumption_kwh`     | Накопичене споживання навантаження    | Accumulated power consumption            | kWh | 40458         | INT64  | 0.01  |
| `total_power_supply_from_grid_kwh`      | Загальне постачання з мережі          | Total power supply from grid             | kWh | 40464         | INT64  | 0.01  |
| `energy_charged_day_kwh`                | Заряд УЗЕ за день                     | Current-day charge capacity              | kWh | 40468         | UINT32 | 0.01  |
| `energy_discharged_day_kwh`             | Розряд УЗЕ за день                    | Energy discharged today                  | kWh | 40470         | UINT32 | 0.01  |
| `total_energy_charged_kwh`              | Загальна енергія заряду УЗЕ           | Total energy charged                     | kWh | 40472         | INT64  | 0.01  |
| `total_energy_discharged_kwh`           | Загальна енергія розряду УЗЕ          | Total energy discharged                  | kWh | 40476         | UINT64 | 0.01  |
| `load_power_kw`                         | Потужність навантаження               | Load power                               | kW  | 40503         | UINT32 | 0.001 |
| `grid_connected_active_power_kw`        | Активна потужність у точці приєднання | Grid-connected active power              | kW  | 40505         | INT32  | 0.001 |
| `power_consumption_day_kwh`             | Споживання елеватора за день          | Current day power consumption            | kWh | 40509         | UINT32 | 0.01  |
| `electricity_sold_day_kwh`              | Експорт в мережу за день              | Electricity sales volume of the day      | kWh | 40511         | UINT32 | 0.01  |
| `electricity_purchased_day_kwh`         | Імпорт з мережі за день               | Electricity purchased on the current day | kWh | 40513         | UINT32 | 0.01  |
| `soc_percent`                           | Рівень заряду УЗЕ (SOC)               | State of Charge                          | %   | 40515         | UINT16 | 0.1   |


## 2. Метрики по групах

### 2.1. Миттєві потужності (kW, kW, %)

Це лічильники "тут і зараз" — змінюються з кожним опитуванням і
лягають в основу графіка потужностей `power_chart`.

#### `active_pv_power_kw`

- Переклад: **Активна потужність СЕС** / *Active PV power*
- Modbus: 40388, UINT32, gain 0.001 → kW
- Суть: миттєва генерація фотовольтаїки (DC→AC після інвертора).
На графіку йде в одну сторону з `load_power_kw`, бо панелі віддають
енергію в систему. Уночі ≈ 0.

#### `active_ess_power_kw`

- Переклад: **Активна потужність УЗЕ (Установка зберігання енергії)** /
*Active ESS power*
- Modbus: 40392, INT32, gain 0.001 → kW
- Суть: потужність акумуляторної системи. Знак ≥ 0 — заряд, ≤ 0 —
розряд (трактування знака залежить від прошивки інвертора). Разом із
`soc_percent` показує, що зараз робить батарея.

#### `load_power_kw`

- Переклад: **Потужність навантаження** / *Load power*
- Modbus: 40503, UINT32, gain 0.001 → kW
- Суть: миттєве споживання об'єкта (будинку/підприємства). Завжди ≥ 0.
На графіку малюється як споживач.

#### `grid_connected_active_power_kw`

- Переклад: **Активна потужність у точці приєднання до мережі** /
*Grid-connected active power*
- Modbus: 40505, INT32, gain 0.001 → kW
- Суть: переток у точці підключення до зовнішньої мережі. Знак залежить
від прошивки: один знак — забір з мережі, інший — віддача в мережу.
Це база для розрахунку "звідки/куди йде енергія" в реальному часі.

#### `soc_percent`

- Переклад: **Рівень заряду УЗЕ (SOC)** / *State of Charge*
- Modbus: 40515, UINT16, gain 0.1 → %
- Суть: процент заряду акумулятора (0–100). Допомагає оператору
розуміти, чи може батарея зараз віддавати/приймати енергію.

### 2.2. Денні лічильники (kWh, скидається опівночі)

Це блок "сьогоднішніх" регістрів, який інвертор сам обнуляє о 00:00
локального часу. Усі вони UINT32 з gain 0.01 (мінімальний крок —
0.01 кВт·год). На відміну від накопичувальних лічильників із §2.3,
дельти за день рахувати на клієнті/бекенді не потрібно — значення
читається напряму як `kWh за поточну добу`. На дашборді ці поля
рендеряться окремою секцією **"Сьогоднішні лічильники"**
(`web/src/dashboard/components/TodayCountersNarrative.tsx`).

#### `pv_energy_yield_day_kwh`

- Переклад: **Виробіток СЕС за день** / *PV energy yield of the day*
- Modbus: 40444, UINT32, gain 0.01 → kWh
- Суть: добова сума сонячної генерації. Інвертор сам скидає його о 00:00
локального часу. Зручний як індикатор "скільки СЕС зробила сьогодні",
без необхідності рахувати дельти на клієнті.

#### `power_supply_from_grid_day_kwh`

- Переклад: **Постачання з мережі за день** /
*Power supply from grid today*
- Modbus: 40438, UINT32, gain 0.01 → kWh
- Суть: добове постачання з мережі за версією інвертора. Семантично
близький до `electricity_purchased_day_kwh`, але читається з іншого
регістра — корисний для крос-верифікації.

#### `energy_charged_day_kwh`

- Переклад: **Заряд УЗЕ за день** / *Current-day charge capacity*
- Modbus: 40468, UINT32, gain 0.01 → kWh
- Суть: скільки кВт·год батарея прийняла за поточну добу.

#### `energy_discharged_day_kwh`

- Переклад: **Розряд УЗЕ за день** / *Energy discharged today*
- Modbus: 40470, UINT32, gain 0.01 → kWh
- Суть: скільки кВт·год батарея віддала за поточну добу.

#### `power_consumption_day_kwh`

- Переклад: **Споживання елеватора за день** /
*Current day power consumption*
- Modbus: 40509, UINT32, gain 0.01 → kWh
- Суть: добове споживання навантаження за версією інвертора.
**Особливість:** поведінка цього регістра аналогічна
`accumulated_power_consumption_kwh` — на деяких прошивках він може
повертати 0; у такому випадку використовуйте обчислену денну дельту
з графіка.

#### `electricity_sold_day_kwh`

- Переклад: **Експорт в мережу за день** /
*Electricity sales volume of the day*
- Modbus: 40511, UINT32, gain 0.01 → kWh
- Суть: скільки кВт·год об'єкт віддав у зовнішню мережу за поточну
добу.

#### `electricity_purchased_day_kwh`

- Переклад: **Імпорт з мережі за день** /
*Electricity purchased on the current day*
- Modbus: 40513, UINT32, gain 0.01 → kWh
- Суть: скільки кВт·год об'єкт спожив із зовнішньої мережі за поточну
добу.

### 2.3. Накопичувальні лічильники за весь час життя інвертора (kWh)

Ці регістри тільки зростають у штатному режимі. На дашборді з них
обчислюються деривативи: для **денного** графіка — на клієнті як сума
5-хвилинних дельт; для **місячного/річного** — на бекенді як
`last_value_at_end - last_value_at_start` (метод `end − seed`,
кламп до 0). Якщо лічильник з якоїсь причини провалюється (firmware
self-recalibration, скидання інвертора), у summary за період вийде
0 кВт·год — це навмисний сигнал про пошкоджені дані, а не "вигадане"
число.

#### `accumulated_pv_energy_yield_kwh`

- Переклад: **Накопичений виробіток СЕС** / *Accumulated PV energy yield*
- Modbus: 40446, INT64, gain 0.01 → kWh
- Суть: сумарна вироблена сонячна енергія за весь час експлуатації.
Найважливіший показник для оцінки продуктивності станції за період.

#### `accumulated_electricity_purchased_kwh`

- Переклад: **Накопичене споживання з мережі ("Взяли з мережі")** /
*Accumulated electricity purchased*
- Modbus: 40450, INT64, gain 0.01 → kWh
- Суть: скільки кВт·год об'єкт спожив із зовнішньої мережі за весь час.
Це джерело розрахунку показника **"Взяли з мережі"** у summary.

#### `accumulated_electricity_sold_kwh`

- Переклад: **Накопичений відпуск у мережу ("Віддали в мережу")** /
*Accumulated electricity sold*
- Modbus: 40454, INT64, gain 0.01 → kWh
- Суть: скільки кВт·год об'єкт віддав у зовнішню мережу (продаж/обмін).
Це джерело показника **"Віддали в мережу"** у summary.

#### `accumulated_power_consumption_kwh`

- Переклад: **Накопичене споживання навантаження** /
*Accumulated power consumption*
- Modbus: 40458, INT64, gain 0.01 → kWh
- Суть: сумарне споживання об'єкта (усе, що пройшло крізь точку
навантаження) за весь час. **Особливість:** на деяких прошивках
Huawei цей регістр повертає 0 — у такому разі і бекенд, і фронтенд
застосовують `applyApplianceConsumptionRule`, який обчислює його
алгебраїчно як `pv + discharged + purchased − charged − sold`.
Якщо ж лічильник звітує справжнє значення, воно зберігається без
модифікацій.

#### `total_power_supply_from_grid_kwh`

- Переклад: **Загальне постачання з мережі** / *Total power supply from grid*
- Modbus: 40464, INT64, gain 0.01 → kWh
- Суть: інтегральне постачання з мережі (за версією інвертора).
Семантично близький до `accumulated_electricity_purchased_kwh`, але
читається з іншого регістра. Залишений у конфіг-картках для
крос-верифікації.

#### `total_energy_charged_kwh`

- Переклад: **Загальна енергія заряду УЗЕ** / *Total energy charged*
- Modbus: 40472, INT64, gain 0.01 → kWh
- Суть: скільки сумарно "залито" у батарею за весь час. Бере участь у
балансі споживання навантаження (`applyApplianceConsumptionRule`).

#### `total_energy_discharged_kwh`

- Переклад: **Загальна енергія розряду УЗЕ** / *Total energy discharged*
- Modbus: 40476, UINT64, gain 0.01 → kWh
- Суть: скільки сумарно "віддано" з батареї за весь час. Аналогічно
бере участь у балансі споживання.

## 3. Як ці поля використовуються


| Місце на UI                                       | Метрики, що рендеряться                                                                                                                                                                                                                                                                                                                                  |
| ------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Картки поточного стану (`cards`)                  | `pv_energy_yield_day_kwh`, `power_supply_from_grid_day_kwh`, `energy_charged_day_kwh`, `energy_discharged_day_kwh`, `power_consumption_day_kwh`, `electricity_sold_day_kwh`, `electricity_purchased_day_kwh`, `total_energy_charged_kwh`, `total_energy_discharged_kwh`, `load_power_kw`, `active_pv_power_kw`, `active_ess_power_kw`, `grid_connected_active_power_kw`, `soc_percent`, усі `accumulated_*_kwh`, `total_power_supply_from_grid_kwh` |
| Сьогоднішні лічильники (`TodayCountersNarrative`) | `pv_energy_yield_day_kwh`, `power_consumption_day_kwh`, `electricity_purchased_day_kwh`, `electricity_sold_day_kwh`, `power_supply_from_grid_day_kwh`, `energy_charged_day_kwh`, `energy_discharged_day_kwh`                                                                                                                                             |
| Графік потужностей (`power_chart`)                | `active_pv_power_kw`, `load_power_kw`, `grid_connected_active_power_kw`                                                                                                                                                                                                                                                                                  |
| Графік енергії (`energy_chart`)                   | `accumulated_electricity_purchased_kwh`, `total_energy_discharged_kwh`, `accumulated_pv_energy_yield_kwh`, `accumulated_electricity_sold_kwh`, `total_energy_charged_kwh`, `accumulated_power_consumption_kwh`                                                                                                                                           |
| `EnergySummary` (місяць / рік)                    | `accumulated_pv_energy_yield_kwh`, `accumulated_electricity_sold_kwh`, `accumulated_electricity_purchased_kwh`, `accumulated_power_consumption_kwh`, `total_energy_charged_kwh`, `total_energy_discharged_kwh`                                                                                                                                           |


## 4. Похідні величини (без свого Modbus-регістра)

Не зчитуються напряму, а обчислюються із полів вище:

- **Виробіток СЕС за період** = дельта `accumulated_pv_energy_yield_kwh`.
- **Заряд / розряд УЗЕ за період** = дельти `total_energy_charged_kwh` та
`total_energy_discharged_kwh`.
- **Взяли з мережі / Віддали в мережу за період** = дельти
`accumulated_electricity_purchased_kwh` та
`accumulated_electricity_sold_kwh`.
- **Споживання навантаження за період** = дельта
`accumulated_power_consumption_kwh`. Якщо інвертор повертає 0 —
застосовується правило `applyApplianceConsumptionRule`:
  ```text
  consumption = pv_yield + discharged + purchased − charged − sold
  ```
  Узгоджено реалізовано і на бекенді (`internal/api/queries.go`), і на
  фронтенді (`web/src/dashboard/transforms/buckets.ts`,
  `web/src/dashboard/transforms/summary.ts`).

## 5. Особливості накопичувальних лічильників

- Усі `INT64`/`UINT64` з gain `0.01`: лічильник у регістрі — це
ціле число `× 100`, тому мінімальний крок — 0.01 кВт·год.
- Семантично всі вони мають бути монотонно зростаючими. Якщо в межах
періоду спостерігається падіння (rollback) — це ознака
програмного скидання чи самокалібрування інвертора. Бекенд у такому
випадку повертає 0 для відповідного totals-поля; це навмисна
поведінка, щоб не показувати "правдоподібні", але насправді
сфабриковані суми.
- Існує окремий запобіжник `MIN_RELIABLE_DATA_AT`
(`web/src/dashboard/config.ts`): seed/end часи Energy Summary
кламп­ляться так, щоб не зачіпати "до-розгортувальні" точки, де
історичні значення можуть бути спотворені бекфілом.

