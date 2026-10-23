package telegram_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"gopds-api/internal/scanfixture"
	"gopds-api/telegram/screens"
	"gopds-api/telegram/tgtest"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bot under authors.identity = entity (design E, §4.2): a book's card
// opens the author entities its line names, an author's list is an entity's
// books under the entity's name, and every author button carries the
// entity's handle. A button made under the legacy identity — or a legacy
// «author:» button of the old interface — names no author here: the bot says
// the author is not found, never shows another author's books; and a
// handle's button means nothing under the legacy identity.

// entityPod is a v2 stack whose bot names authors by their entities, over
// the characterization books linked to scanfixture.Entities.
func entityPod(t *testing.T) (v *v2Stack, ids map[string]uuid.UUID) {
	t.Helper()
	v = newV2(t, func(s *stack) {
		ids = scanfixture.LinkEntities(t, s.db)
		s.entities = true
	})
	return v, ids
}

// tapOn presses label on a message through pod, another process of the
// same bot.
func (v *v2Stack) tapOn(pod *stack, on *tgtest.Call, label string) {
	v.t.Helper()
	rec := pod.hook(botToken1).Post(v.t, v.u.Tap(v.t, ownerTG, on, label))
	require.Equal(v.t, http.StatusOK, rec.Code)
	pod.bm.WaitIdle()
}

// labelWith is the button label of message on that contains part.
func labelWith(t *testing.T, on *tgtest.Call, part string) string {
	t.Helper()
	for _, text := range tgtest.Labels(t, on) {
		if strings.Contains(text, part) {
			return text
		}
	}
	t.Fatalf("no button with %q in %v", part, tgtest.Labels(t, on))
	return ""
}

func TestTheBotOpensAnEntitysBooksFromTheCard(t *testing.T) {
	v, ids := entityPod(t)
	results, card := v.openFixtureCard()
	v.tap(&card, screens.ButtonBookAuthors)
	authors := v.latest(results.MessageID)
	// The four entities the line links to, in line order; the namesake's
	// label tells this Толстой from the other.
	assert.Equal(t, []string{"Лев Николаевич Толстой · prose_classic", "Пушкин", "Аноним Первый", "Аноним Второй"},
		tgtest.Labels(t, &authors)[:4])

	v.tap(&authors, "Лев Николаевич Толстой · prose_classic")
	list := v.latest(results.MessageID)
	assert.Contains(t, content(&list), "Лев Николаевич Толстой · prose_classic")
	books := strings.Join(tgtest.Labels(t, &list), "|")
	assert.Contains(t, books, "война и МИР")
	assert.NotContains(t, books, "Сборник без автора", "only the entity's books")
	assert.NotContains(t, books, "the GREAT book", "only the entity's books")
	assert.Equal(t, 1, v.count(`SELECT count(*) FROM telegram_view WHERE author_id = (
		SELECT id FROM telegram_author_entity WHERE entity_id = ?)`, ids["Толстой"]), "the list goes by the handle")
	assert.Zero(t, v.count(`SELECT count(*) FROM telegram_author_entity WHERE id < 1099511627776`))
}

// The author tab of a search lists entities, each opening its books.
func TestTheBotsAuthorSearchListsEntities(t *testing.T) {
	v, _ := entityPod(t)
	v.say("толстой")
	results := v.lastSend()
	v.tap(&results, labelWith(t, &results, "Авторы"))
	tab := v.latest(results.MessageID)
	v.tap(&tab, labelWith(t, &tab, "Лев Николаевич Толстой"))
	list := v.latest(results.MessageID)
	assert.Contains(t, strings.Join(tgtest.Labels(t, &list), "|"), "война и МИР")
}

func TestALegacyAuthorButtonIsNotFoundUnderEntities(t *testing.T) {
	// A card painted by the legacy identity, its «Авторы книги» pressed.
	v := newV2(t, func(s *stack) { scanfixture.LinkEntities(t, s.db) })
	results, card := v.openFixtureCard()
	v.tap(&card, screens.ButtonBookAuthors)
	legacyAuthors := v.latest(results.MessageID)

	// The library switches to entities: another process, the same bot.
	entities := *v.stack
	entities.entities = true
	pod := entities.start()
	before := len(v.calls())
	v.tapOn(pod, &legacyAuthors, labelWith(t, &legacyAuthors, "Толстой"))
	after := v.latest(results.MessageID)
	assert.Greater(t, len(v.calls()), before)
	assert.Contains(t, content(&after), screens.CopyAuthorGone)
	assert.NotContains(t, content(&after), "война и МИР", "never a list of books")

	// The old interface's text button names a legacy author too.
	tolstoy := strconv.FormatInt(legacyID(t, v.stack, "Толстой лев"), 10)
	rec := pod.hook(botToken1).Post(t, v.u.Callback(ownerTG, &after, "author:"+tolstoy))
	require.Equal(t, http.StatusOK, rec.Code)
	pod.bm.WaitIdle()
	notice := v.lastSend()
	assert.Contains(t, content(&notice), screens.CopyAuthorGone)

	// And an entity's button means nothing under the legacy identity.
	rec = pod.hook(botToken1).Post(t, v.u.Text(ownerTG, "мир"))
	require.Equal(t, http.StatusOK, rec.Code)
	pod.bm.WaitIdle()
	fresh := v.lastSend()
	v.tapOn(pod, &fresh, labelWith(t, &fresh, "война и МИР"))
	card = v.latest(fresh.MessageID)
	v.tapOn(pod, &card, screens.ButtonBookAuthors)
	entityAuthors := v.latest(fresh.MessageID)
	v.tap(&entityAuthors, "Пушкин")
	gone := v.latest(fresh.MessageID)
	assert.Contains(t, content(&gone), screens.CopyAuthorGone)
}

// legacyID is the catalog author of the name.
func legacyID(t *testing.T, s *stack, name string) int64 {
	t.Helper()
	var ids []int64
	_, err := s.db.Query(&ids, `SELECT id FROM opds_catalog_author WHERE full_name = ?`, name)
	require.NoError(t, err)
	require.Len(t, ids, 1)
	return ids[0]
}
