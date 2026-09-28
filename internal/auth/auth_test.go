package auth

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRolePermissions(t *testing.T) {
	cases := []struct {
		role Role
		want []Permission
		deny []Permission
	}{
		{RoleEngineer, []Permission{PermAnalyticsDay},
			[]Permission{PermAnalyticsFull, PermEconomicsRead, PermControl, PermTechnicalWrite, PermService}},
		{RoleControlEngineer, []Permission{PermAnalyticsDay, PermControl, PermTechnicalWrite},
			[]Permission{PermAnalyticsFull, PermEconomicsRead, PermEconomicsWrite, PermService}},
		{RoleEconomist, []Permission{PermEconomicsRead, PermEconomicsWrite},
			[]Permission{PermAnalyticsDay, PermControl, PermTechnicalWrite, PermService}},
		{RoleAdmin, []Permission{PermAnalyticsDay, PermAnalyticsFull, PermEconomicsRead, PermEconomicsWrite,
			PermControl, PermTechnicalWrite, PermService}, nil},
	}
	for _, c := range cases {
		for _, p := range c.want {
			if !c.role.Has(p) {
				t.Errorf("%s lacks %s", c.role, p)
			}
		}
		for _, p := range c.deny {
			if c.role.Has(p) {
				t.Errorf("%s must not have %s", c.role, p)
			}
		}
	}
	if Role("operator").Valid() || Role("operator").Has(PermAnalyticsDay) {
		t.Fatal("unknown role must be invalid and carry nothing")
	}
}

func TestPrincipalScopes(t *testing.T) {
	p := &Principal{Grants: []Grant{
		{Role: RoleEconomist, OrganizationID: "ze"},
		{Role: RoleEngineer, OrganizationID: "pe"},
		{Role: RoleEngineer},
	}}
	if !p.Can(PermEconomicsRead, "ze") || p.Can(PermEconomicsRead, "pe") {
		t.Fatal("economist grant must apply to ze only")
	}
	if !p.Can(PermAnalyticsDay, "ab") {
		t.Fatal("engineer on every organization must reach an unlisted one")
	}
	if !p.CanAll(PermAnalyticsDay) || p.CanAll(PermEconomicsRead) {
		t.Fatal("CanAll must follow the all-organization grants only")
	}
	if !p.CanSome(PermEconomicsWrite) || p.CanSome(PermControl) {
		t.Fatal("CanSome must follow any grant")
	}
	if p.IsGlobalAdmin() {
		t.Fatal("no admin grant, not a global admin")
	}
	if p.Can(PermAnalyticsDay, "") {
		t.Fatal("an empty organization is never covered")
	}
	got := p.PermissionsOn("ze")
	want := []Permission{PermAnalyticsDay, PermEconomicsRead, PermEconomicsWrite}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PermissionsOn(ze) = %v, want %v", got, want)
	}

	scoped := &Principal{Grants: []Grant{{Role: RoleAdmin, OrganizationID: "ze"}}}
	if scoped.IsGlobalAdmin() || !scoped.Can(PermService, "ze") || scoped.Covers("pe") {
		t.Fatal("an admin of one organization is not a global admin")
	}
	global := &Principal{Grants: []Grant{{Role: RoleAdmin}}}
	if !global.IsGlobalAdmin() || !global.Covers("anything") {
		t.Fatal("admin on every organization is a global admin")
	}

	var none *Principal
	if none.Can(PermAnalyticsDay, "ze") || none.CanSome(PermAnalyticsDay) || none.IsGlobalAdmin() || none.PermissionsOn("ze") != nil {
		t.Fatal("a nil principal holds nothing")
	}
}

func TestNormalizeGrants(t *testing.T) {
	got := NormalizeGrants([]Grant{
		{Role: RoleEngineer, OrganizationID: "pe"},
		{Role: RoleEconomist, OrganizationID: "ze"},
		{Role: RoleEngineer, OrganizationID: "ab"},
		{Role: RoleEconomist},
		{Role: RoleEngineer, OrganizationID: "pe"},
		{Role: RoleAdmin, OrganizationID: "ze"},
	})
	want := []Grant{
		{Role: RoleAdmin, OrganizationID: "ze"},
		{Role: RoleEconomist},
		{Role: RoleEngineer, OrganizationID: "ab"},
		{Role: RoleEngineer, OrganizationID: "pe"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeGrants = %v, want %v", got, want)
	}
	if got := NormalizeGrants(nil); len(got) != 0 {
		t.Fatalf("NormalizeGrants(nil) = %v, want none", got)
	}
}

func TestSameGrants(t *testing.T) {
	a := []Grant{{Role: RoleAdmin}, {Role: RoleEngineer, OrganizationID: "ze"}}
	cases := []struct {
		name string
		b    []Grant
		want bool
	}{
		{"reordered", []Grant{{Role: RoleEngineer, OrganizationID: "ze"}, {Role: RoleAdmin}}, true},
		{"duplicated", []Grant{{Role: RoleAdmin}, {Role: RoleAdmin}, {Role: RoleEngineer, OrganizationID: "ze"}}, true},
		{"subset", []Grant{{Role: RoleAdmin}}, false},
		{"superset", append([]Grant{{Role: RoleEconomist}}, a...), false},
		{"other organization", []Grant{{Role: RoleAdmin}, {Role: RoleEngineer, OrganizationID: "pe"}}, false},
	}
	for _, c := range cases {
		if got := SameGrants(a, c.b); got != c.want {
			t.Errorf("%s: SameGrants = %v, want %v", c.name, got, c.want)
		}
	}
	if !SameGrants(nil, []Grant{}) {
		t.Fatal("two empty lists are the same")
	}
}

func TestPasswordPolicy(t *testing.T) {
	if err := ValidatePassword("short"); !errors.Is(err, ErrPasswordTooShort) {
		t.Fatalf("short password: err = %v", err)
	}
	// Ten Cyrillic letters are 20 bytes: the minimum counts characters.
	if err := ValidatePassword("абвгґдеєжз"); err != nil {
		t.Fatalf("10-character Cyrillic password rejected: %v", err)
	}
	if err := ValidatePassword(strings.Repeat("я", 37)); !errors.Is(err, ErrPasswordTooLong) {
		t.Fatalf("74-byte password: err = %v", err)
	}

	hash, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(hash, "correct horse battery") {
		t.Fatal("matching password rejected")
	}
	if CheckPassword(hash, "correct horse batterY") {
		t.Fatal("wrong password accepted")
	}
	if CheckPassword("", "correct horse battery") {
		t.Fatal("an empty hash must never match")
	}

	// bcrypt reads 72 bytes; anything appended to a 72-byte password must
	// not be accepted as the same password.
	longest := strings.Repeat("a", 72)
	longHash, err := HashPassword(longest)
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(longHash, longest) || CheckPassword(longHash, longest+"x") {
		t.Fatal("a password past 72 bytes must not match its prefix")
	}
}

func TestThrottle(t *testing.T) {
	th := NewThrottle(3, 10*time.Minute)
	start := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if blocked, _ := th.Blocked("a@x", start); blocked {
			t.Fatalf("blocked after %d failures", i)
		}
		th.Fail("a@x", start.Add(time.Duration(i)*time.Minute))
	}
	blocked, wait := th.Blocked("a@x", start.Add(4*time.Minute))
	if !blocked || wait != 6*time.Minute {
		t.Fatalf("Blocked = %v, %v; want true, 6m", blocked, wait)
	}
	if blocked, _ := th.Blocked("b@x", start); blocked {
		t.Fatal("another account must not be affected")
	}
	if blocked, _ := th.Blocked("a@x", start.Add(10*time.Minute)); blocked {
		t.Fatal("lockout must end with the window")
	}

	th.Fail("c@x", start)
	th.Fail("c@x", start)
	th.Reset("c@x")
	th.Fail("c@x", start)
	if blocked, _ := th.Blocked("c@x", start); blocked {
		t.Fatal("Reset must clear earlier failures")
	}
}

func TestSessionToken(t *testing.T) {
	a, digestA := NewSessionToken()
	b, _ := NewSessionToken()
	if a == b || len(a) < 20 {
		t.Fatalf("tokens must be long and unique: %q %q", a, b)
	}
	if len(digestA) != 32 || !reflect.DeepEqual(digestA, TokenDigest(a)) {
		t.Fatal("digest must be the sha256 of the token")
	}
}
