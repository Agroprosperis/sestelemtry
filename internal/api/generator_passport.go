package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/nesh/sestelemetry/internal/storage"
)

// GeneratorPassportResponse is the body of GET /api/v1/generator: the
// station passport's «Генератор» block (ems-spec generator handoff §5.4).
// HasGenerator is false for a site without an Encombi; the rest is then
// empty. The fuel comes from the tariff schedule, which the page reads
// on its own.
type GeneratorPassportResponse struct {
	OrganizationID string     `json:"organization_id"`
	HasGenerator   bool       `json:"has_generator"`
	DeviceHost     string     `json:"device_host,omitempty"`
	LastSampleAt   *time.Time `json:"last_sample_at,omitempty"`
	RatedKw        *float64   `json:"rated_kw,omitempty"`
	MeterKwh       *float64   `json:"meter_kwh,omitempty"`
	MeterAt        *time.Time `json:"meter_at,omitempty"`
	MeterAlive     *bool      `json:"meter_alive,omitempty"`
	SwVersion      *float64   `json:"sw_version,omitempty"`
	LastRunAt      *time.Time `json:"last_run_at,omitempty"`
}

// generatorPassportSource is the optional store extension the passport
// endpoint reads.
type generatorPassportSource interface {
	GeneratorPassport(ctx context.Context, organizationID string) (storage.GeneratorPassportRow, bool, error)
}

// GeneratorPassport reads a site's generator passport.
func (s *Store) GeneratorPassport(ctx context.Context, organizationID string) (storage.GeneratorPassportRow, bool, error) {
	return storage.LatestGeneratorPassport(ctx, s.pool, organizationID)
}

// GET /api/v1/generator?organization_id=
func (h *Handlers) generatorPassport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	orgID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if orgID == "" {
		http.Error(w, "organization_id is required", http.StatusBadRequest)
		return
	}
	resp := GeneratorPassportResponse{OrganizationID: orgID}
	src, ok := h.store.(generatorPassportSource)
	if !ok {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	row, found, err := src.GeneratorPassport(r.Context(), orgID)
	if err != nil {
		h.log.Error("api_generator_passport", "organization_id", orgID, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if found {
		resp.HasGenerator = true
		resp.DeviceHost = row.DeviceHost
		resp.LastSampleAt = row.LastSampleAt
		resp.RatedKw = row.RatedKw
		resp.MeterKwh = row.MeterKwh
		resp.MeterAt = row.MeterAt
		resp.SwVersion = row.SwVersion
		resp.LastRunAt = row.LastRunAt
		if row.MeterAlive != nil {
			alive := *row.MeterAlive == 1
			resp.MeterAlive = &alive
		}
	}
	writeJSON(w, http.StatusOK, resp)
}
