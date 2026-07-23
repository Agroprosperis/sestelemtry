package api

import (
	"context"
	"net/http"
	"strconv"
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

// PlantInventoryChange is one field change event for the history API.
type PlantInventoryChange struct {
	At         time.Time `json:"at"`
	From       *float64  `json:"from"`
	To         *float64  `json:"to"`
	PollReason string    `json:"poll_reason,omitempty"`
}

// PlantInventoryHistoryResponse is GET /api/v1/plant-inventory/history.
type PlantInventoryHistoryResponse struct {
	OrganizationID string                           `json:"organization_id"`
	Changes        map[string][]PlantInventoryChange `json:"changes"`
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

func plantInventoryHistoryResponse(orgID string, diffs map[string][]inventory.FieldChange) PlantInventoryHistoryResponse {
	changes := make(map[string][]PlantInventoryChange, len(diffs))
	for key, events := range diffs {
		if len(events) == 0 {
			changes[key] = []PlantInventoryChange{}
			continue
		}
		out := make([]PlantInventoryChange, len(events))
		for i, e := range events {
			out[i] = PlantInventoryChange{
				At:         e.At.UTC(),
				From:       e.From,
				To:         e.To,
				PollReason: e.PollReason,
			}
		}
		changes[key] = out
	}
	return PlantInventoryHistoryResponse{
		OrganizationID: orgID,
		Changes:        changes,
	}
}

func (s *Store) LatestPlantInventory(ctx context.Context, organizationID string) (PlantInventoryResponse, bool, error) {
	// Coalesce over recent snapshots so night-time zeros / failed reads
	// don't blank out the passport values: each field shows the most
	// recent real reading.
	snaps, err := storage.ListPlantInventorySnapshots(ctx, s.pool, organizationID, 0)
	if err != nil {
		return PlantInventoryResponse{}, false, err
	}
	snap, ok := inventory.CoalesceLatest(snaps)
	if !ok {
		return PlantInventoryResponse{}, false, nil
	}
	return plantInventoryResponse(snap), true, nil
}

func (s *Store) PlantInventoryHistory(ctx context.Context, organizationID string, limit int) (PlantInventoryHistoryResponse, error) {
	snaps, err := storage.ListPlantInventorySnapshots(ctx, s.pool, organizationID, limit)
	if err != nil {
		return PlantInventoryHistoryResponse{}, err
	}
	return plantInventoryHistoryResponse(organizationID, inventory.DiffHistory(snaps)), nil
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

// GET /api/v1/plant-inventory/history?organization_id=&limit=
func (h *Handlers) plantInventoryHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	orgID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if orgID == "" {
		http.Error(w, "organization_id is required", http.StatusBadRequest)
		return
	}
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			http.Error(w, "limit must be a non-negative integer", http.StatusBadRequest)
			return
		}
		limit = n
	}
	resp, err := h.store.PlantInventoryHistory(r.Context(), orgID, limit)
	if err != nil {
		h.log.Error("api_plant_inventory_history", "organization_id", orgID, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
