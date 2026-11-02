package services

import (
	"encoding/json"
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The audit side carries every identity field of the frozen card (E4h
// round 2): names, variants, the Wikidata label and QID, and each book's
// fields with its samizdat mark; a plain card's side encodes as before, the
// new fields absent.
func TestTheAuditSideCarriesTheCardsIdentity(t *testing.T) {
	card := authorentity.MergeCard{Names: []string{"Mikhail Bulgakov", "Михаил Булгаков"}, Variants: []string{"Мастер"},
		Size: "2-3", Wikidata: "Михаил Булгаков", WikidataID: "Q835",
		Books: []authorentity.MergeCardBook{{Title: "Роковые яйца", Samizdat: true, Year: "1925", Lang: "ru"}}}
	side := adversarialSideOf(&card)
	assert.Equal(t, card.Names, side.Names)
	assert.Equal(t, card.Variants, side.Variants)
	assert.Equal(t, card.Wikidata, side.Wikidata)
	assert.Equal(t, card.WikidataID, side.WikidataID)
	require.Len(t, side.Books, 1)
	assert.True(t, side.Books[0].Samizdat)

	plain := authorentity.MergeCard{Names: []string{"А. Сальников"}, Size: "4-10",
		Books: []authorentity.MergeCardBook{{Title: "Отдел"}}}
	raw, err := json.Marshal(adversarialSideOf(&plain))
	require.NoError(t, err)
	assert.JSONEq(t, `{"names":["А. Сальников"],"size":"4-10","books":[{"title":"Отдел"}]}`, string(raw))
}
