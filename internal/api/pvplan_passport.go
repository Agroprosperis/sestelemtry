package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/nesh/sestelemetry/internal/pvplan"
)

// --- Station passport: /api/v1/pv-orientations ---

// pvPlanLoc is the zone the passport's "today" is counted in: the
// planner's, so the comparison and the plan it computes agree on which
// day "today" is.
func (h *Handlers) pvPlanLoc() *time.Location {
	if h.pvPlanner != nil {
		return h.pvPlanner.Location()
	}
	return time.UTC
}

// pvOrientations answers GET (read) and PUT (save) of a station's PV
// plan passport.
func (h *Handlers) pvOrientations(w http.ResponseWriter, r *http.Request) {
	orgID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if orgID == "" {
		http.Error(w, "organization_id is required", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		st, ok, err := h.store.PvPlanStation(r.Context(), orgID)
		if err != nil {
			h.log.Error("api_pv_orientations", "organization_id", orgID, "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		var station *pvplan.Station
		if ok {
			station = &st
		}
		writeJSON(w, http.StatusOK, h.pvStationResponse(r.Context(), orgID, station))
	case http.MethodPut:
		if h.pvPlanner == nil {
			http.Error(w, "pv plan planner not configured", http.StatusServiceUnavailable)
			return
		}
		var st pvplan.Station
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&st); err != nil {
			http.Error(w, "не вдалося прочитати паспорт: "+err.Error(), http.StatusBadRequest)
			return
		}
		st.OrganizationID = orgID
		if st.Source == "" {
			st.Source = pvplan.SourceN8N
		}
		saved, err := h.pvPlanner.SaveStation(r.Context(), st, principalLabel(r))
		if errors.Is(err, pvplan.ErrInvalid) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err != nil {
			h.log.Error("api_pv_orientations_save", "organization_id", orgID, "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		h.log.Info("api_pv_orientations_saved", "organization_id", orgID, "source", saved.Source, "orientations", len(saved.Orientations))
		writeJSON(w, http.StatusOK, h.pvStationResponse(r.Context(), orgID, &saved))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// principalLabel names the signed-in user for journals.
func principalLabel(r *http.Request) string {
	p := principalFrom(r.Context())
	if p == nil {
		return ""
	}
	if strings.TrimSpace(p.Name) != "" {
		return p.Name
	}
	return p.Email
}

func (h *Handlers) pvStationResponse(ctx context.Context, orgID string, st *pvplan.Station) PvStationResponse {
	resp := PvStationResponse{OrganizationID: orgID, Station: st}
	if h.pvPlanner != nil {
		if c, ok := h.pvPlanner.DefaultCoordinates(orgID); ok {
			resp.Defaults = &PvPlanCoordinates{Latitude: c.Latitude, Longitude: c.Longitude}
		}
		resp.HistoryJob = h.pvPlanner.HistoryStatusOf(orgID)
	}
	if st != nil {
		resp.PdcTotalKwp = st.PdcTotalKwp()
	}
	code, n8nOK := h.n8nCode(orgID)
	resp.N8NAvailable = n8nOK

	loc := h.pvPlanLoc()
	today := startOfCivilDay(time.Now(), loc)
	resp.Today.Date = today.Format("2006-01-02")
	if st != nil && st.Modelled() {
		if hours, _, err := h.store.PvPlanHours(ctx, orgID, civilDate(today), pvplan.DayAhead); err == nil && len(hours) > 0 {
			total := sumHours(hours)
			resp.Today.ModelKwh = &total
		}
	}
	if n8nOK {
		if hours, err := h.n8nDayHours(ctx, orgID, code, today, loc); err == nil && len(hours) > 0 {
			total := sumHours(hours)
			resp.Today.N8NKwh = &total
		}
	}
	return resp
}

func sumHours(hours map[int]float64) float64 {
	total := 0.0
	for _, v := range hours {
		total += v
	}
	return total
}

// pvOrientationsHistory answers GET /api/v1/pv-orientations/history.
func (h *Handlers) pvOrientationsHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	orgID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if orgID == "" {
		http.Error(w, "organization_id is required", http.StatusBadRequest)
		return
	}
	if h.pvPlanner == nil {
		http.Error(w, "pv plan planner not configured", http.StatusServiceUnavailable)
		return
	}
	entries, err := h.pvPlanner.History(r.Context(), orgID, 50)
	if err != nil {
		h.log.Error("api_pv_orientations_history", "organization_id", orgID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, PvStationHistoryResponse{OrganizationID: orgID, Entries: entries})
}

// pvPlanHistoryJob answers GET (status) and POST (start) of a station's
// plan-history rebuild from the Previous Runs archive. POST takes
// optional from/to (YYYY-MM-DD); without them the rebuild runs from the
// station's first telemetry day to yesterday.
func (h *Handlers) pvPlanHistoryJob(w http.ResponseWriter, r *http.Request) {
	orgID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if orgID == "" {
		http.Error(w, "organization_id is required", http.StatusBadRequest)
		return
	}
	if h.pvPlanner == nil {
		http.Error(w, "pv plan planner not configured", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.pvPlanner.HistoryStatusOf(orgID))
	case http.MethodPost:
		from := strings.TrimSpace(r.URL.Query().Get("from"))
		to := strings.TrimSpace(r.URL.Query().Get("to"))
		for _, d := range []string{from, to} {
			if d == "" {
				continue
			}
			if _, err := time.Parse("2006-01-02", d); err != nil {
				http.Error(w, "from/to must be YYYY-MM-DD", http.StatusBadRequest)
				return
			}
		}
		st, ok, err := h.store.PvPlanStation(r.Context(), orgID)
		if err != nil {
			h.log.Error("api_pv_plan_history", "organization_id", orgID, "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if !ok || !st.Modelled() {
			http.Error(w, "спершу внесіть орієнтації і ліміт інвертора", http.StatusBadRequest)
			return
		}
		status, err := h.pvPlanner.StartHistory(orgID, from, to)
		if err != nil && !pvplan.ErrHistoryRunning(err) {
			h.log.Error("api_pv_plan_history_start", "organization_id", orgID, "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusAccepted, status)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
