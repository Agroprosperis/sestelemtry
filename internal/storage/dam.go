package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DAMRow is one hourly Day-Ahead Market record for a given delivery date and trading zone.
// Numeric fields are pointers because the source XLSX may omit values for certain hours
// (e.g. low-activity hours where volume is reported but price is missing).
type DAMRow struct {
	DeliveryDate time.Time
	Hour         int
	Zone         int
	Price        *float64
	SaleVol      *float64
	BuyVol       *float64
	DeclSaleVol  *float64
	DeclBuyVol   *float64
	SourceURL    string
}

// InitDAMSchema creates the market_dam_prices table and supporting index (idempotent).
func InitDAMSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS market_dam_prices (
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
		)`,
		`CREATE INDEX IF NOT EXISTS market_dam_prices_date_idx
			ON market_dam_prices (delivery_date DESC)`,
	}
	for _, s := range stmts {
		if _, err := pool.Exec(ctx, s); err != nil {
			return fmt.Errorf("storage: exec dam schema: %w", err)
		}
	}
	return nil
}

// UpsertDAMRows inserts/updates DAM rows by (delivery_date, hour, zone). Safe to call
// repeatedly for the same date — late corrections from OREE will overwrite prior values.
func UpsertDAMRows(ctx context.Context, pool *pgxpool.Pool, rows []DAMRow) error {
	if len(rows) == 0 {
		return nil
	}
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	const stmt = `
		INSERT INTO market_dam_prices (
			delivery_date, hour, zone,
			price_uah_per_mwh,
			sale_volume_mwh, purchase_volume_mwh,
			declared_sale_volume_mwh, declared_purchase_volume_mwh,
			source_url, fetched_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
		ON CONFLICT (delivery_date, hour, zone) DO UPDATE SET
			price_uah_per_mwh            = EXCLUDED.price_uah_per_mwh,
			sale_volume_mwh              = EXCLUDED.sale_volume_mwh,
			purchase_volume_mwh          = EXCLUDED.purchase_volume_mwh,
			declared_sale_volume_mwh     = EXCLUDED.declared_sale_volume_mwh,
			declared_purchase_volume_mwh = EXCLUDED.declared_purchase_volume_mwh,
			source_url                   = EXCLUDED.source_url,
			fetched_at                   = now()
	`
	batch := &pgx.Batch{}
	for _, r := range rows {
		batch.Queue(stmt,
			r.DeliveryDate, r.Hour, r.Zone,
			r.Price,
			r.SaleVol, r.BuyVol,
			r.DeclSaleVol, r.DeclBuyVol,
			r.SourceURL,
		)
	}
	br := pool.SendBatch(ctx, batch)
	defer br.Close()
	for i := 0; i < len(rows); i++ {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("storage: upsert dam row %d: %w", i, err)
		}
	}
	return nil
}

// CountDAMRowsForDate returns how many rows already exist for the given delivery_date+zone.
// Used by the catch-up logic to decide whether a fresh fetch is needed on startup.
func CountDAMRowsForDate(ctx context.Context, pool *pgxpool.Pool, deliveryDate time.Time, zone int) (int, error) {
	if pool == nil {
		return 0, fmt.Errorf("storage: nil pool")
	}
	var n int
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM market_dam_prices WHERE delivery_date = $1 AND zone = $2`,
		deliveryDate, zone,
	).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}
