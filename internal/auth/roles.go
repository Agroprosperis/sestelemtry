// Package auth is the dashboard's access model: the four roles, the
// permissions each one carries, and the principal a signed-in request
// acts as. It knows nothing about HTTP or the database — internal/api
// maps routes onto these permissions and internal/storage persists the
// grants.
package auth

import "sort"

// Role is what an administrator assigns to a user on one organization,
// several, or all of them.
type Role string

const (
	RoleAdmin           Role = "admin"
	RoleEconomist       Role = "economist"
	RoleEngineer        Role = "engineer"
	RoleControlEngineer Role = "control_engineer"
)

// Roles lists every assignable role in the order the user-management
// page shows them.
var Roles = []Role{RoleAdmin, RoleEconomist, RoleEngineer, RoleControlEngineer}

// Permission is the unit routes are checked against. Roles are fixed
// bundles of permissions; users only ever receive roles.
type Permission string

const (
	// PermAnalyticsDay reads the day energy chart: live cards, telemetry
	// windows up to a day and a bit, the day plan, weather.
	PermAnalyticsDay Permission = "analytics.day"
	// PermAnalyticsFull lifts the telemetry window cap, which is what
	// the month and year presets of the analytics page need.
	PermAnalyticsFull  Permission = "analytics.full"
	PermEconomicsRead  Permission = "economics.read"
	PermEconomicsWrite Permission = "economics.write"
	// PermControl is the «Керування» mode: edge status, the УЗЕ planner,
	// modes and manifest publication.
	PermControl Permission = "control"
	// PermTechnicalWrite changes the per-site limits the planner and
	// every manifest obey (SOC policy, kW limits, PV rating).
	PermTechnicalWrite Permission = "technical.write"
	// PermService covers an organization's admin tools: exports,
	// archive imports, the station passport and alert routing.
	PermService Permission = "service"
)

var rolePermissions = map[Role][]Permission{
	RoleEngineer:        {PermAnalyticsDay},
	RoleControlEngineer: {PermAnalyticsDay, PermControl, PermTechnicalWrite},
	RoleEconomist:       {PermEconomicsRead, PermEconomicsWrite},
	RoleAdmin: {
		PermAnalyticsDay, PermAnalyticsFull,
		PermEconomicsRead, PermEconomicsWrite,
		PermControl, PermTechnicalWrite,
		PermService,
	},
}

// Valid reports whether r is one of the assignable roles.
func (r Role) Valid() bool {
	_, ok := rolePermissions[r]
	return ok
}

// Has reports whether r carries p.
func (r Role) Has(p Permission) bool {
	for _, have := range rolePermissions[r] {
		if have == p {
			return true
		}
	}
	return false
}

// Grant assigns a role on one organization, or on every organization
// when OrganizationID is empty — including organizations added to the
// config after the grant was made.
type Grant struct {
	Role           Role
	OrganizationID string
}

// AllOrganizations reports whether g is not tied to one organization.
func (g Grant) AllOrganizations() bool { return g.OrganizationID == "" }

// Covers reports whether g applies to org.
func (g Grant) Covers(org string) bool {
	return g.AllOrganizations() || g.OrganizationID == org
}

// Principal is the signed-in user a request acts as. A nil *Principal
// is allowed everywhere and holds no permissions.
type Principal struct {
	UserID int64
	Email  string
	Name   string
	Grants []Grant
}

// Can reports whether some grant covering org carries perm.
func (p *Principal) Can(perm Permission, org string) bool {
	if p == nil || org == "" {
		return false
	}
	for _, g := range p.Grants {
		if g.Covers(org) && g.Role.Has(perm) {
			return true
		}
	}
	return false
}

// CanAll reports whether p holds perm on every organization.
func (p *Principal) CanAll(perm Permission) bool {
	if p == nil {
		return false
	}
	for _, g := range p.Grants {
		if g.AllOrganizations() && g.Role.Has(perm) {
			return true
		}
	}
	return false
}

// CanSome reports whether p holds perm on at least one organization.
func (p *Principal) CanSome(perm Permission) bool {
	if p == nil {
		return false
	}
	for _, g := range p.Grants {
		if g.Role.Has(perm) {
			return true
		}
	}
	return false
}

// IsGlobalAdmin reports whether p is an administrator of every
// organization — the only scope allowed to change what all of them
// share (site-wide alert delivery, user accounts).
func (p *Principal) IsGlobalAdmin() bool {
	if p == nil {
		return false
	}
	for _, g := range p.Grants {
		if g.AllOrganizations() && g.Role == RoleAdmin {
			return true
		}
	}
	return false
}

// Covers reports whether any grant of p reaches org.
func (p *Principal) Covers(org string) bool {
	if p == nil || org == "" {
		return false
	}
	for _, g := range p.Grants {
		if g.Covers(org) && g.Role.Valid() {
			return true
		}
	}
	return false
}

// PermissionsOn returns the sorted union of the permissions p holds on
// org; empty when no grant reaches it.
func (p *Principal) PermissionsOn(org string) []Permission {
	if p == nil || org == "" {
		return nil
	}
	seen := map[Permission]bool{}
	for _, g := range p.Grants {
		if !g.Covers(org) {
			continue
		}
		for _, perm := range rolePermissions[g.Role] {
			seen[perm] = true
		}
	}
	out := make([]Permission, 0, len(seen))
	for perm := range seen {
		out = append(out, perm)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
