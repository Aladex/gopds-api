package llmres

import (
	"bytes"
	"testing"
)

// The canonical answer is the tuple (form, order, roles[], case_fix[]) after
// sorting by index; two models agree iff the canonical forms are byte-equal.
// There is no partial agreement.

func TestCanonicalBytesSortsByIndex(t *testing.T) {
	a := Reply{Form: FormPerson, Order: OrderFamilyFirst, Roles: []RoleAssignment{
		{2, RoleGiven}, {0, RoleFamily}, {1, RoleSeparator}}, CaseFix: []CaseFix{}}
	b := Reply{Form: FormPerson, Order: OrderFamilyFirst, Roles: []RoleAssignment{
		{0, RoleFamily}, {1, RoleSeparator}, {2, RoleGiven}}, CaseFix: []CaseFix{}}
	if !Agree(&a, &b) {
		t.Fatal("role order in the wire reply must not affect agreement")
	}
	got := string(a.CanonicalBytes())
	want := `{"form":"person","order":"family_first","roles":[{"i":0,"role":"family"},{"i":1,"role":"separator"},` +
		`{"i":2,"role":"given"}],"case_fix":[]}`
	if got != want {
		t.Fatalf("CanonicalBytes = %s, want %s", got, want)
	}
}

func TestAgreeIsByteExact(t *testing.T) {
	base := Reply{Form: FormPerson, Order: OrderFamilyFirst, Roles: []RoleAssignment{
		{0, RoleFamily}, {1, RoleSeparator}, {2, RoleGiven}}, CaseFix: []CaseFix{}}

	differentRole := base
	differentRole.Roles = []RoleAssignment{{0, RoleGiven}, {1, RoleSeparator}, {2, RoleGiven}}
	if Agree(&base, &differentRole) {
		t.Error("same form with different roles must not agree")
	}

	differentForm := base
	differentForm.Form = FormCollective
	if Agree(&base, &differentForm) {
		t.Error("different form must not agree")
	}

	differentOrder := base
	differentOrder.Order = OrderGivenFirst
	if Agree(&base, &differentOrder) {
		t.Error("different order must not agree")
	}

	withCaseFix := base
	withCaseFix.CaseFix = []CaseFix{{I: 0, To: CaseFixCapitalize}}
	if Agree(&base, &withCaseFix) {
		t.Error("different case_fix must not agree")
	}

	reorderedFix := base
	reorderedFix.CaseFix = []CaseFix{{I: 2, To: CaseFixCapitalize}, {I: 0, To: CaseFixCapitalize}}
	sortedFix := base
	sortedFix.CaseFix = []CaseFix{{I: 0, To: CaseFixCapitalize}, {I: 2, To: CaseFixCapitalize}}
	if !Agree(&reorderedFix, &sortedFix) {
		t.Error("case_fix order on the wire must not affect agreement")
	}
}

func TestCanonicalBytesDeterministic(t *testing.T) {
	rep := Reply{Form: FormPerson, Order: OrderGivenFirst, Roles: []RoleAssignment{
		{1, RoleFamily}, {0, RoleGiven}}, CaseFix: []CaseFix{{I: 1, To: CaseFixCapitalizeParts}}}
	if !bytes.Equal(rep.CanonicalBytes(), rep.CanonicalBytes()) {
		t.Fatal("CanonicalBytes must be deterministic")
	}
}
