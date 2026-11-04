-- Exchange rates for the economics currency switch, read from the ERP
-- «apbank» service. Mirror of storage.InitFxRatesSchema, which creates
-- these tables idempotently at API startup — apply manually only when
-- running migrations by hand.

-- Where the ERP rates service is, edited on the «Курси валют ERP» page:
-- one row. The password has its own column and is never returned by the
-- API, only whether one is stored.
CREATE TABLE IF NOT EXISTS fx_settings (
    id         smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    settings   jsonb NOT NULL,
    password   text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- One rate per civil day (Europe/Kyiv) and currency, UAH per unit.
-- fetched_at tells a day's final rate (fetched at or after its noon)
-- from today's running one.
CREATE TABLE IF NOT EXISTS fx_rates (
    day          date NOT NULL,
    currency     text NOT NULL,
    uah_per_unit double precision NOT NULL,
    fetched_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (day, currency)
);
