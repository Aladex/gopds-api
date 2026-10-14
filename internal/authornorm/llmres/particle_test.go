package llmres

import (
	"testing"

	"gopds-api/internal/authornorm"
)

// The particle rule (V8): a token of a name particle stays in a name
// component — the family name, or the component of the FB2 field it came
// from. The particle role keeps it out of every component, so a person reply
// that gives a particle word that role, or moves it into another component,
// is invalid.

// ursulaFields is the resolve pilot run 16 input whose agreed reply lost the
// particle from the family name.
func ursulaFields() []FieldValue {
	return []FieldValue{{FieldFirst, "Úrsula"}, {FieldMiddle, "K."}, {FieldLast, "Le Güín"}}
}

func assembleParticle(t *testing.T, fields []FieldValue, rep *Reply) (Outcome, error) {
	t.Helper()
	in := inputOf(t, fields...)
	return Assemble(&in, rep, testExtractorVersion, testConfigVersion, authornorm.DecisionClass("llm.test"))
}

func TestParticleRoleOnALastFieldParticleIsInvalid(t *testing.T) {
	// The agreed reply of run 16: both models labeled "Le" a particle, the
	// assembly applied family_name "Güín".
	_, err := assembleParticle(t, ursulaFields(), &Reply{Form: FormPerson, CaseFix: []CaseFix{}, Roles: []RoleAssignment{
		role(0, RoleGiven), role(1, RoleAdditional), role(2, RoleParticle), role(3, RoleFamily)}})
	checkViolation(t, err, validatorV8)
}

func TestParticleKeptInTheFamilyAssembles(t *testing.T) {
	out, err := assembleParticle(t, ursulaFields(), &Reply{Form: FormPerson, CaseFix: []CaseFix{}, Roles: []RoleAssignment{
		role(0, RoleGiven), role(1, RoleAdditional), role(2, RoleFamily), role(3, RoleFamily)}})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	r := out.Result
	if r.FamilyName != "Le Güín" || r.GivenName != "Úrsula" || r.AdditionalNames != "K." {
		t.Errorf("fields = %q/%q/%q", r.GivenName, r.AdditionalNames, r.FamilyName)
	}
	if r.SortName != "Le Güín, Úrsula K." {
		t.Errorf("sort = %q", r.SortName)
	}
}

func TestParticleMovedOutOfTheLastFieldIsInvalid(t *testing.T) {
	// "Le" moved from the family into the additional names.
	_, err := assembleParticle(t, ursulaFields(), &Reply{Form: FormPerson, CaseFix: []CaseFix{}, Roles: []RoleAssignment{
		role(0, RoleGiven), role(1, RoleAdditional), role(2, RoleAdditional), role(3, RoleFamily)}})
	checkViolation(t, err, validatorV8)
}

func TestParticleOfAMiddleField(t *testing.T) {
	// The resolve pilot run 15 input: a lowercase particle in the middle
	// field. Its own component or the family keeps it; the particle role
	// drops it.
	fields := []FieldValue{{FieldFirst, "И"}, {FieldMiddle, "де"}, {FieldLast, "Рок-Казбеков"}}

	_, err := assembleParticle(t, fields, &Reply{Form: FormPerson, CaseFix: []CaseFix{}, Roles: []RoleAssignment{
		role(0, RoleGiven), role(1, RoleParticle), role(2, RoleFamily)}})
	checkViolation(t, err, validatorV8)

	out, err := assembleParticle(t, fields, &Reply{Form: FormPerson, CaseFix: []CaseFix{}, Roles: []RoleAssignment{
		role(0, RoleGiven), role(1, RoleFamily), role(2, RoleFamily)}})
	if err != nil {
		t.Fatalf("particle joined to the family: %v", err)
	}
	if out.Result.FamilyName != "де Рок-Казбеков" {
		t.Errorf("family = %q", out.Result.FamilyName)
	}

	out, err = assembleParticle(t, fields, &Reply{Form: FormPerson, CaseFix: []CaseFix{}, Roles: []RoleAssignment{
		role(0, RoleGiven), role(1, RoleAdditional), role(2, RoleFamily)}})
	if err != nil {
		t.Fatalf("particle kept in its own field: %v", err)
	}
	if out.Result.AdditionalNames != "де" {
		t.Errorf("additional = %q", out.Result.AdditionalNames)
	}
}

func TestParticleWordAsAGivenNameInTheFirstField(t *testing.T) {
	// "Ben" is on the particle list; in the first field it is a given name.
	_, err := assembleParticle(t, []FieldValue{{FieldFirst, "Ben"}, {FieldLast, "Jonson"}},
		&Reply{Form: FormPerson, CaseFix: []CaseFix{}, Roles: []RoleAssignment{
			role(0, RoleGiven), role(1, RoleFamily)}})
	if err != nil {
		t.Fatalf("a first-field particle word kept as given: %v", err)
	}
}

func TestParticleGluedToTheFamily(t *testing.T) {
	// A glued particle ("d'Artagnan") is one token; it is the family itself.
	fields := []FieldValue{{FieldFirst, "Charles"}, {FieldLast, "d'Artagnan"}}
	_, err := assembleParticle(t, fields, &Reply{Form: FormPerson, CaseFix: []CaseFix{}, Roles: []RoleAssignment{
		role(0, RoleGiven), role(1, RoleParticle)}})
	checkViolation(t, err, validatorV8)
	if _, err := assembleParticle(t, fields, &Reply{Form: FormPerson, CaseFix: []CaseFix{}, Roles: []RoleAssignment{
		role(0, RoleGiven), role(1, RoleFamily)}}); err != nil {
		t.Fatalf("glued particle as family: %v", err)
	}
}

func TestParticleRuleSparesNonPersonForms(t *testing.T) {
	// A collective has no name components to drop a particle from; the
	// display keeps every token.
	if _, err := assembleParticle(t, ursulaFields(), &Reply{Form: FormCollective, CaseFix: []CaseFix{}, Roles: []RoleAssignment{
		role(0, RoleSingleName), role(1, RoleSingleName), role(2, RoleSingleName), role(3, RoleSingleName)}}); err != nil {
		t.Fatalf("collective: %v", err)
	}
}

func TestLoneParticleWordIsNoParticle(t *testing.T) {
	// The extractor marks a standalone particle word only next to another
	// word: a one-word name "Ван" is a name.
	if _, err := assembleParticle(t, []FieldValue{{FieldLast, "Ван"}},
		&Reply{Form: FormPerson, CaseFix: []CaseFix{}, Roles: []RoleAssignment{role(0, RoleSingleName)}}); err != nil {
		t.Fatalf("one-word name: %v", err)
	}
}
