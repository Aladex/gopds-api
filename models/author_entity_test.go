package models

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Under the entity identity a name links to every entity it names, or —
// when one of them is not live — to none (design E, §6 item 5), and never to
// a legacy author. A name of no entity stays plain.
func TestWithEntitiesLinksAllOrNone(t *testing.T) {
	a, b, gone := uuid.New(), uuid.New(), uuid.New()
	legacy := int64(7)
	live := map[uuid.UUID]AuthorEntity{
		a: {ID: a, FullName: "Аркадий Стругацкий"},
		b: {ID: b, FullName: "Борис Стругацкий", Label: "фантастика"},
	}
	line := []AuthorDisplay{
		{Name: "Аркадий и Борис Стругацкие", LegacyAuthorID: &legacy, EntityIDs: []string{a.String(), b.String()}},
		{Name: "Аркадий Стругацкий", LegacyAuthorID: &legacy, EntityIDs: []string{a.String()}},
		{Name: "Стругацкие и другие", EntityIDs: []string{a.String(), gone.String()}},
		{Name: "Неизвестный", LegacyAuthorID: &legacy},
		{Name: "Испорченный", EntityIDs: []string{"not-a-uuid"}},
	}
	got := WithEntities(line, live)
	require.Len(t, got, len(line))
	assert.Equal(t, []AuthorEntity{live[a], live[b]}, got[0].Entities, "several people: each of them")
	assert.Equal(t, []AuthorEntity{live[a]}, got[1].Entities)
	assert.Nil(t, got[2].Entities, "one of them is gone: no link at all")
	assert.Nil(t, got[3].Entities)
	assert.Nil(t, got[4].Entities)
	for i := range got {
		assert.Nil(t, got[i].LegacyAuthorID, "no legacy link under entities")
		assert.Equal(t, line[i].Name, got[i].Name, "the line reads as the file wrote it")
	}
	assert.NotNil(t, line[0].LegacyAuthorID, "the input is left as it was")

	raw, err := json.Marshal(got[1])
	require.NoError(t, err)
	assert.JSONEq(t, `{"name":"Аркадий Стругацкий","entities":[{"id":"`+a.String()+`","full_name":"Аркадий Стругацкий"}]}`,
		string(raw))
	raw, err = json.Marshal(line[1])
	require.NoError(t, err)
	assert.JSONEq(t, `{"name":"Аркадий Стругацкий","legacy_author_id":7}`, string(raw),
		"the legacy line's JSON carries no entity")
}

// The namesake label follows the name wherever a surface writes plain text.
func TestAnEntityTitleCarriesItsLabel(t *testing.T) {
	assert.Equal(t, "Стивен Кинг · Ужасы", (&AuthorEntity{FullName: "Стивен Кинг", Label: "Ужасы"}).Title())
	assert.Equal(t, "Стивен Кинг", (&AuthorEntity{FullName: "Стивен Кинг"}).Title())
	assert.Equal(t, "Стивен Кинг · 682 книги", (&AuthorEntity{FullName: "Стивен Кинг", LabelBooks: 682}).Title())
	assert.Equal(t, "Стивен Кинг · 21 книга", (&AuthorEntity{FullName: "Стивен Кинг", LabelBooks: 21}).Title())
}

// An author suggestion of an entity carries the entity's UUID as its id;
// every other suggestion is written as it always was.
func TestAnEntitySuggestionIsIdentifiedByItsUUID(t *testing.T) {
	id := uuid.New()
	raw, err := json.Marshal(AutocompleteSuggestion{Value: "Стивен Кинг", Type: "author", EntityID: id, BooksCount: 3,
		Label: "Ужасы"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"value":"Стивен Кинг","type":"author","id":"`+id.String()+`","books_count":3,"label":"Ужасы"}`,
		string(raw))
	raw, err = json.Marshal(AutocompleteSuggestion{Value: "Стивен Кинг", Type: "author", ID: 42, BooksCount: 3})
	require.NoError(t, err)
	assert.Equal(t, `{"value":"Стивен Кинг","type":"author","id":42,"books_count":3}`, string(raw))
	raw, err = json.Marshal([]AutocompleteSuggestion{{Value: "Оно", Type: "book", ID: 9}})
	require.NoError(t, err)
	assert.Equal(t, `[{"value":"Оно","type":"book","id":9}]`, string(raw))
}
