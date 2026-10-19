package telegram_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"gopds-api/internal/scanfixture"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The S4 fix round, B2 (review-s4.md): an update whose handler could not be
// chosen stays unread.

// armedRouterReadFailure fails the next read of a bot's row by id, once armed.
func armedRouterReadFailure(s *stack) *atomic.Bool {
	armed := &atomic.Bool{}
	s.db.AddQueryHook(queryHook(func(_ context.Context, q string) error {
		if strings.Contains(q, `FROM "telegram_bot"`) && strings.Contains(q, `"telegram_bot"."id"`) && armed.CompareAndSwap(true, false) {
			return errors.New("transient router read failed")
		}
		return nil
	}))
	return armed
}

// B2, the reviewer's TestReviewTransientRouterReadDoesNotFinishLegacyUpdateUnread.
func TestATransientRouterReadDoesNotFinishALegacyUpdateUnread(t *testing.T) {
	s := newStack(t)
	armed := armedRouterReadFailure(s)
	s.start()
	user := s.seedUser("legacy-transient")
	secret, _ := s.connect(user, botToken1)
	s.linkVia(botToken1, ownerTG, secret)
	s.bm.WaitIdle()
	before := len(s.messagesTo(ownerTG))
	update := s.u.Text(ownerTG, "/b мир")
	armed.Store(true)
	rec := s.hook(botToken1).Post(t, update)
	require.Equal(t, http.StatusOK, rec.Code)
	s.bm.WaitIdle()
	require.False(t, armed.Load(), "the fault was not injected")
	assert.Len(t, s.messagesTo(ownerTG), before, "no handler ran")
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_update_inbox WHERE update_id = ? AND state = 'pending'`, update.ID),
		"an unread update is kept for the next sweep")
}

// B2, the reviewer's TestReviewTransientRouterReadDoesNotLoseLegacyDownload.
func TestATransientRouterReadDoesNotLoseALegacyDownload(t *testing.T) {
	s := newStack(t)
	armed := armedRouterReadFailure(s)
	s.start()
	user := s.seedUser("legacy-transient")
	secret, _ := s.connect(user, botToken1)
	s.linkVia(botToken1, ownerTG, secret)
	s.bm.WaitIdle()
	hook := s.hook(botToken1)
	// An old card's file button, still in the chat.
	home := s.waitMessage(ownerTG, 1)
	card := oldCard(t, &home, s.books[scanfixture.EntryMultiAuthor])
	update := s.u.Tap(t, ownerTG, &card, "📚 EPUB")
	armed.Store(true)
	rec := hook.Post(t, update)
	require.Equal(t, http.StatusOK, rec.Code)
	s.bm.WaitIdle()
	require.False(t, armed.Load(), "the fault was not injected")
	assert.Zero(t, s.count(`SELECT count(*) FROM telegram_delivery`))
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_update_inbox WHERE update_id = ? AND state = 'pending'`, update.ID),
		"a download that made no job stays retriable")
}
