package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nesh/sestelemetry/internal/auth"
)

// accessKind says how a route establishes who is calling.
type accessKind int

const (
	// accessSession: a signed-in user, checked against the rules.
	accessSession accessKind = iota
	// accessPublic: no session (health probes, login, logout).
	accessPublic
	// accessEdge: the handler checks the site's Bearer token itself.
	accessEdge
)

// routeAccess says who may call a route. Rules apply to session routes
// and are keyed by HTTP method; the "" rule covers every method without
// its own entry, so a handler still answers 405 for the methods it
// doesn't serve.
type routeAccess struct {
	kind accessKind
	// pendingOK keeps a session route open to a user whose password has
	// to be changed first; every other route refuses them.
	pendingOK bool
	rules     map[string]accessRule
}

type accessRule struct {
	// anyOf lists the permissions that each suffice; empty means any
	// signed-in user.
	anyOf []auth.Permission
	// orgParam names the query parameter carrying the organization the
	// request touches. Empty means the permission on any organization
	// will do — list handlers then filter their own output.
	orgParam string
	// global restricts the route to administrators of every
	// organization.
	global bool
	// capWindow bounds the telemetry window to maxDayWindow unless the
	// caller holds analytics.full on the organization.
	capWindow bool
}

func (a routeAccess) rule(method string) accessRule {
	if r, ok := a.rules[method]; ok {
		return r
	}
	return a.rules[""]
}

var (
	publicRoute    = routeAccess{kind: accessPublic}
	edgeRoute      = routeAccess{kind: accessEdge}
	signedIn       = need(accessRule{})
	passwordChange = routeAccess{kind: accessSession, pendingOK: true, rules: map[string]accessRule{"": {}}}
	globalOnly     = need(accessRule{global: true})
)

func need(r accessRule) routeAccess {
	return routeAccess{kind: accessSession, rules: map[string]accessRule{"": r}}
}

// on returns a copy of a with a stricter rule for one method.
func (a routeAccess) on(method string, r accessRule) routeAccess {
	rules := make(map[string]accessRule, len(a.rules)+1)
	for k, v := range a.rules {
		rules[k] = v
	}
	rules[method] = r
	a.rules = rules
	return a
}

func onOrg(perms ...auth.Permission) accessRule {
	return accessRule{anyOf: perms, orgParam: "organization_id"}
}

func onSite(perms ...auth.Permission) accessRule {
	return accessRule{anyOf: perms, orgParam: "site_id"}
}

func onAnyOrg(perms ...auth.Permission) accessRule {
	return accessRule{anyOf: perms}
}

func dayWindow(r accessRule) accessRule {
	r.capWindow = true
	return r
}

type route struct {
	pattern string
	handler http.HandlerFunc
	access  routeAccess
}

// routes is the single table Router registers from, so a route can't
// exist without an access rule.
func (h *Handlers) routes() []route {
	var (
		day      = onOrg(auth.PermAnalyticsDay)
		econRead = onOrg(auth.PermEconomicsRead)
		econEdit = onOrg(auth.PermEconomicsWrite)
		service  = onOrg(auth.PermService)
		control  = onSite(auth.PermControl)
		anyAdmin = need(onAnyOrg(auth.PermService))
	)
	return []route{
		{"/healthz", h.healthz, publicRoute},
		{"/readyz", h.readyz, publicRoute},
		{"/api/v1/auth/login", h.authLogin, publicRoute},
		{"/api/v1/auth/logout", h.authLogout, publicRoute},
		{"/api/v1/auth/me", h.authMe, passwordChange},
		{"/api/v1/auth/password", h.authPassword, passwordChange},
		{"/api/v1/users", h.users, globalOnly},
		{"/api/v1/dashboard-config", h.dashboardConfig, signedIn},
		{"/api/v1/organizations", h.organizationsList, signedIn},
		{"/api/v1/current", h.current, need(day)},
		{"/api/v1/timeseries", h.timeseries, need(dayWindow(day))},
		{"/api/v1/samples", h.samples, need(service)},
		{"/api/v1/registers", h.registers, anyAdmin},
		{"/api/v1/energy-summary", h.energySummary, need(dayWindow(day))},
		{"/api/v1/energy-flow-hourly", h.energyFlowHourly, need(service)},
		{"/api/v1/pv-plan-summary", h.pvPlanSummary, need(onOrg(auth.PermAnalyticsFull, auth.PermEconomicsRead))},
		// DAM prices are public OREE market data shared by every
		// organization; the day chart overlays them.
		{"/api/v1/dam-prices", h.damPrices, signedIn},
		{"/api/v1/dam-prices/refresh", h.damPricesRefresh, need(onAnyOrg(auth.PermEconomicsWrite))},
		{"/api/v1/dam-prices/refresh-range", h.damPricesRefreshRange, need(onAnyOrg(auth.PermEconomicsWrite))},
		{"/api/v1/fusionsolar/import", h.fusionSolarImport, need(service)},
		{"/api/v1/fusionsolar/config", h.fusionSolarConfig, anyAdmin},
		{"/api/v1/askoe/import", h.askoeImport, need(service)},
		{"/api/v1/weather-forecast", h.weatherForecast, need(day)},
		{"/api/v1/plant-inventory/history", h.plantInventoryHistory, need(service)},
		{"/api/v1/plant-inventory", h.plantInventory, need(service)},
		{"/api/v1/organization-tariffs", h.organizationTariffs, need(econRead).on(http.MethodPut, econEdit)},
		{"/api/v1/organization-tariff-schedule", h.organizationTariffSchedule,
			need(econRead).on(http.MethodPut, econEdit).on(http.MethodDelete, econEdit)},
		// Site-wide SMTP and default recipients apply to every
		// organization.
		{"/api/v1/alert-settings", h.alertSettings, globalOnly},
		// Without organization_id the test goes to the default list,
		// which only an admin of every organization may use.
		{"/api/v1/alert-settings/test-email", h.alertSettingsTestEmail, need(service)},
		{"/api/v1/organization-alert-settings", h.organizationAlertSettings,
			anyAdmin.on(http.MethodPut, service)},
		{"/api/v1/economics/daily", h.economicsDaily, need(econRead)},
		{"/api/v1/economics/monthly", h.economicsMonthly, need(econRead)},
		{"/api/v1/economics/annual", h.economicsAnnual, need(econRead)},
		{"/api/v1/economics/portfolio", h.economicsPortfolio, need(onAnyOrg(auth.PermEconomicsRead))},
		{"/api/v1/economics/recompute", h.economicsRecompute, need(econEdit)},
		{"/api/v1/economics/data-range", h.economicsDataRange, need(econRead)},
		{"/api/v1/uze-plan", h.uzePlan, need(day)},
		{"/api/v1/edge/batch", h.edgeBatch, edgeRoute},
		{"/api/v1/edge/heartbeat", h.edgeHeartbeat, edgeRoute},
		{"/api/v1/edge/manifest", h.edgeManifest, edgeRoute},
		{"/api/v1/edge/manifest/publish", h.edgeManifestPublish, need(control)},
		{"/api/v1/edge/manifest/publish-manual", h.edgeManifestPublishManual, need(control)},
		{"/api/v1/edge/sites", h.edgeSites, need(onAnyOrg(auth.PermControl))},
		{"/api/v1/edge/load-plan", h.edgeLoadPlan, need(control)},
		{"/api/v1/edge/plan/preview", h.edgePlanPreview, need(control)},
		{"/api/v1/edge/manifests", h.edgeManifestJournal, need(control)},
		{"/api/v1/edge/settings", h.edgeSettings,
			need(control).on(http.MethodPut, onSite(auth.PermTechnicalWrite))},
		{"/api/v1/edge/status", h.edgeStatus, need(control)},
		// The top bar's IOT2050 chips poll this on the analytics page too.
		{"/api/v1/edge/fleet", h.edgeFleet, need(onAnyOrg(auth.PermAnalyticsDay, auth.PermControl))},
		{"/swagger", h.swaggerUI, anyAdmin},
		{"/swagger/", h.swaggerUI, anyAdmin},
		{"/swagger/openapi.yaml", h.swaggerSpec, anyAdmin},
	}
}

func (h *Handlers) Router() http.Handler {
	mux := http.NewServeMux()
	access := make(map[string]routeAccess)
	for _, rt := range h.routes() {
		mux.HandleFunc(rt.pattern, rt.handler)
		access[rt.pattern] = rt.access
	}
	handler := h.withGzip(mux)
	if h.auth != nil {
		handler = h.withAuth(mux, access, handler)
	}
	return h.withSecurityHeaders(h.withCORS(handler))
}

// withAuth enforces the route table. It sits inside withCORS, so a
// preflight OPTIONS is answered before a session is asked for.
func (h *Handlers) withAuth(mux *http.ServeMux, access map[string]routeAccess, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pattern := mux.Handler(r)
		acc, known := access[pattern]
		switch {
		case !known || acc.kind == accessEdge:
			// Unknown paths get the mux's 404; the edge uplink checks
			// its Bearer token itself.
			next.ServeHTTP(w, r)
			return
		case isUnsafeMethod(r.Method) && strings.TrimSpace(r.Header.Get(csrfHeader)) == "":
			http.Error(w, "forbidden: missing "+csrfHeader+" header", http.StatusForbidden)
			return
		case acc.kind == accessPublic:
			next.ServeHTTP(w, r)
			return
		}
		p, err := h.auth.authenticate(w, r)
		if err != nil {
			h.log.Error("api_auth_session", "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if p == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if p.MustChangePassword && !acc.pendingOK {
			http.Error(w, "forbidden: change the password first", http.StatusForbidden)
			return
		}
		if msg, ok := h.authorize(p, acc.rule(r.Method), r); !ok {
			http.Error(w, msg, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), p)))
	})
}

// authorize applies rule to r on behalf of p; the string explains a
// refusal.
func (h *Handlers) authorize(p *auth.Principal, rule accessRule, r *http.Request) (string, bool) {
	if rule.global {
		return "forbidden: administrators of every organization only", p.IsGlobalAdmin()
	}
	if len(rule.anyOf) == 0 {
		return "", true
	}
	if rule.orgParam == "" {
		for _, perm := range rule.anyOf {
			if p.CanSome(perm) {
				return "", true
			}
		}
		return "forbidden", false
	}
	org := strings.TrimSpace(r.URL.Query().Get(rule.orgParam))
	if org == "" {
		// The handler answers 400 without a target; only a grant on
		// every organization gets that far.
		for _, perm := range rule.anyOf {
			if p.CanAll(perm) {
				return "", true
			}
		}
		return "forbidden", false
	}
	if !h.canOnOrg(p, org, rule.anyOf...) {
		return "forbidden", false
	}
	if rule.capWindow && !h.canOnOrg(p, org, auth.PermAnalyticsFull) && telemetryWindow(r) > maxDayWindow {
		return fmt.Sprintf("forbidden: this role reads at most %s of telemetry per request", maxDayWindow), false
	}
	return "", true
}

// canOnOrg reports whether p holds any of perms on org. Edge shadow
// telemetry ("<site>" + EDGE_ORG_SUFFIX) is covered by the site's grant.
func (h *Handlers) canOnOrg(p *auth.Principal, org string, perms ...auth.Permission) bool {
	site, shadow := h.edgeShadowSite(org)
	for _, perm := range perms {
		if p.Can(perm, org) || (shadow && p.Can(perm, site)) {
			return true
		}
	}
	return false
}

// edgeShadowSite maps a shadow organization back to its edge site. Only
// sites with an edge token have shadow telemetry, so "<org>-edge" of
// any other organization maps to nothing.
func (h *Handlers) edgeShadowSite(org string) (string, bool) {
	if h.edge == nil || h.edge.OrgSuffix == "" {
		return "", false
	}
	site, ok := strings.CutSuffix(org, h.edge.OrgSuffix)
	if !ok || site == "" {
		return "", false
	}
	_, isEdge := h.edge.Tokens[site]
	return site, isEdge
}

// visibleTo reports whether a list entry for org may be shown to the
// request's principal. Without SetAuth everything is visible.
func (h *Handlers) visibleTo(r *http.Request, org string, perms ...auth.Permission) bool {
	if h.auth == nil {
		return true
	}
	return h.canOnOrg(principalFrom(r.Context()), org, perms...)
}

// telemetryWindow is the span a telemetry request asks for, with the
// store's defaults for an omitted bound (the last 24 h, up to now).
// Unparseable bounds count as zero: the handler rejects them itself.
func telemetryWindow(r *http.Request) time.Duration {
	from, to, _, _, err := parseRange(r)
	if err != nil {
		return 0
	}
	now := time.Now()
	if to.IsZero() {
		to = now
	}
	if from.IsZero() {
		from = now.Add(-24 * time.Hour)
	}
	return to.Sub(from)
}

func isUnsafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}
