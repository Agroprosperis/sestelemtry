package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/nesh/sestelemetry/internal/inventory"
	"github.com/nesh/sestelemetry/internal/storage"
)

// PlantInventoryResponse is the JSON body of GET /api/v1/plant-inventory.
type PlantInventoryResponse struct {
	OrganizationID         string         `json:"organization_id"`
	Time                   time.Time      `json:"time"`
	DeviceHost             string         `json:"device_host,omitempty"`
	PollReason             string         `json:"poll_reason"`
	PVRatedKw              *float64       `json:"pv_rated_kw"`
	ESSRatedKw             *float64       `json:"ess_rated_kw"`
	ESSRatedKwh            *float64       `json:"ess_rated_kwh"`
	ESSCount               *float64       `json:"ess_count"`
	PCSCount               *float64       `json:"pcs_count"`
	ESSSOHPct              *float64       `json:"ess_soh_pct"`
	ActivePowerControlMode *float64       `json:"active_power_control_mode"`
	QualityFlags           []string       `json:"quality_flags"`
	Raw                    map[string]any `json:"raw,omitempty"`
}

func plantInventoryResponse(snap inventory.Snapshot) PlantInventoryResponse {
	flags := snap.QualityFlags
	if flags == nil {
		flags = []string{}
	}
	return PlantInventoryResponse{
		OrganizationID:         snap.OrganizationID,
		Time:                   snap.Time.UTC(),
		DeviceHost:             snap.DeviceHost,
		PollReason:             snap.PollReason,
		PVRatedKw:              snap.PVRatedKw,
		ESSRatedKw:             snap.ESSRatedKw,
		ESSRatedKwh:            snap.ESSRatedKwh,
		ESSCount:               snap.ESSCount,
		PCSCount:               snap.PCSCount,
		ESSSOHPct:              snap.ESSSOHPct,
		ActivePowerControlMode: snap.ActivePowerControlMode,
		QualityFlags:           flags,
		Raw:                    snap.Raw,
	}
}

func (s *Store) LatestPlantInventory(ctx context.Context, organizationID string) (PlantInventoryResponse, bool, error) {
	snap, ok, err := storage.LatestPlantInventorySnapshot(ctx, s.pool, organizationID)
	if err != nil || !ok {
		return PlantInventoryResponse{}, ok, err
	}
	return plantInventoryResponse(snap), true, nil
}

// GET /api/v1/plant-inventory?organization_id=
func (h *Handlers) plantInventory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	orgID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if orgID == "" {
		http.Error(w, "organization_id is required", http.StatusBadRequest)
		return
	}
	resp, ok, err := h.store.LatestPlantInventory(r.Context(), orgID)
	if err != nil {
		h.log.Error("api_plant_inventory", "organization_id", orgID, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "no plant inventory snapshot", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
