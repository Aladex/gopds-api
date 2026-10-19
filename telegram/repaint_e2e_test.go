package telegram_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/telegram/tgtest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// R2 of the S4 fixes (review-s4-fixes.md): the repaint a job owes its card
// survives a wait for the chat that runs out.

// heldEPUB opens the fixture card, presses EPUB with that edit held, and
// lets the job finish; it returns the card and the press's end.
func heldEPUB(t *testing.T, v *v2Stack, tr *heldPreparing, releasePrepare func()) (card tgtest.Call, pressed chan struct{}) {
	t.Helper()
	_, card = v.openFixtureCard()
	pressed = make(chan struct{})
	go func() { v.tap(&card, "EPUB"); close(pressed) }()
	select {
	case <-tr.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the preparing edit did not reach the transport")
	}
	releasePrepare()
	v.waitJobSent()
	return card, pressed
}

// finishedCard reports the card painted without «⏳».
func finishedCard(t *testing.T, v *v2Stack, messageID int64) bool {
	latest := v.latest(messageID)
	rows := tgtest.ButtonTexts(t, &latest)
	return len(rows) > 0 && rows[0][0] == "EPUB" && !strings.Contains(content(&latest), "⏳")
}

// R2: a job whose wait for the chat ran out leaves its repaint to the chat's
// holder, which runs it before letting go.
func TestARepaintThatCouldNotWaitIsRunByTheHolder(t *testing.T) {
	tr := &heldPreparing{entered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	letGo := func() { releaseOnce.Do(func() { close(tr.release) }) }
	defer letGo()
	var releasePrepare func()
	v := newV2(t, func(s *stack) {
		s.transport = tr
		s.chatWait = 200 * time.Millisecond
		releasePrepare = gatedPrepare(s)
	})
	defer releasePrepare()
	card, pressed := heldEPUB(t, v, tr, releasePrepare)
	require.Eventually(t, func() bool { return v.count(`SELECT count(*) FROM telegram_panel_repaint`) == 1 },
		5*time.Second, 10*time.Millisecond)
	time.Sleep(600 * time.Millisecond) // three of the job's waits: it gave up
	v.delivery.WaitIdle()
	assert.Equal(t, 1, v.count(`SELECT count(*) FROM telegram_panel_repaint`), "the repaint is kept")

	letGo()
	<-pressed
	require.Eventually(t, func() bool { return finishedCard(t, v, card.MessageID) }, 5*time.Second, 10*time.Millisecond)
	assert.Zero(t, v.count(`SELECT count(*) FROM telegram_panel_repaint`))
	assert.Len(t, v.fake.Calls("sendDocument"), 1)
}

// R2: with no holder left to run it, the sweeper does.
func TestTheSweeperRunsARepaintLeftBehind(t *testing.T) {
	var releasePrepare func()
	v := newV2(t, func(s *stack) {
		s.chatWait = 200 * time.Millisecond
		releasePrepare = gatedPrepare(s)
	})
	defer releasePrepare()
	_, card := v.openFixtureCard()
	v.tap(&card, "EPUB")
	ref := int64(v.count(`SELECT id FROM telegram_bot`))
	// A holder that will not run the repaint: another pod, dying.
	unlock, err := database.LockTelegramChat(context.Background(), ref, ownerTG, time.Minute, time.Second)
	require.NoError(t, err)
	releasePrepare()
	v.waitJobSent()
	v.delivery.WaitIdle()
	unlock()
	require.Equal(t, 1, v.count(`SELECT count(*) FROM telegram_panel_repaint`))
	require.False(t, finishedCard(t, v, card.MessageID), "still «⏳»")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	v.bm.StartInboxSweeper(ctx, 100*time.Millisecond)
	require.Eventually(t, func() bool { return finishedCard(t, v, card.MessageID) }, 15*time.Second, 50*time.Millisecond)
	assert.Zero(t, v.count(`SELECT count(*) FROM telegram_panel_repaint`))
}

// R2, the reviewer's TestReviewCompletionSurvivesDefaultChatWait: two
// processes, the production waits, an edit held for 31 s.
func TestACompletionSurvivesTheDefaultChatWait(t *testing.T) {
	if testing.Short() {
		t.Skip("holds an edit for 31 s")
	}
	tr := &heldPreparing{entered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	letGo := func() { releaseOnce.Do(func() { close(tr.release) }) }
	defer letGo()
	var releasePrepare func()
	v := newV2(t, func(s *stack) { s.transport = tr; releasePrepare = gatedPrepare(s) })
	defer releasePrepare()
	_, card := v.openFixtureCard()
	v.delivery.Stop()
	other := v.secondPod()
	initialEdits := len(v.edits(card.MessageID))
	pressed := make(chan struct{})
	go func() { v.tap(&card, "EPUB"); close(pressed) }()
	select {
	case <-tr.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the edit was not held")
	}
	releasePrepare()
	v.waitJobSent()
	time.Sleep(31 * time.Second)
	assert.Len(t, v.edits(card.MessageID), initialEdits, "no completion edit overtakes the held press")
	letGo()
	select {
	case <-pressed:
	case <-time.After(5 * time.Second):
		t.Fatal("the press did not finish")
	}
	other.delivery.WaitIdle()
	assert.Len(t, v.fake.Calls("sendDocument"), 1)
	require.Eventually(t, func() bool { return finishedCard(t, v, card.MessageID) }, 5*time.Second, 10*time.Millisecond,
		"a slow edit does not lose the completion repaint")
	assert.Zero(t, v.count(`SELECT count(*) FROM telegram_chat_lock`))
}
