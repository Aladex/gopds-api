package database

import (
	"context"
	"testing"

	"gopds-api/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A bot's card reads its card in one statement: the visible files and the
// card-level star.
func TestTelegramCardOfCountsVisibleFiles(t *testing.T) {
	f := cardsDB(t)
	entity := f.entity("Булгаков")
	files := f.group3(entity, "Белая гвардия")
	lone := f.variantBook("Морфий", []string{entity})
	user, other := f.user(), f.user()
	f.star(other, files[0])
	f.commit()
	ctx := context.Background()

	card, err := TelegramCardOf(ctx, user, files[1])
	require.NoError(t, err)
	assert.Equal(t, TelegramCard{Files: 3}, card, "another reader's star is not the caller's")

	f.star(user, files[2])
	f.commit()
	card, err = TelegramCardOf(ctx, user, files[0])
	require.NoError(t, err)
	assert.Equal(t, TelegramCard{Files: 3, Fav: true}, card, "a star on any file of the card")

	// A file hidden since the card was built is not counted, and its star
	// is not the card's.
	f.exec(`UPDATE opds_catalog_book SET duplicate_hidden = true WHERE id = ?`, files[2])
	f.commit()
	card, err = TelegramCardOf(ctx, user, files[0])
	require.NoError(t, err)
	assert.Equal(t, TelegramCard{Files: 2}, card)

	// A book with no row yet is a card of one.
	card, err = TelegramCardOf(ctx, user, lone)
	require.NoError(t, err)
	assert.Equal(t, TelegramCard{Files: 1}, card)
}

func TestUnsetFavoriteBookGroup(t *testing.T) {
	f := cardsDB(t)
	entity := f.entity("Беляев")
	files := f.group3(entity, "Остров погибших кораблей")
	unrelated := f.variantBook("Ариэль", []string{entity})
	user, other := f.user(), f.user()
	f.star(user, files[0])
	f.star(user, files[2])
	f.star(user, unrelated)
	f.star(other, files[0])
	f.commit()

	// Under a lease the update no longer holds, nothing is written.
	lost := WithInboxLease(context.Background(), InboxLease{BotRef: 1, UpdateID: 1})
	assert.ErrorIs(t, UnsetFavoriteBookGroup(lost, user, files[1]), ErrInboxLeaseLost)
	n, err := f.scratch.Model((*models.UserToBook)(nil)).Where("user_id = ?", user).Count()
	require.NoError(t, err)
	assert.Equal(t, 3, n)

	require.NoError(t, UnsetFavoriteBookGroup(context.Background(), user, files[1]))
	var left []int64
	_, err = f.scratch.Query(&left, `SELECT book_id FROM favorite_books WHERE user_id = ? ORDER BY book_id`, user)
	require.NoError(t, err)
	assert.Equal(t, []int64{unrelated}, left, "every file of the card, the unrelated star stays")
	n, err = f.scratch.Model((*models.UserToBook)(nil)).Where("user_id = ?", other).Count()
	require.NoError(t, err)
	assert.Equal(t, 1, n, "another reader's star is untouched")
}
