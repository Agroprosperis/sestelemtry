-- Day-Ahead Market (RDN) hourly prices and volumes per delivery date and trading zone.
-- Mirrored programmatically by the dam-collector via storage.InitDAMSchema.
CREATE TABLE IF NOT EXISTS market_dam_prices (
    delivery_date                date     NOT NULL,
    hour                         smallint NOT NULL CHECK (hour BETWEEN 1 AND 24),
    zone                         smallint NOT NULL,
    price_uah_per_mwh            double precision,
    sale_volume_mwh              double precision,
    purchase_volume_mwh          double precision,
    declared_sale_volume_mwh     double precision,
    declared_purchase_volume_mwh double precision,
    source_url                   text NOT NULL,
    fetched_at                   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (delivery_date, hour, zone)
);

CREATE INDEX IF NOT EXISTS market_dam_prices_date_idx
    ON market_dam_prices (delivery_date DESC);
