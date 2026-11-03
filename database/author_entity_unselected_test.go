package database

import (
	"context"
	"strings"
	"testing"

	"gopds-api/internal/authorentity"
	"gopds-api/internal/authornorm"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The credits under review the build reads for the same-name join and the
// particle rule (E4k).

// unselectedSpec is an author credit whose selection is not "selected", with
// the proposal its selection rests on.
type unselectedSpec struct {
	state, reason, class, kind string
	flags                      []string
	family, given              string
	additional                 []string
	// sourceFamily is the file's last name field when it is not family.
	sourceFamily string
}

// unselectedCredit writes the credit of the snapshot whose fingerprint is
// fill, its proposal and its selection.
func (f *authorSchemaFixture) unselectedCredit(snapshot int64, fill byte, s *unselectedSpec) int64 {
	f.t.Helper()
	fp := fingerprint(32, fill)
	display := strings.Join(strings.Fields(strings.Join(append(append([]string{s.given}, s.additional...),
		s.family), " ")), " ")
	sourceFamily := s.family
	if s.sourceFamily != "" {
		sourceFamily, display = s.sourceFamily, s.sourceFamily
	}
	credit := f.returningID(`INSERT INTO book_contributor_credit
		(snapshot_id, role, position, source_first_name, source_middle_name, source_last_name, source_display_name,
		 source_fingerprint)
		VALUES (?, 'author', ?, ?, ?, ?, ?, ?) RETURNING id`, snapshot, int(fill), s.given, strings.Join(s.additional, " "),
		sourceFamily, display, fp)
	result := f.returningID(`INSERT INTO contributor_normalization_result
		(source_fingerprint, normalization_key, extractor_version, normalizer_version, result_schema_version,
		 method, kind, status, decision_class, family_name, given_name, additional_names,
		 display_name, sort_name, search_key, script, quality_flags)
		VALUES (?, ?, 'extractor-v1', 'normalizer-v1', 'result-v1', 'structured', ?, 'unresolved',
		 ?, nullif(?, ''), nullif(?, ''), coalesce(?::text[], '{}'), ?, ?, ?, 'Cyrl', ?) RETURNING id`,
		fp, keyFor(string(fp), "extractor-v1", "normalizer-v1"), s.kind, s.class, s.family, s.given,
		pg.Array(s.additional), display, display, authornorm.SearchKey(display), pg.Array(s.flags))
	var reason any
	if s.reason != "" {
		reason = s.reason
	}
	f.exec(selectionInsert, credit, fp, s.state, result, nil, nil, nil, reason, nil)
	return credit
}

func TestTheBuildReadsTheCreditsUnderReviewWithTheirProposal(t *testing.T) {
	f := entitySchema(t)
	_, snap := f.entityBook("Судья Ди")
	gulik := f.unselectedCredit(snap, 0xd1, &unselectedSpec{state: "review", class: "particle", kind: "person",
		flags: []string{"particle"}, family: "ван Гулик", given: "Роберт"})
	unsplit := f.unselectedCredit(snap, 0xd2, &unselectedSpec{state: "review", class: "single_field",
		kind: "person", flags: []string{"unstructured"}, sourceFamily: "Фома Аквинский"})
	pair := f.unselectedCredit(snap, 0xd3, &unselectedSpec{state: "review", class: "collective_candidate",
		kind: "collective", flags: []string{"collective_marker"}, sourceFamily: "Анна и Сергей Литвиновы"})
	refused := f.unselectedCredit(snap, 0xd4, &unselectedSpec{state: "unresolved", reason: "llm_refused",
		class: "initials", kind: "person", flags: []string{"initials"}, family: "Беловинский", given: "Л. В."})

	in, err := LoadAuthorEntityInput(context.Background(), f.tx)
	require.NoError(t, err)
	got := map[int64]authorentity.Unselected{}
	for _, u := range in.Unselected {
		got[u.ID] = u
	}
	require.Contains(t, got, gulik)
	assert.Equal(t, "particle", got[gulik].Class)
	assert.Equal(t, []string{"particle"}, got[gulik].Flags)
	assert.Equal(t, "ван Гулик", got[gulik].Family)
	assert.Equal(t, "Роберт", got[gulik].SourceGiven)
	require.Contains(t, got, unsplit)
	assert.Equal(t, "", got[unsplit].Family)
	assert.Equal(t, "Фома Аквинский", got[unsplit].SourceFamily)
	assert.NotContains(t, got, pair, "a collective proposal")
	assert.NotContains(t, got, refused, "the model's refusal stays out")
	for _, c := range in.Credits {
		assert.NotEqual(t, gulik, c.ID, "a credit under review is no selected credit")
	}
}

// A credit under review of a name a selected credit carries is linked to
// its entity: the read model reads the fingerprint row.
func TestACreditUnderReviewIsLinkedToTheEntityOfItsName(t *testing.T) {
	f := entitySchema(t)
	_, snap := f.entityBook("Рассвет")
	_, other := f.entityBook("Закат")
	selected := f.entityCredit(snap, 0xd5, "Смирнов", "Роман")
	review := f.unselectedCredit(other, 0xd6, &unselectedSpec{state: "review", class: "case_defect",
		kind: "person", flags: []string{"all_caps"}, family: "СМИРНОВ", given: "РОМАН"})
	p, _, skipped := f.pass()
	require.Empty(t, skipped)
	require.Equal(t, p.EntityOf(selected), p.EntityOf(review))
	var rule, entity string
	_, err := f.tx.QueryOne(pg.Scan(&rule, &entity), `SELECT rule, entity_id::text FROM author_entity_assignment
		WHERE source_fingerprint = ?`, fingerprint(32, 0xd6))
	require.NoError(t, err)
	assert.Equal(t, authorentity.RuleSameName, rule)
	assert.Equal(t, p.EntityOf(selected).String(), entity)
}
