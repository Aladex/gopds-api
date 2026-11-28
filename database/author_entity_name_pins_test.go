package database

import (
	"context"
	"testing"

	"gopds-api/internal/authornorm"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shownCredit is entityCredit with the display name the result gives,
// written as display rather than given name first.
func (f *authorSchemaFixture) shownCredit(snapshot int64, fill byte, family, given, display string) int64 {
	f.t.Helper()
	fp := fingerprint(32, fill)
	credit := f.returningID(`INSERT INTO book_contributor_credit
		(snapshot_id, role, position, source_first_name, source_last_name, source_display_name, source_fingerprint)
		VALUES (?, 'author', 0, ?, ?, ?, ?) RETURNING id`, snapshot, given, family, display, fp)
	result := f.returningID(`INSERT INTO contributor_normalization_result
		(source_fingerprint, normalization_key, extractor_version, normalizer_version, result_schema_version,
		 method, kind, status, decision_class, family_name, given_name, additional_names,
		 display_name, sort_name, search_key, script)
		VALUES (?, ?, 'extractor-v1', 'normalizer-v1', 'result-v1', 'rules', 'person', 'normalized',
		 'structured_person', ?, ?, '{}', ?, ?, ?, 'Cyrl') RETURNING id`,
		fp, keyFor(string(fp), "extractor-v1", "normalizer-v1"), family, given, display, display,
		authornorm.SearchKey(display))
	f.exec(selectionInsert, credit, fp, "selected", result, "automatic", nil, "4", nil, nil)
	return credit
}

// namePinCatalog is one entity written two ways: «Урсула Ле Гуин» on three
// books and «Ле Гуин Урсула» on one, and another entity beside it.
func namePinCatalog(t *testing.T) (f *authorSchemaFixture, entity, other uuid.UUID) {
	f = entitySchema(t)
	var short, full int64
	for _, title := range []string{"Волшебник Земноморья", "Гробницы Атуана", "На последнем берегу"} {
		_, s := f.entityBook(title)
		short = f.entityCredit(s, 0xa1, "Ле Гуин", "Урсула")
	}
	_, s := f.entityBook("Левая рука тьмы")
	full = f.shownCredit(s, 0xa2, "Ле Гуин", "Урсула", "Ле Гуин Урсула")
	_, s = f.entityBook("Пикник на обочине")
	lone := f.entityCredit(s, 0xa3, "Стругацкий", "Аркадий")
	p, _, _ := f.pass()
	entity, other = p.EntityOf(short), p.EntityOf(lone)
	require.Equal(t, entity, p.EntityOf(full), "the two forms are one entity")
	require.NotEqual(t, entity, other)
	return f, entity, other
}

// entityNameRow is what the pin writes of an entity.
func (f *authorSchemaFixture) entityNameRow(id uuid.UUID) (name, sortName, key string, pinned bool) {
	f.t.Helper()
	row := struct {
		DisplayName string `pg:"display_name"`
		SortName    string `pg:"sort_name"`
		SearchKey   string `pg:"search_key"`
		NamePinned  bool   `pg:"name_pinned"`
	}{}
	_, err := f.tx.QueryOne(&row, `SELECT display_name, sort_name, search_key, name_pinned FROM author_entity WHERE id = ?`, id)
	require.NoError(f.t, err)
	return row.DisplayName, row.SortName, row.SearchKey, row.NamePinned
}

// An entity's names are the ones its credits are written with, the most used
// first; a pin chooses one of them, and the row takes its sort name and its
// search key as the build would; the pin is logged with its actor. An
// unpin hands the name back, logged too.
func TestAPinChoosesOneOfTheEntitysNames(t *testing.T) {
	f, entity, _ := namePinCatalog(t)
	ctx := context.Background()
	admin := f.user()

	names, err := ReadEntityNames(ctx, f.tx, entity)
	require.NoError(t, err)
	assert.False(t, names.NamePinned)
	require.Len(t, names.Forms, 2)
	assert.Equal(t, EntityNameForm{Name: "Урсула Ле Гуин", SortName: "Урсула Ле Гуин", Books: 3}, names.Forms[0])
	assert.Equal(t, "Ле Гуин Урсула", names.Forms[1].Name)
	assert.Equal(t, 1, names.Forms[1].Books)

	require.NoError(t, PinEntityNames(ctx, f.tx, []NamePin{{Entity: entity, Name: " Ле Гуин Урсула "}}, admin))
	name, sortName, key, pinned := f.entityNameRow(entity)
	assert.Equal(t, "Ле Гуин Урсула", name)
	assert.Equal(t, "Ле Гуин Урсула", sortName)
	assert.Equal(t, "ле гуин урсула", key)
	assert.True(t, pinned)
	names, err = ReadEntityNames(ctx, f.tx, entity)
	require.NoError(t, err)
	assert.True(t, names.NamePinned)
	require.NotNil(t, names.PinnedBy)
	assert.Equal(t, admin, *names.PinnedBy)
	assert.NotNil(t, names.PinnedAt)

	require.NoError(t, UnpinEntityNames(ctx, f.tx, []uuid.UUID{entity}, admin))
	_, _, _, pinned = f.entityNameRow(entity)
	assert.False(t, pinned)
	assert.Equal(t, 2, f.count(`SELECT count(*) FROM author_entity_name_pin_log WHERE entity_id = ? AND actor_user_id = ?`,
		entity, admin))
	assert.Equal(t, 1, f.count(`SELECT count(*) FROM author_entity_name_pin_log WHERE entity_id = ? AND action = 'unpin'
		AND display_name = 'Ле Гуин Урсула'`, entity))
}

// A pin is refused, and nothing is written, for a name no credit of the
// entity uses (another entity's included), an unknown or merged entity, an
// empty, oversized or repeated batch, and while a build is active; an unpin
// of a name that is not pinned is refused too.
func TestAPinRefusesWhatItCannotHold(t *testing.T) {
	f, entity, other := namePinCatalog(t)
	ctx := context.Background()
	admin := f.user()
	before := f.count(`SELECT count(*) FROM author_entity WHERE name_pinned`)

	pin := func(pins ...NamePin) error { return PinEntityNames(ctx, f.tx, pins, admin) }
	assert.ErrorIs(t, pin(NamePin{Entity: entity, Name: "Урсула Ле Гуинн"}), ErrNamePinName)
	assert.ErrorIs(t, pin(NamePin{Entity: entity, Name: "Аркадий Стругацкий"}), ErrNamePinName, "another entity's name")
	assert.ErrorIs(t, pin(NamePin{Entity: entity, Name: ""}), ErrNamePinName)
	// All or none: the good first pin is not written either.
	assert.ErrorIs(t, pin(NamePin{Entity: other, Name: "Аркадий Стругацкий"}, NamePin{Entity: entity, Name: "нет"}),
		ErrNamePinName)
	assert.ErrorIs(t, pin(NamePin{Entity: uuid.New(), Name: "Урсула Ле Гуин"}), ErrNamePinEntity)
	assert.ErrorIs(t, pin(), ErrNamePinBatch)
	assert.ErrorIs(t, pin(NamePin{Entity: entity, Name: "Урсула Ле Гуин"}, NamePin{Entity: entity, Name: "Урсула Ле Гуин"}),
		ErrNamePinBatch)
	many := make([]NamePin, NamePinMaxBatch+1)
	for i := range many {
		many[i] = NamePin{Entity: uuid.New(), Name: "x"}
	}
	assert.ErrorIs(t, pin(many...), ErrNamePinBatch)
	assert.ErrorIs(t, UnpinEntityNames(ctx, f.tx, []uuid.UUID{entity}, admin), ErrNamePinNotPinned)
	assert.ErrorIs(t, UnpinEntityNames(ctx, f.tx, []uuid.UUID{uuid.New()}, admin), ErrNamePinEntity)

	f.exec(`UPDATE author_entity SET merged_into = ? WHERE id = ?`, other, entity)
	assert.ErrorIs(t, pin(NamePin{Entity: entity, Name: "Урсула Ле Гуин"}), ErrNamePinEntity, "a merged entity")
	f.exec(`UPDATE author_entity SET merged_into = NULL WHERE id = ?`, entity)

	_, err := StartAuthorEntityBuild(ctx, f.tx, admin)
	require.NoError(t, err)
	assert.ErrorIs(t, pin(NamePin{Entity: entity, Name: "Урсула Ле Гуин"}), ErrAuthorEntityBuildActive)

	assert.Equal(t, before, f.count(`SELECT count(*) FROM author_entity WHERE name_pinned`))
	assert.Zero(t, f.count(`SELECT count(*) FROM author_entity_name_pin_log WHERE entity_id IN (?, ?)`, entity, other))
}

// A build pass keeps a pinned name, with its sort name and search key, and
// moves no credit; once unpinned, the next pass shows the name it computes.
func TestABuildKeepsAPinnedName(t *testing.T) {
	f, entity, other := namePinCatalog(t)
	ctx := context.Background()
	admin := f.user()
	computed, _, _, _ := f.entityNameRow(entity)
	require.Equal(t, "Урсула Ле Гуин", computed)

	require.NoError(t, PinEntityNames(ctx, f.tx, []NamePin{{Entity: entity, Name: "Ле Гуин Урсула"}}, admin))
	before := f.creditEntities()
	p, changes, skipped := f.pass()
	assert.Zero(t, changes, "a pass over a pinned name changes nothing")
	assert.Empty(t, skipped)
	assert.Equal(t, before, f.creditEntities())
	assert.Equal(t, entity, p.EntityOf(firstCreditOf(t, f, entity)))
	name, _, key, pinned := f.entityNameRow(entity)
	assert.Equal(t, "Ле Гуин Урсула", name)
	assert.Equal(t, "ле гуин урсула", key)
	assert.True(t, pinned)
	otherName, _, _, _ := f.entityNameRow(other)
	assert.Equal(t, "Аркадий Стругацкий", otherName)

	require.NoError(t, UnpinEntityNames(ctx, f.tx, []uuid.UUID{entity}, admin))
	f.pass()
	name, _, _, pinned = f.entityNameRow(entity)
	assert.Equal(t, computed, name)
	assert.False(t, pinned)
	assert.Equal(t, before, f.creditEntities())
}

// creditEntities is every fingerprint's entity, as the build stored it.
func (f *authorSchemaFixture) creditEntities() map[string]uuid.UUID {
	f.t.Helper()
	var rows []struct {
		FP       string    `pg:"fp"`
		Ordinal  int       `pg:"ordinal"`
		EntityID uuid.UUID `pg:"entity_id,type:uuid"`
	}
	_, err := f.tx.Query(&rows, `SELECT encode(source_fingerprint, 'hex') AS fp, ordinal, entity_id
		FROM author_entity_assignment`)
	require.NoError(f.t, err)
	out := map[string]uuid.UUID{}
	for _, r := range rows {
		out[r.FP] = r.EntityID
	}
	return out
}

// firstCreditOf is a credit the entity holds.
func firstCreditOf(t *testing.T, f *authorSchemaFixture, entity uuid.UUID) int64 {
	t.Helper()
	var id int64
	_, err := f.tx.QueryOne(pg.Scan(&id), `SELECT c.id FROM book_contributor_credit c
		JOIN author_entity_assignment a ON a.source_fingerprint = c.source_fingerprint
		WHERE a.entity_id = ? ORDER BY c.id LIMIT 1`, entity)
	require.NoError(t, err)
	return id
}
