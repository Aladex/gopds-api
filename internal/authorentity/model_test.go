package authorentity_test

import (
	"encoding/json"
	"slices"
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// world is a catalog and its store over passes, with the decisions merge
// runs and an administrator made on its candidates.
type world struct {
	t         *testing.T
	ch        *churn
	s         *authorentity.State
	decisions []authorentity.MergeDecision
	nextID    int64
	// level0 is the newest pass before its decisions; last after them.
	level0, last *authorentity.Plan
}

func newWorld(t *testing.T, ch *churn) *world {
	return &world{t: t, ch: ch, s: authorentity.NewState()}
}

// pass builds over the store with the live decisions, seals the candidates,
// applies the decisions and stores the plan.
func (w *world) pass() *authorentity.Plan {
	w.t.Helper()
	w.ch.c.in.Old = w.s
	w.ch.c.in.Decisions = slices.Clone(w.decisions)
	p := w.ch.c.build()
	require.NoError(w.t, p.SealMergeCandidates(w.ch.fields))
	w.level0 = p
	final, err := p.ApplyMergeDecisions()
	require.NoError(w.t, err)
	final.Apply(w.s)
	w.last = final
	// The next pass reads which decisions this one applied, as the
	// database keeps their states.
	for i := range w.decisions {
		w.decisions[i].WasApplied = w.state(w.decisions[i].ID).State == authorentity.DecisionApplied
	}
	return final
}

// register gives decision id a registration: a later one is applied after.
func (w *world) register(id, registration int64) {
	w.t.Helper()
	i := slices.IndexFunc(w.decisions, func(d authorentity.MergeDecision) bool { return d.ID == id })
	require.GreaterOrEqual(w.t, i, 0)
	w.decisions[i].RegistrationID = registration
}

// decide is a same decision on the newest pass's candidate of kind between
// the sides of credits a and b; for a bucket item a is the book's credit and
// b a credit of the full name.
func (w *world) decide(kind string, a, b int64) int64 {
	w.t.Helper()
	var c *authorentity.MergeCandidate
	if kind == authorentity.CandidateBucket {
		c = bookItemOf(w.level0, a, b)
	} else {
		c = candidateOf(w.level0, kind, a, b)
	}
	require.NotNil(w.t, c, "a %s candidate between %d and %d", kind, a, b)
	w.nextID++
	d := authorentity.MergeDecision{ID: w.nextID, Digest: c.Digest, Kind: c.Kind, Script: c.Script,
		ItemKind: c.ItemKind, Outcome: authorentity.DecisionSame, MembersSHA256: c.MembersSHA256,
		MembersA: c.MembersA, MembersB: c.MembersB}
	if c.ItemKind == authorentity.ItemBook {
		d.Credit = &authorentity.CreditKey{CreditID: c.CreditID, Ordinal: c.CreditOrdinal}
	}
	w.decisions = append(w.decisions, d)
	return d.ID
}

// revoke is an administrator's revoke of decision id: the decision is no
// longer live and a revoke of its digest takes its place.
func (w *world) revoke(id int64) {
	w.t.Helper()
	i := slices.IndexFunc(w.decisions, func(d authorentity.MergeDecision) bool { return d.ID == id })
	require.GreaterOrEqual(w.t, i, 0)
	d := w.decisions[i]
	w.decisions = slices.Delete(w.decisions, i, i+1)
	w.nextID++
	d.ID, d.Outcome = w.nextID, authorentity.DecisionRevoked
	w.decisions = append(w.decisions, d)
}

// state is what the newest pass did with decision id.
func (w *world) state(id int64) authorentity.DecisionState {
	for _, s := range w.last.DecisionStates() {
		if s.DecisionID == id {
			return s
		}
	}
	return authorentity.DecisionState{}
}

// revival is the newest pass's revival of entity id.
func (w *world) revival(id uuid.UUID) authorentity.Revival {
	for _, r := range w.last.Revivals() {
		if r.Entity == id {
			return r
		}
	}
	return authorentity.Revival{}
}

func applied(id int64) authorentity.DecisionState {
	return authorentity.DecisionState{DecisionID: id, State: authorentity.DecisionApplied}
}

func lapsed(id int64, reason string) authorentity.DecisionState {
	return authorentity.DecisionState{DecisionID: id, State: authorentity.DecisionLapsed, Reason: reason}
}

func conflict(id int64, reason string) authorentity.DecisionState {
	return authorentity.DecisionState{DecisionID: id, State: authorentity.DecisionConflict, Reason: reason}
}

// merged reports the stored entity id merged into into.
func (w *world) merged(id, into uuid.UUID) bool {
	e := w.s.Entities[id]
	return e.MergedInto != nil && *e.MergedInto == into
}

// A pass with no decision and no model merge never enters the decision
// step: the plan is the one Build made, unit for unit, report byte for byte.
func TestNoDecisionLeavesThePlanAsItIs(t *testing.T) {
	entered := 0
	defer authorentity.OnDecisionStep(func() { entered++ })()
	for _, c := range []*catalog{twoTolstoys(t), newChurn(t, 40).c} {
		p := c.build()
		require.NoError(t, p.SealMergeCandidates(nil))
		before, err := json.Marshal(p.Report)
		require.NoError(t, err)
		units := slices.Clone(p.Units)
		final, err := p.ApplyMergeDecisions()
		require.NoError(t, err)
		assert.Same(t, p, final)
		assert.Equal(t, units, final.Units)
		after, err := json.Marshal(final.Report)
		require.NoError(t, err)
		assert.Equal(t, string(before), string(after))
		assert.NotContains(t, string(after), `"model"`)
		assert.Empty(t, final.DecisionStates())
		assert.Empty(t, final.Revivals())
	}
	assert.Zero(t, entered)
}

// A same decision joins its two level-0 components: the larger keeps its ID,
// the other is absorbed by method model with its decision path and what it
// held; a rebuild keeps the merge and changes nothing.
func TestADecisionMergesAndARebuildKeepsIt(t *testing.T) {
	w := newWorld(t, newChurn(t, 40))
	w.pass()
	a, b := w.last.EntityOf(1), w.last.EntityOf(500)
	require.NotEqual(t, a, b)
	id := w.decide(authorentity.CandidateSwap, 1, 500)
	p := w.pass()
	assert.Equal(t, applied(id), w.state(id))
	assert.Equal(t, a, p.EntityOf(500))
	assert.Equal(t, "model:swap", p.RuleOf(500))
	assert.True(t, w.merged(b, a))
	m := w.s.Merges[len(w.s.Merges)-1]
	assert.Equal(t, authorentity.MethodModel, m.Method)
	assert.Equal(t, []int64{id}, m.DecisionIDs)
	assert.Equal(t, authorentity.CandidateSwap, m.Stratum)
	assert.Equal(t, []authorentity.FPKey{{Fingerprint: "fp-small", ExtractorVersion: "v1"}}, m.Members)
	require.NotNil(t, p.Report.Model)
	assert.Equal(t, 1, p.Report.Model.Applied)
	assert.Equal(t, 1, p.Report.Model.Unions)
	assert.Equal(t, 1, p.Report.Rules["model"])

	again := w.pass()
	assert.Zero(t, again.Changes())
	assert.Equal(t, applied(id), w.state(id))
	assert.Equal(t, a, again.EntityOf(500))
	assert.Empty(t, again.Revivals())
}

// A book decision moves the bucket book to the full name, by method model.
func TestABookDecisionMovesTheBook(t *testing.T) {
	w := newWorld(t, newChurn(t, 40))
	w.pass()
	bucket := w.last.EntityOf(620)
	id := w.decide(authorentity.CandidateBucket, 620, 600)
	p := w.pass()
	assert.Equal(t, applied(id), w.state(id))
	assert.Equal(t, p.EntityOf(600), p.EntityOf(620))
	assert.Equal(t, "model:bucket", p.RuleOf(620))
	row := w.s.Credits[authorentity.CreditKey{CreditID: 620}]
	assert.Equal(t, authorentity.MethodModel, row.Method)
	assert.Equal(t, 1, p.Report.Model.BookMoves)
	assert.Zero(t, w.pass().Changes())
	// Undone, the book goes back to its bucket; no ID changes.
	w.revoke(id)
	back := w.pass()
	assert.Equal(t, bucket, back.EntityOf(620))
	_, kept := w.s.Credits[authorentity.CreditKey{CreditID: 620}]
	assert.False(t, kept)
}

// Every churn case of §4.2 end to end: after a pair and a book decision
// applied, a change either keeps them applied or lapses them with the
// stated reason; a lapsed pair splits with the absorbed ID revived, a lapsed
// book goes back to its bucket.
func TestEveryChurnCaseAppliesOrLapses(t *testing.T) {
	const kept = ""
	type tc struct {
		name       string
		big        int
		change     func(w *world)
		pair, item string // kept, or the lapse reason
	}
	for _, c := range []tc{
		{"1 nothing", 40, func(*world) {}, kept, kept},
		{"2 a large side gains a book outside the sample, its class kept", 40, func(w *world) {
			w.ch.c.book(41, "Роман 41").credit(41, 41, "fp-big", person("Кинг", "Стивен"))
		}, kept, kept},
		{"3 a small side gains a book", 40, func(w *world) {
			w.ch.c.book(501, "Ещё рассказы").credit(501, 501, "fp-small", person("Стивен", "Кинг"))
		}, authorentity.LapsedInputChanged, kept},
		{"4 the size class changes", 10, func(w *world) {
			w.ch.c.book(11, "Роман 11").credit(11, 11, "fp-big", person("Кинг", "Стивен"))
		}, authorentity.LapsedInputChanged, kept},
		{"5 a sample book's field is edited", 40, func(w *world) {
			w.ch.fields[3] = authorentity.CardBookFields{Year: "1981", Genres: []string{"horror"}}
		}, authorentity.LapsedInputChanged, kept},
		{"5 a sample book's title is edited", 40, func(w *world) {
			w.ch.c.in.Books[3] = authorentity.Book{Title: "Роман три"}
		}, authorentity.LapsedInputChanged, kept},
		{"6 a side gains a fingerprint", 40, func(w *world) {
			w.ch.c.book(502, "Повести").credit(502, 502, "fp-small-2", person("СТИВЕН", "Кинг"))
		}, authorentity.LapsedMembersChanged, kept},
		{"7 an extractor bump", 40, func(w *world) {
			for i := range w.ch.c.in.Credits {
				w.ch.c.in.Credits[i].ExtractorVersion = "v2"
			}
		}, authorentity.LapsedExtractorMoved, authorentity.LapsedExtractorMoved},
		{"8 a normalizer bump with the rendered fields unchanged", 40, func(w *world) {
			r := *w.ch.credit(500).Result
			r.ResultID += 1000
			w.ch.credit(500).Result = &r
		}, kept, kept},
		{"9 a member's rendered name changes", 40, func(w *world) {
			r := *w.ch.credit(500).Result
			r.DisplayName = "Кинг, Стивен"
			w.ch.credit(500).Result = &r
		}, authorentity.LapsedInputChanged, kept},
		{"11 D3 moves a book into a large side outside its sample", 40, func(w *world) {
			w.ch.c.book(650, "Повесть 600").credit(650, 650, "fp-bucket", person("Иванов", "Алексей"))
		}, kept, kept},
		{"12 D3 moves a book into a side's sample", 40, func(w *world) {
			w.ch.c.book(599, "Повесть 601").credit(599, 599, "fp-bucket", person("Иванов", "Алексей"))
		}, kept, authorentity.LapsedInputChanged},
		{"13 a Wikidata merge joins a side with a third", 40, func(w *world) {
			w.ch.c.book(700, "It").credit(700, 700, "fp-nick", shown("Стивен Кинг", nickOnly("Стивен Кинг")))
			w.ch.c.with(newReference().item("Q39829", []string{"Стивен Кинг"}))
		}, authorentity.LapsedMembersChanged, kept},
		{"14 a manual row fixes a member's credit", 40, func(w *world) {
			id := uuid.New()
			w.s.Entities[id] = authorentity.EntityRow{ID: id, Kind: "person", DisplayName: "Стивен Кинг"}
			key := authorentity.CreditKey{CreditID: 2}
			w.s.Credits[key] = authorentity.CreditRow{Key: key, EntityID: &id, Method: authorentity.MethodManual, Rule: "manual"}
		}, authorentity.ExcludedManualSide, kept},
		{"15 the bucket credit is re-selected with another name", 40, func(w *world) {
			r := *w.ch.credit(620).Result
			r.DisplayName = "Алексей ИВАНОВ"
			w.ch.credit(620).Result = &r
		}, kept, authorentity.LapsedInputChanged},
		{"15 the bucket credit is deleted", 40, func(w *world) {
			w.ch.c.in.Credits = slices.DeleteFunc(w.ch.c.in.Credits, func(c authorentity.Credit) bool { return c.ID == 620 })
		}, kept, authorentity.LapsedMembersChanged},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, newChurn(t, c.big))
			w.pass()
			a, b, bucket := w.last.EntityOf(1), w.last.EntityOf(500), w.last.EntityOf(620)
			pair := w.decide(authorentity.CandidateSwap, 1, 500)
			item := w.decide(authorentity.CandidateBucket, 620, 600)
			w.pass()
			require.Equal(t, applied(pair), w.state(pair))
			require.Equal(t, applied(item), w.state(item))
			c.change(w)
			p := w.pass()
			if c.pair == kept {
				assert.Equal(t, applied(pair), w.state(pair), "the pair")
				same(t, p, 1, 500)
			} else {
				assert.Equal(t, lapsed(pair, c.pair), w.state(pair), "the pair")
				if c.pair != authorentity.LapsedExtractorMoved {
					// An extractor bump re-keys every stored row: only a
					// revival matches its record without the version.
					assert.Equal(t, a, p.EntityOf(1))
				}
				assert.Equal(t, b, p.EntityOf(500), "the absorbed side revived")
				assert.False(t, w.merged(b, a))
			}
			if c.item == kept {
				assert.Equal(t, applied(item), w.state(item), "the bucket item")
				same(t, p, 620, 600)
			} else {
				assert.Equal(t, lapsed(item, c.item), w.state(item), "the bucket item")
				if p.EntityOf(620) != uuid.Nil {
					assert.Equal(t, bucket, p.EntityOf(620), "the book back in its bucket")
				}
			}
		})
	}
}

// A decision on the old members of a side that grew a fingerprint does not
// apply to the grown side: never on a subset (§4.2 case 6).
func TestAnOldDigestNeverAppliesToAGrownSide(t *testing.T) {
	w := newWorld(t, newChurn(t, 40))
	w.pass()
	w.ch.c.book(502, "Повести").credit(502, 502, "fp-small-2", person("СТИВЕН", "Кинг"))
	// Decided on the side before it grew, applied first after it.
	id := w.decide(authorentity.CandidateSwap, 1, 500)
	p := w.pass()
	assert.Equal(t, lapsed(id, authorentity.LapsedMembersChanged), w.state(id))
	apart(t, p, 1, 500)
	same(t, p, 500, 502)
}

// threePoselyagins is one name on three level-0 components, every pair of
// which is a swap candidate: the three words in three field splits, «Поселягин
// / Владимир / Геннадьевич» of 6 books, «Владимир / Поселягин / Геннадьевич»
// of 3, «Геннадьевич / Владимир / Поселягин» of 1.
func threePoselyagins(t *testing.T) *churn {
	ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
	for i := int64(1); i <= 6; i++ {
		ch.c.book(i, "Роман").credit(i, i, "fp-a", person("Поселягин", "Владимир", "Геннадьевич"))
	}
	for i := int64(11); i <= 13; i++ {
		ch.c.book(i, "Повесть").credit(i, i, "fp-b", person("Владимир", "Поселягин", "Геннадьевич"))
	}
	ch.c.book(21, "Рассказ").credit(21, 21, "fp-c", person("Геннадьевич", "Владимир", "Поселягин"))
	return ch
}

// A star of decisions (A–B, A–C) is one entity; overlapping decisions are
// unions, each pair gated on its own.
func TestAStarOfDecisionsApplies(t *testing.T) {
	w := newWorld(t, threePoselyagins(t))
	w.pass()
	a := w.last.EntityOf(1)
	ab := w.decide(authorentity.CandidateSwap, 1, 11)
	ac := w.decide(authorentity.CandidateSwap, 1, 21)
	p := w.pass()
	assert.Equal(t, applied(ab), w.state(ab))
	assert.Equal(t, applied(ac), w.state(ac))
	same(t, p, 1, 11)
	same(t, p, 1, 21)
	assert.Equal(t, a, p.EntityOf(21))
	assert.Equal(t, 2, p.Report.Model.Unions)
}

// A union that would join two components whose own pair was revoked is
// refused: every decision of the would-be component is in conflict.
func TestAUnionOverARevokedPairIsAConflict(t *testing.T) {
	w := newWorld(t, threePoselyagins(t))
	w.pass()
	bc := w.decide(authorentity.CandidateSwap, 11, 21)
	w.revoke(bc)
	ab := w.decide(authorentity.CandidateSwap, 1, 11)
	ac := w.decide(authorentity.CandidateSwap, 1, 21)
	p := w.pass()
	assert.Equal(t, conflict(ab, authorentity.ConflictRevoked), w.state(ab))
	assert.Equal(t, conflict(ac, authorentity.ConflictRevoked), w.state(ac))
	apart(t, p, 1, 11)
	apart(t, p, 1, 21)
	assert.Equal(t, 2, p.Report.Model.Conflict[authorentity.ConflictRevoked])
	// A decision outside that component still applies.
	w2 := newWorld(t, threePoselyagins(t))
	w2.pass()
	bc = w2.decide(authorentity.CandidateSwap, 11, 21)
	w2.revoke(bc)
	ab = w2.decide(authorentity.CandidateSwap, 1, 11)
	w2.pass()
	assert.Equal(t, applied(ab), w2.state(ab))
}

// A manual row wins: the decision on a side it fixes lapses manual_side
// and the row and its entity are left as they are.
func TestAManualRowWinsOverADecision(t *testing.T) {
	w := newWorld(t, newChurn(t, 40))
	w.pass()
	id := w.decide(authorentity.CandidateSwap, 1, 500)
	w.pass()
	manual := uuid.New()
	w.s.Entities[manual] = authorentity.EntityRow{ID: manual, Kind: "person", DisplayName: "Стивен Кинг"}
	key := authorentity.CreditKey{CreditID: 2}
	row := authorentity.CreditRow{Key: key, EntityID: &manual, Method: authorentity.MethodManual, Rule: "manual"}
	w.s.Credits[key] = row
	p := w.pass()
	assert.Equal(t, lapsed(id, authorentity.ExcludedManualSide), w.state(id))
	assert.Equal(t, manual, p.EntityOf(2))
	assert.Equal(t, row, w.s.Credits[key])
	apart(t, p, 1, 500)
}

// bucketBeside is a bucket book with series evidence for two full names,
// «Алексей Викторович Иванов» (V) and «Алексей Александрович Иванов» (A), so
// D3 leaves it; «Aleksey Viktorovich Ivanov» (X) is a co-author on it and a
// translit candidate of V.
func bucketBeside(t *testing.T) *churn {
	ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
	ch.c.book(1, "Первая", 7).credit(1, 1, "fp-v", person("Иванов", "Алексей", "Викторович")).
		book(2, "Вторая", 7).credit(2, 2, "fp-a", person("Иванов", "Алексей", "Александрович")).
		book(3, "Третья", 7).credit(3, 3, "fp-short", person("Иванов", "Алексей")).
		credit(4, 3, "fp-x", person("Ivanov", "Aleksey", "Viktorovich").latin()).
		book(5, "Четвёртая").credit(5, 5, "fp-x", person("Ivanov", "Aleksey", "Viktorovich").latin())
	return ch
}

// A book decision against D3 (§8 E4b): D3 run again over the joined
// components may move the book itself — to the decision's full name (one
// row, applied) or to another (the book stays where D3 put it, conflict) —
// and two decisions sending one book to two full names apply neither.
func TestABookDecisionAgainstD3(t *testing.T) {
	setup := func(t *testing.T) *world {
		w := newWorld(t, bucketBeside(t))
		p := w.pass()
		require.NotEqual(t, p.EntityOf(3), p.EntityOf(1), "D3 leaves the book")
		return w
	}
	t.Run("D3 agrees", func(t *testing.T) {
		w := setup(t)
		// Joined with X, which stands on the book, V is no target of D3: it
		// moves the book to A, as the decision does.
		vx := w.decide(authorentity.CandidateTranslit, 1, 4)
		book := w.decide(authorentity.CandidateBucket, 3, 2)
		p := w.pass()
		assert.Equal(t, applied(vx), w.state(vx))
		assert.Equal(t, applied(book), w.state(book))
		same(t, p, 3, 2)
		assert.Equal(t, "D3:series", p.RuleOf(3), "D3's row, one row")
		assert.Zero(t, p.Report.Model.BookMoves)
	})
	t.Run("D3 disagrees", func(t *testing.T) {
		w := setup(t)
		vx := w.decide(authorentity.CandidateTranslit, 1, 4)
		book := w.decide(authorentity.CandidateBucket, 3, 1)
		p := w.pass()
		assert.Equal(t, applied(vx), w.state(vx))
		assert.Equal(t, conflict(book, authorentity.ConflictD3Disagrees), w.state(book))
		same(t, p, 3, 2)
	})
	t.Run("two targets", func(t *testing.T) {
		w := setup(t)
		toV := w.decide(authorentity.CandidateBucket, 3, 1)
		toA := w.decide(authorentity.CandidateBucket, 3, 2)
		p := w.pass()
		assert.Equal(t, conflict(toV, authorentity.ConflictTwoTargets), w.state(toV))
		assert.Equal(t, conflict(toA, authorentity.ConflictTwoTargets), w.state(toA))
		apart(t, p, 3, 1)
		apart(t, p, 3, 2)
	})
}

// IDs are stable with 0, 1 and 2 decisions: the entities no decision
// touches keep theirs, a merge leaves the larger side's.
func TestIDsAreStableWithDecisions(t *testing.T) {
	w := newWorld(t, threePoselyagins(t))
	w.ch.c.book(30, "Анна Каренина").credit(30, 30, "fp-tolstoy", person("Толстой", "Лев", "Николаевич"))
	w.pass()
	ids := map[int64]uuid.UUID{}
	for _, c := range []int64{1, 11, 21, 30} {
		ids[c] = w.last.EntityOf(c)
	}
	w.decide(authorentity.CandidateSwap, 1, 11)
	p := w.pass()
	assert.Equal(t, ids[1], p.EntityOf(11))
	assert.Equal(t, ids[21], p.EntityOf(21))
	assert.Equal(t, ids[30], p.EntityOf(30))
	w.decide(authorentity.CandidateSwap, 1, 21)
	p = w.pass()
	assert.Equal(t, ids[1], p.EntityOf(21))
	assert.Equal(t, ids[30], p.EntityOf(30))
	assert.Zero(t, w.pass().Changes())
}

// A store with a model merge needs its census even with no decision live,
// and a plan without its census is refused, not decided.
func TestAModelMergeNeedsItsCensus(t *testing.T) {
	w, _, a, b := mergedPair(t, 40, nil)
	require.True(t, w.merged(b, a))
	w.ch.c.in.Old, w.ch.c.in.Decisions = w.s, nil
	p := w.ch.c.build()
	assert.True(t, p.NeedsMergeCensus())
	_, err := p.ApplyMergeDecisions()
	assert.ErrorIs(t, err, authorentity.ErrNoCandidates)
}

// A card that panics leaves only its own candidate out: the seal holds the
// rest, the failure is counted, and a decision on that candidate lapses
// card_failed, so its pair splits while every other pass goes on.
func TestOneFailedCardLapsesOnlyItsDecision(t *testing.T) {
	w, d, a, b := mergedPair(t, 40, nil)
	defer authorentity.PanicOnCardBook(w.ch.credit(500).BookID)()
	p := w.pass()
	assert.True(t, w.level0.MergeCandidatesSealed())
	assert.Equal(t, lapsed(d, authorentity.LapsedCardFailed), w.state(d))
	assert.Equal(t, a, p.EntityOf(1))
	assert.Equal(t, b, p.EntityOf(500))
	assert.Equal(t, 1, p.Report.Candidates.ItemsFailed)
	assert.NotEmpty(t, w.level0.MergeCandidates(), "the other candidates are sealed")
	assert.Equal(t, len(w.level0.MergeCandidates()), p.Report.Candidates.Candidates)
	byKind := 0
	for _, scripts := range p.Report.Candidates.ByKind {
		for _, c := range scripts {
			byKind += c.Candidates
		}
	}
	assert.Equal(t, p.Report.Candidates.Candidates, byKind)
	for _, c := range w.level0.MergeCandidates() {
		assert.NotEqual(t, w.decisions[0].Digest, c.Digest)
	}
}
