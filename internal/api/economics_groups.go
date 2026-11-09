package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nesh/sestelemetry/internal/auth"
	"github.com/nesh/sestelemetry/internal/economics"
)

// OrganizationGroupInfo is an economic group (economics.Groups): one object
// of the economics pages made of several configured organizations.
type OrganizationGroupInfo struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Members []string `json:"members"`
}

// economicGroup is the group with id when every member is a configured
// organization; a group missing a member is not offered at all.
func (h *Handlers) economicGroup(id string) (economics.Group, bool) {
	g, ok := economics.GroupByID(id)
	if !ok {
		return economics.Group{}, false
	}
	for _, m := range g.Members {
		if !h.configuredOrganization(m.ID) {
			return economics.Group{}, false
		}
	}
	return g, true
}

func (h *Handlers) configuredOrganization(org string) bool {
	for _, o := range h.organizations {
		if o.ID == org {
			return true
		}
	}
	return false
}

// economicGroups lists the groups economicGroup offers.
func (h *Handlers) economicGroups() []economics.Group {
	var out []economics.Group
	for _, g := range economics.Groups {
		if _, ok := h.economicGroup(g.ID); ok {
			out = append(out, g)
		}
	}
	return out
}

func (h *Handlers) groupOfMember(org string) (economics.Group, bool) {
	for _, g := range h.economicGroups() {
		for _, m := range g.Members {
			if m.ID == org {
				return g, true
			}
		}
	}
	return economics.Group{}, false
}

// economicObjects lists what the economics pages report as objects, in
// config order: a group the caller may read stands, once, where its first
// member would, in place of every member.
func (h *Handlers) economicObjects(visible func(org string) bool) []OrganizationInfo {
	shown := map[string]bool{}
	var out []OrganizationInfo
	for _, org := range h.organizations {
		if g, ok := h.groupOfMember(org.ID); ok && visible(g.ID) {
			if !shown[g.ID] {
				shown[g.ID] = true
				out = append(out, OrganizationInfo{ID: g.ID, Name: g.Name, Location: org.Location})
			}
			continue
		}
		if visible(org.ID) {
			out = append(out, org)
		}
	}
	return out
}

// groupInfos lists the offered groups whose every member passes covers.
func (h *Handlers) groupInfos(covers func(org string) bool) []OrganizationGroupInfo {
	out := []OrganizationGroupInfo{}
	for _, g := range h.economicGroups() {
		all := true
		for _, m := range g.Members {
			all = all && covers(m.ID)
		}
		if all {
			out = append(out, OrganizationGroupInfo{ID: g.ID, Name: g.Name, Members: g.MemberIDs()})
		}
	}
	return out
}

// groupPermissions is what p may do on a group: what it may do on every
// member.
func groupPermissions(p *auth.Principal, g economics.Group) []auth.Permission {
	counts := map[auth.Permission]int{}
	for _, m := range g.Members {
		for _, perm := range p.PermissionsOn(m.ID) {
			counts[perm]++
		}
	}
	var out []auth.Permission
	for _, perm := range p.PermissionsOn(g.Members[0].ID) {
		if counts[perm] == len(g.Members) {
			out = append(out, perm)
		}
	}
	return out
}

// tariffVersions is org's tariff schedule; a group's combines its
// members' (groupTariffVersions).
func (h *Handlers) tariffVersions(ctx context.Context, org string) ([]TariffScheduleVersion, error) {
	if g, ok := h.economicGroup(org); ok {
		return h.groupTariffVersions(ctx, g)
	}
	return h.store.GetTariffScheduleVersions(ctx, org)
}

// groupTariffVersions is a group's tariff schedule on every day it changes:
// the first member in force sets the prices and every member in force
// adds its УЗЕ and its capital, the rule the economics engine combines the
// group's УЗЕ by. A version's capital is the cumulative investment, so the
// sums stay cumulative.
func (h *Handlers) groupTariffVersions(ctx context.Context, g economics.Group) ([]TariffScheduleVersion, error) {
	byMember := make(map[string][]TariffScheduleVersion, len(g.Members))
	dates := make(map[string][]string, len(g.Members))
	for _, m := range g.Members {
		vs, err := h.store.GetTariffScheduleVersions(ctx, m.ID)
		if err != nil {
			return nil, err
		}
		byMember[m.ID] = vs
		for _, v := range vs {
			dates[m.ID] = append(dates[m.ID], v.EffectiveFrom)
		}
	}
	out := []TariffScheduleVersion{}
	for _, day := range g.VersionDates(dates) {
		var inForce []OrgTariffs
		for _, id := range g.ActiveOn(day) {
			if t, ok := tariffVersionOn(byMember[id], day); ok {
				inForce = append(inForce, t)
			}
		}
		if len(inForce) > 0 {
			out = append(out, TariffScheduleVersion{EffectiveFrom: day, Tariffs: combineTariffs(inForce)})
		}
	}
	return out, nil
}

// tariffVersionOn is the version of vs (ascending) in force on day.
func tariffVersionOn(vs []TariffScheduleVersion, day string) (OrgTariffs, bool) {
	var out OrgTariffs
	found := false
	for _, v := range vs {
		if v.EffectiveFrom <= day {
			out, found = v.Tariffs, true
		}
	}
	return out, found
}

// combineTariffs is several members' tariffs as one object's: the first
// member's prices, the members' УЗЕ and capital added up. A pack without a
// nameplate power counts at ≈ 1C, as the economics engine reads it.
func combineTariffs(ts []OrgTariffs) OrgTariffs {
	out := ts[0]
	out.EssCapacityKwh, out.EssPowerLimitKw, out.CapexUah = 0, 0, 0
	for _, t := range ts {
		out.EssCapacityKwh += t.EssCapacityKwh
		power := t.EssPowerLimitKw
		if power <= 0 {
			power = t.EssCapacityKwh
		}
		out.EssPowerLimitKw += power
		out.CapexUah += t.CapexUah
	}
	return out
}

// orgTariffs is org's tariff form. A group's is its combined schedule's
// version in force today: a member's form can lag behind its schedule
// (de's still held the whole elevator after the split), so adding the
// forms would count Т1 twice. Only a group without a schedule combines
// the forms of the members in force today.
func (h *Handlers) orgTariffs(ctx context.Context, org string) (OrgTariffs, bool, error) {
	g, ok := h.economicGroup(org)
	if !ok {
		return h.store.GetOrgTariffs(ctx, org)
	}
	loc, err := loadLocation(chatTZ)
	if err != nil {
		loc = time.UTC
	}
	today := time.Now().In(loc).Format("2006-01-02")
	versions, err := h.groupTariffVersions(ctx, g)
	if err != nil {
		return OrgTariffs{}, false, err
	}
	if t, ok := tariffVersionOn(versions, today); ok {
		return t, true, nil
	}
	var inForce []OrgTariffs
	for _, id := range g.ActiveOn(today) {
		t, ok, err := h.store.GetOrgTariffs(ctx, id)
		if err != nil {
			return OrgTariffs{}, false, err
		}
		if ok {
			inForce = append(inForce, t)
		}
	}
	if len(inForce) == 0 {
		return OrgTariffs{}, false, nil
	}
	return combineTariffs(inForce), true, nil
}

// rejectGroupWrite answers 400 to a write aimed at a group: its tariffs
// are its members', edited one by one.
func rejectGroupWrite(w http.ResponseWriter, org string) bool {
	g, ok := economics.GroupByID(org)
	if !ok {
		return false
	}
	http.Error(w, fmt.Sprintf("%s is a group: edit the tariffs of %s", org, strings.Join(g.MemberIDs(), ", ")), http.StatusBadRequest)
	return true
}

// telemetryDataRange is the span org has telemetry for; a group's is the
// span any member has.
func (h *Handlers) telemetryDataRange(ctx context.Context, org string) (time.Time, time.Time, bool, error) {
	g, ok := h.economicGroup(org)
	if !ok {
		return h.store.TelemetryDataRange(ctx, org)
	}
	var from, to time.Time
	found := false
	for _, id := range g.MemberIDs() {
		a, b, ok, err := h.store.TelemetryDataRange(ctx, id)
		if err != nil {
			return time.Time{}, time.Time{}, false, err
		}
		if !ok {
			continue
		}
		if !found || a.Before(from) {
			from = a
		}
		if !found || b.After(to) {
			to = b
		}
		found = true
	}
	return from, to, found, nil
}
