package authorentity_test

import (
	"fmt"
	"slices"
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mergedPair is the churn world after its swap pair merged: A («Стивен Кинг»,
// big books) absorbed B («Кинг Стивен», credit 500 and whatever more grows
// before the merge).
func mergedPair(t *testing.T, big int, grow func(ch *churn)) (w *world, decision int64, a, b uuid.UUID) {
	t.Helper()
	ch := newChurn(t, big)
	if grow != nil {
		grow(ch)
	}
	w = newWorld(t, ch)
	w.pass()
	a, b = w.last.EntityOf(1), w.last.EntityOf(500)
	require.NotEqual(t, a, b)
	decision = w.decide(authorentity.CandidateSwap, 1, 500)
	w.pass()
	require.Equal(t, applied(decision), w.state(decision))
	require.True(t, w.merged(b, a))
	return w, decision, a, b
}

func revival(id uuid.UUID, outcome, reason string, recorded, live, restored int) authorentity.Revival {
	return authorentity.Revival{Entity: id, Outcome: outcome, Reason: reason, Recorded: recorded, Live: live,
		Restored: restored}
}

// revivalOf is the newest pass's revival of entity id without its
// merge-log ordinal, which only the store knows.
func (w *world) revivalOf(id uuid.UUID) authorentity.Revival {
	r := w.revival(id)
	r.LogID = 0
	return r
}

// A revoke splits the pair and both old IDs come back: the absorbed one
// is live again, its merged_into cleared.
func TestRevokeExact(t *testing.T) {
	w, d, a, b := mergedPair(t, 40, nil)
	w.revoke(d)
	p := w.pass()
	assert.Equal(t, a, p.EntityOf(1))
	assert.Equal(t, b, p.EntityOf(500))
	assert.Nil(t, w.s.Entities[b].MergedInto)
	assert.Equal(t, revival(b, authorentity.RevivalExact, "", 1, 1, 1), w.revivalOf(b))
	assert.Equal(t, 1, p.Report.Model.Revivals.Exact)
	again := w.pass()
	assert.Zero(t, again.Changes())
	assert.Empty(t, again.Revivals())
}

// A recorded member deleted since: the rest revives, partial member_gone.
func TestRevokeAfterMemberDeletion(t *testing.T) {
	w, d, _, b := mergedPair(t, 40, func(ch *churn) {
		ch.c.book(502, "Повести").credit(502, 502, "fp-small-2", person("СТИВЕН", "Кинг"))
	})
	w.ch.c.in.Credits = slices.DeleteFunc(w.ch.c.in.Credits, func(c authorentity.Credit) bool { return c.ID == 502 })
	w.revoke(d)
	p := w.pass()
	assert.Equal(t, b, p.EntityOf(500))
	assert.Equal(t, revival(b, authorentity.RevivalPartial, authorentity.RevivalMemberGone, 2, 1, 1),
		w.revivalOf(b))
}

// An extractor bump re-keys the record: matched without the version, the
// absorbed side revives, partial extractor_moved.
func TestRevokeAfterExtractorBump(t *testing.T) {
	w, d, _, b := mergedPair(t, 40, nil)
	for i := range w.ch.c.in.Credits {
		w.ch.c.in.Credits[i].ExtractorVersion = "v2"
	}
	w.revoke(d)
	p := w.pass()
	assert.Equal(t, b, p.EntityOf(500))
	assert.Equal(t, revival(b, authorentity.RevivalPartial, authorentity.RevivalExtractorMoved, 1, 1, 1),
		w.revivalOf(b))
}

// A normalizer change of a member's rendered name: the record is the
// fingerprints, so the revival is exact and the name is the new one.
func TestRevokeAfterNormalizerChange(t *testing.T) {
	w, d, _, b := mergedPair(t, 40, nil)
	r := *w.ch.credit(500).Result
	r.DisplayName = "Кинг Стивен Эдвин"
	w.ch.credit(500).Result = &r
	w.revoke(d)
	p := w.pass()
	assert.Equal(t, b, p.EntityOf(500))
	assert.Equal(t, revival(b, authorentity.RevivalExact, "", 1, 1, 1), w.revivalOf(b))
	assert.Equal(t, "Кинг Стивен Эдвин", w.s.Entities[b].DisplayName)
}

// A recorded member now in another component: the larger piece inherits
// the ID, partial member_split; the other piece is new.
func TestRevokeMemberSplit(t *testing.T) {
	w, d, _, b := mergedPair(t, 40, func(ch *churn) {
		ch.c.book(501, "Ещё рассказы").credit(501, 501, "fp-small", person("Стивен", "Кинг"))
		ch.c.book(502, "Повести").credit(502, 502, "fp-small-2", person("СТИВЕН", "Кинг"))
	})
	r := *w.ch.credit(502).Result
	r.Additional = []string{"Петрович"}
	w.ch.credit(502).Result = &r
	w.revoke(d)
	p := w.pass()
	assert.Equal(t, b, p.EntityOf(500))
	assert.Equal(t, b, p.EntityOf(501))
	assert.NotEqual(t, b, p.EntityOf(502))
	assert.Equal(t, revival(b, authorentity.RevivalPartial, authorentity.RevivalMemberSplit, 2, 2, 1),
		w.revivalOf(b))
}

// A chain A–B (d1), B–C (d2) with d1 revoked: d2 still holds B and C
// together. B inherits its ID (partial: C's members came along), C is
// absorbed into B again with a fresh path (lapsed still_merged), and the
// next pass is stable. (A revoke inside a cycle refuses the whole
// component instead: the conflict rule of §4.2.)
func TestRevokeWhileAnotherMergeHolds(t *testing.T) {
	w := newWorld(t, threePoselyagins(t))
	w.pass()
	a, b, c := w.last.EntityOf(1), w.last.EntityOf(11), w.last.EntityOf(21)
	d1 := w.decide(authorentity.CandidateSwap, 1, 11)
	d2 := w.decide(authorentity.CandidateSwap, 11, 21)
	w.pass()
	require.True(t, w.merged(b, a))
	require.True(t, w.merged(c, a))
	w.revoke(d1)
	p := w.pass()
	assert.Equal(t, applied(d2), w.state(d2))
	assert.Equal(t, a, p.EntityOf(1))
	assert.Equal(t, b, p.EntityOf(11))
	assert.Equal(t, b, p.EntityOf(21))
	assert.Equal(t, revival(b, authorentity.RevivalPartial, authorentity.RevivalExtraMembers, 1, 1, 1),
		w.revivalOf(b))
	assert.Equal(t, revival(c, authorentity.RevivalLapsed, authorentity.RevivalStillMerged, 1, 1, 0),
		w.revivalOf(c))
	assert.True(t, w.merged(c, b))
	assert.Equal(t, []int64{d2}, w.s.ModelMerges[c].DecisionIDs, "a fresh path")
	again := w.pass()
	assert.Zero(t, again.Changes())
	assert.Empty(t, again.Revivals())
}

// nested is Y («Кинг Стивен» swapped, 1 book) merged into X («Стивен Кинг»,
// 3 books) by d1, then X into S («Stephen King», 6 books) by d2.
type nestedMerge struct {
	w       *world
	d1, d2  int64
	x, y, s uuid.UUID
}

func nested(t *testing.T) nestedMerge {
	t.Helper()
	ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
	for i := int64(1); i <= 3; i++ {
		ch.c.book(i, "Роман").credit(i, i, "fp-x", person("Кинг", "Стивен"))
	}
	ch.c.book(11, "Рассказ").credit(11, 11, "fp-y", person("Стивен", "Кинг"))
	for i := int64(21); i <= 26; i++ {
		ch.c.book(i, "Novel").credit(i, i, "fp-s", person("King", "Stephen").latin())
	}
	w := newWorld(t, ch)
	w.pass()
	x, y, s := w.last.EntityOf(1), w.last.EntityOf(11), w.last.EntityOf(21)
	d1 := w.decide(authorentity.CandidateSwap, 1, 11)
	w.pass()
	require.True(t, w.merged(y, x))
	d2 := w.decide(authorentity.CandidateTranslit, 1, 21)
	w.pass()
	require.True(t, w.merged(x, s))
	require.True(t, w.merged(y, x))
	require.Equal(t, []int64{d2}, w.s.ModelMerges[x].DecisionIDs)
	return nestedMerge{w: w, d1: d1, d2: d2, x: x, y: y, s: s}
}

// Revoke the outer merge: X revives with its record, which holds Y's
// members; Y stays merged into X.
func TestRevokeNestedOuter(t *testing.T) {
	n := nested(t)
	w, x, y, s := n.w, n.x, n.y, n.s
	w.revoke(n.d2)
	p := w.pass()
	assert.Equal(t, x, p.EntityOf(1))
	assert.Equal(t, x, p.EntityOf(11))
	assert.Equal(t, s, p.EntityOf(21))
	assert.True(t, w.merged(y, x))
	assert.Equal(t, authorentity.RevivalExact, w.revival(x).Outcome)
}

// Revoke the inner merge only: Y revives, X stays merged into S.
func TestRevokeNestedInner(t *testing.T) {
	n := nested(t)
	w, x, y, s := n.w, n.x, n.y, n.s
	w.revoke(n.d1)
	p := w.pass()
	assert.Equal(t, y, p.EntityOf(11))
	assert.Equal(t, s, p.EntityOf(1))
	assert.True(t, w.merged(x, s))
	assert.Equal(t, revival(y, authorentity.RevivalExact, "", 1, 1, 1), w.revivalOf(y))
}

// Revoke both: each revives with its own members — Y's keys go to the
// innermost record, so X is partial (member_split).
func TestRevokeNestedBoth(t *testing.T) {
	n := nested(t)
	w, x, y, s := n.w, n.x, n.y, n.s
	w.revoke(n.d1)
	w.revoke(n.d2)
	p := w.pass()
	assert.Equal(t, x, p.EntityOf(1))
	assert.Equal(t, y, p.EntityOf(11))
	assert.Equal(t, s, p.EntityOf(21))
	assert.Equal(t, revival(y, authorentity.RevivalExact, "", 1, 1, 1), w.revivalOf(y))
	assert.Equal(t, revival(x, authorentity.RevivalPartial, authorentity.RevivalMemberSplit, 2, 2, 1),
		w.revivalOf(x))
}

// A recorded member an administrator moved by hand: the rest revives,
// partial manual_member; the manual row and its entity are untouched.
func TestRevokeWithManualReassignment(t *testing.T) {
	w, d, _, b := mergedPair(t, 40, func(ch *churn) {
		ch.c.book(501, "Ещё рассказы").credit(501, 501, "fp-small", person("Стивен", "Кинг"))
	})
	manual := uuid.New()
	w.s.Entities[manual] = authorentity.EntityRow{ID: manual, Kind: "person", DisplayName: "Стивен Кинг"}
	key := authorentity.CreditKey{CreditID: 501}
	row := authorentity.CreditRow{Key: key, EntityID: &manual, Method: authorentity.MethodManual, Rule: "manual"}
	w.s.Credits[key] = row
	w.revoke(d)
	p := w.pass()
	assert.Equal(t, b, p.EntityOf(500))
	assert.Equal(t, manual, p.EntityOf(501))
	assert.Equal(t, row, w.s.Credits[key])
	assert.Nil(t, w.s.Entities[manual].MergedInto)
	assert.Equal(t, revival(b, authorentity.RevivalPartial, authorentity.RevivalManualMember, 1, 1, 1),
		w.revivalOf(b))
}

// A lapse splits the pair with the absorbed ID back; the new candidate
// asked again merges it under the same survivor.
func TestLapseRevivesAndReaskRemerges(t *testing.T) {
	w, d, a, b := mergedPair(t, 40, nil)
	w.ch.c.book(501, "Ещё рассказы").credit(501, 501, "fp-small", person("Стивен", "Кинг"))
	p := w.pass()
	assert.Equal(t, lapsed(d, authorentity.LapsedInputChanged), w.state(d))
	assert.Equal(t, b, p.EntityOf(500))
	again := w.decide(authorentity.CandidateSwap, 1, 500)
	p = w.pass()
	assert.Equal(t, applied(again), w.state(again))
	assert.Equal(t, a, p.EntityOf(500))
	assert.True(t, w.merged(b, a))
	assert.Equal(t, []int64{again}, w.s.ModelMerges[b].DecisionIDs)
	assert.Zero(t, w.pass().Changes())
}

// Sides that grew past each other while their decision stays applied: the
// whole path applied, nothing revives and no ID flips.
func TestFullPathAppliedNoFlip(t *testing.T) {
	w, d, a, b := mergedPair(t, 12, func(ch *churn) {
		for i := int64(501); i <= 510; i++ {
			ch.c.book(i, fmt.Sprintf("Рассказы %d", i)).credit(i, i, "fp-small", person("Стивен", "Кинг"))
		}
	})
	// The absorbed side passes the survivor outside its sample, its size
	// class kept: the digest stays.
	for i := int64(511); i <= 514; i++ {
		w.ch.c.book(i, fmt.Sprintf("Рассказы %d", i)).credit(i, i, "fp-small", person("Стивен", "Кинг"))
	}
	p := w.pass()
	assert.Equal(t, applied(d), w.state(d))
	assert.Equal(t, a, p.EntityOf(500))
	assert.True(t, w.merged(b, a))
	assert.Empty(t, p.Revivals())
}

// Every recorded member gone: no piece can take the ID back, so the
// absorbed entity stays merged, lapsed no_live_member.
func TestRevokeAfterEveryMemberDeletion(t *testing.T) {
	w, d, a, b := mergedPair(t, 40, nil)
	w.ch.c.in.Credits = slices.DeleteFunc(w.ch.c.in.Credits, func(c authorentity.Credit) bool { return c.ID == 500 })
	w.revoke(d)
	w.pass()
	assert.True(t, w.merged(b, a))
	assert.Equal(t, revival(b, authorentity.RevivalLapsed, authorentity.RevivalNoLiveMember, 1, 0, 0),
		w.revivalOf(b))
}
