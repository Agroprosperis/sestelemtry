package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// GeneratorPassportRow is what the station passport shows of a site's
// generator (ems-spec generator handoff §5.4). Pointers stay nil for what
// the site never reported.
type GeneratorPassportRow struct {
	DeviceHost   string
	LastSampleAt *time.Time
	RatedKw      *float64
	MeterKwh     *float64
	MeterAt      *time.Time
	MeterAlive   *float64
	SwVersion    *float64
	LastRunAt    *time.Time
}

// genRunningKw is the generator power above which it counts as running
// (the supply-mode threshold of handoff §4.1).
const genRunningKw = 5

// LatestGeneratorPassport reads a site's generator passport. ok is false
// when the site has no Encombi samples at all.
//
// The rated power is non-zero only while the generator runs, and a
// generator stands idle for weeks: rather than scanning back through the
// telemetry for it, the last run is found from the economics hours, and
// the rating and the exact last running moment are read inside that hour.
func LatestGeneratorPassport(ctx context.Context, pool *pgxpool.Pool, organizationID string) (GeneratorPassportRow, bool, error) {
	var out GeneratorPassportRow
	if pool == nil {
		return out, false, fmt.Errorf("storage: nil pool")
	}
	rows, err := pool.Query(ctx, `
		SELECT m.metric_key, s.value, s.time, COALESCE(s.labels->>'device_host', '')
		FROM unnest($2::text[]) AS m(metric_key)
		CROSS JOIN LATERAL (
			SELECT value, time, labels
			FROM telemetry_samples
			WHERE organization_id = $1 AND metric_key = m.metric_key
			ORDER BY time DESC
			LIMIT 1
		) s
	`, organizationID, []string{
		"grid_breakers_on_bits", "accumulated_gen_energy_kwh", "gen_meter_alive",
		"encombi_sw_version", "gen_rated_online_kw", "active_gen_power_kw",
	})
	if err != nil {
		return out, false, fmt.Errorf("storage: query generator passport: %w", err)
	}
	found := false
	var currentRated, currentPower *float64
	var powerAt time.Time
	for rows.Next() {
		var key, host string
		var value float64
		var at time.Time
		if err := rows.Scan(&key, &value, &at, &host); err != nil {
			rows.Close()
			return out, false, fmt.Errorf("storage: scan generator passport: %w", err)
		}
		found = true
		v, t := value, at
		switch key {
		case "grid_breakers_on_bits":
			out.LastSampleAt = &t
			out.DeviceHost = host
		case "accumulated_gen_energy_kwh":
			out.MeterKwh, out.MeterAt = &v, &t
		case "gen_meter_alive":
			out.MeterAlive = &v
		case "encombi_sw_version":
			out.SwVersion = &v
		case "gen_rated_online_kw":
			currentRated = &v
		case "active_gen_power_kw":
			currentPower, powerAt = &v, t
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, false, fmt.Errorf("storage: iterate generator passport: %w", err)
	}
	if !found {
		return out, false, nil
	}
	if currentRated != nil && *currentRated > 0 {
		out.RatedKw = currentRated
	}
	if currentPower != nil && *currentPower > genRunningKw {
		out.LastRunAt = &powerAt
	}
	if out.RatedKw != nil && out.LastRunAt != nil {
		return out, true, nil
	}

	var lastHour time.Time
	err = pool.QueryRow(ctx, `
		SELECT hour_start FROM economics_hourly
		WHERE organization_id = $1 AND gen_kwh > 0
		ORDER BY hour_start DESC
		LIMIT 1
	`, organizationID).Scan(&lastHour)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, true, nil
	}
	if err != nil {
		return out, false, fmt.Errorf("storage: query generator last hour: %w", err)
	}
	// The run may reach into the hour after the economics' last one.
	from, to := lastHour, lastHour.Add(2*time.Hour)
	if out.LastRunAt == nil {
		var at time.Time
		err := pool.QueryRow(ctx, `
			SELECT time FROM telemetry_samples
			WHERE organization_id = $1 AND metric_key = 'active_gen_power_kw' AND value > $2
				AND time >= $3 AND time < $4
			ORDER BY time DESC
			LIMIT 1
		`, organizationID, float64(genRunningKw), from, to).Scan(&at)
		switch {
		case err == nil:
			out.LastRunAt = &at
		case !errors.Is(err, pgx.ErrNoRows):
			return out, false, fmt.Errorf("storage: query generator last run: %w", err)
		}
	}
	if out.RatedKw == nil {
		var kw float64
		err := pool.QueryRow(ctx, `
			SELECT value FROM telemetry_samples
			WHERE organization_id = $1 AND metric_key = 'gen_rated_online_kw' AND value > 0
				AND time >= $2 AND time < $3
			ORDER BY time DESC
			LIMIT 1
		`, organizationID, from, to).Scan(&kw)
		switch {
		case err == nil:
			out.RatedKw = &kw
		case !errors.Is(err, pgx.ErrNoRows):
			return out, false, fmt.Errorf("storage: query generator rating: %w", err)
		}
	}
	return out, true, nil
}
