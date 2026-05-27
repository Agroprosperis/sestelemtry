// Package dam exposes the synchronous "pull one day's DAM XLS, parse,
// upsert" pipeline as a single function so both the daily collector
// daemon (cmd/dam-collector) and the on-demand API refresh handler
// (POST /api/v1/dam-prices/refresh) can call it without copy-pasting
// the wiring between internal/oree (download + parse) and
// internal/storage (upsert).
package dam

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nesh/sestelemetry/internal/oree"
	"github.com/nesh/sestelemetry/internal/storage"
)

// FetchAndStore downloads the OREE DAM XLS for `deliveryDate`+`zone`,
// parses the 24 hourly rows out of it, and upserts them into
// `market_dam_prices`. Returns the number of rows actually written
// (0 means the parser produced no rows, which only happens on a
// malformed sheet — DownloadDAM would have failed first).
//
// `attempts` and `backoff` are passed through to oree.DownloadDAM:
// the daemon uses the configured retry budget (default 5 × 5min) so
// a flaky publication window self-heals; the API refresh handler
// passes (1, 0) so an operator who clicks the button gets the OREE
// error message back immediately instead of waiting 25 minutes for
// a fixed retry loop to give up.
//
// On a successful round-trip the resulting URL is stored in each
// row's `source_url` column for forensics ("where exactly did this
// price come from").
func FetchAndStore(
	ctx context.Context,
	log *slog.Logger,
	client *oree.Client,
	pool *pgxpool.Pool,
	deliveryDate time.Time,
	zone int,
	attempts int,
	backoff time.Duration,
) (int, error) {
	if client == nil {
		return 0, fmt.Errorf("dam: nil oree client")
	}
	if pool == nil {
		return 0, fmt.Errorf("dam: nil pool")
	}
	body, url, err := client.DownloadDAM(ctx, deliveryDate, zone, attempts, backoff)
	if err != nil {
		return 0, fmt.Errorf("dam: download: %w", err)
	}
	rows, err := oree.ParseDAMSheet(body)
	if err != nil {
		return 0, fmt.Errorf("dam: parse: %w", err)
	}
	storeRows := make([]storage.DAMRow, 0, len(rows))
	for _, r := range rows {
		storeRows = append(storeRows, storage.DAMRow{
			DeliveryDate: deliveryDate,
			Hour:         r.Hour,
			Zone:         zone,
			Price:        r.Price,
			SaleVol:      r.SaleVol,
			BuyVol:       r.BuyVol,
			DeclSaleVol:  r.DeclSaleVol,
			DeclBuyVol:   r.DeclBuyVol,
			SourceURL:    url,
		})
	}
	if err := storage.UpsertDAMRows(ctx, pool, storeRows); err != nil {
		return 0, fmt.Errorf("dam: upsert: %w", err)
	}
	if log != nil {
		log.Info("dam_fetch_ok",
			"delivery_date", deliveryDate.Format("2006-01-02"),
			"zone", zone,
			"rows", len(storeRows),
			"url", url,
		)
	}
	return len(storeRows), nil
}
