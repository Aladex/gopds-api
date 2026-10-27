package services_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"gopds-api/database"
	"gopds-api/internal/authorentity"
	"gopds-api/internal/authornorm"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The durable veto of a revoke (plan §4.11, E4b-3), end to end: the revoke
// records each side's books and raw fingerprints, every pass keeps the
// sides apart and stores what it found of the veto, and only an
// administrator's retire or re-anchor releases it.

// vetoedCatalog is the swap pair merged by a decision and revoked: «Стивен
// Кинг» (0x61, two books) and «Кинг Стивен» (0x62, one book), apart again.
type vetoedCatalog struct {
	*entityCatalog
	big, small     string
	revoke, vetoID int64
}

func newVetoedCatalog(t *testing.T) *vetoedCatalog {
	t.Helper()
	v, id := newMergedCatalog(t)
	v.revokeDecision(id)
	return v
}

// newMergedCatalog is the swap pair merged by a decision, not revoked yet;
// it returns the decision.
func newMergedCatalog(t *testing.T) (v *vetoedCatalog, decision int64) {
	t.Helper()
	c := newEntityCatalog(t)
	small := c.seedCandidates()
	c.build()
	v = &vetoedCatalog{entityCatalog: c, big: c.entityOf(0x61), small: c.entityOf(small)}
	decision = c.decideSwap()
	c.build()
	require.Equal(t, v.big, c.mergedInto(v.small))
	return v, decision
}

// revokeDecision revokes the decision and finds its veto.
func (v *vetoedCatalog) revokeDecision(id int64) {
	v.t.Helper()
	c := v.entityCatalog
	var err error
	v.revoke, err = database.RevokeMergeDecision(context.Background(), c.db, id, 0)
	require.NoError(v.t, err)
	_, err = c.db.QueryOne(pg.Scan(&v.vetoID), `SELECT id FROM author_entity_merge_veto WHERE revoke_decision_id = ?`,
		v.revoke)
	require.NoError(v.t, err)
}

// vetoState is the veto's stored state.
func (v *vetoedCatalog) vetoState(id int64) string {
	v.t.Helper()
	var state string
	var held, lost, gone, widened int
	_, err := v.db.QueryOne(pg.Scan(&state, &held, &lost, &gone, &widened), `SELECT state, held_books, lost_books,
		gone_books, widened_fingerprints FROM author_entity_merge_veto_state WHERE veto_id = ?`, id)
	require.NoError(v.t, err)
	return fmt.Sprintf("%s held=%d lost=%d gone=%d widened=%d", state, held, lost, gone, widened)
}

// unanchored is AuthorEntityVetoUnanchored.
func (v *vetoedCatalog) unanchored() bool {
	v.t.Helper()
	got, err := database.AuthorEntityVetoUnanchored(context.Background(), v.db)
	require.NoError(v.t, err)
	return got
}

// reextract gives the books of fill a new current snapshot under
// extractor-v2 whose author credit is newFill with the same names: the same
// fingerprint for a version-only bump, another for a rewrite.
func (v *vetoedCatalog) reextract(fill, newFill byte, family, given string) {
	v.t.Helper()
	var books []int64
	_, err := v.db.Query(&books, `SELECT DISTINCT s.book_id FROM book_contributor_credit c
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id AND s.is_current WHERE c.source_fingerprint = ?
		ORDER BY 1`, bytes.Repeat([]byte{fill}, 32))
	require.NoError(v.t, err)
	require.NotEmpty(v.t, books)
	for _, book := range books {
		v.creditV2(v.resnapshot(book), newFill, family, given)
	}
}

// creditV2 adds a selected author credit to an extractor-v2 snapshot, with a
// result of that extractor (one per fingerprint).
func (v *vetoedCatalog) creditV2(snap int64, fill byte, family, given string) {
	v.t.Helper()
	fp := bytes.Repeat([]byte{fill}, 32)
	display := given + " " + family
	var credit, result int64
	_, err := v.db.QueryOne(pg.Scan(&credit), `INSERT INTO book_contributor_credit
		(snapshot_id, role, position, source_first_name, source_last_name, source_display_name, source_fingerprint)
		VALUES (?, 'author', 0, ?, ?, ?, ?) RETURNING id`, snap, given, family, display, fp)
	require.NoError(v.t, err)
	_, err = v.db.Query(pg.Scan(&result), `SELECT id FROM contributor_normalization_result
		WHERE source_fingerprint = ? AND extractor_version = 'extractor-v2'`, fp)
	require.NoError(v.t, err)
	if result == 0 {
		key := append(bytes.Repeat([]byte{fill ^ 0xff}, 31), 0x02)
		_, err = v.db.QueryOne(pg.Scan(&result), `INSERT INTO contributor_normalization_result
			(source_fingerprint, normalization_key, extractor_version, normalizer_version, result_schema_version,
			 method, kind, status, decision_class, family_name, given_name, additional_names,
			 display_name, sort_name, search_key, script)
			VALUES (?, ?, 'extractor-v2', 'normalizer-v1', 'result-v1', 'rules', 'person', 'normalized',
			 'structured_person', ?, ?, '{}', ?, ?, ?, 'Cyrl') RETURNING id`,
			fp, key, family, given, display, display, authornorm.SearchKey(display))
		require.NoError(v.t, err)
	}
	v.exec(`INSERT INTO book_contributor_credit_selection
		(credit_id, source_fingerprint, state, result_id, basis, policy_version)
		VALUES (?, ?, 'selected', ?, 'automatic', '4')`, credit, fp, result)
}

// resnapshot replaces a book's current snapshot by a new extractor-v2 one
// without credits and returns it.
func (v *vetoedCatalog) resnapshot(book int64) int64 {
	v.t.Helper()
	return resnapshotOf(v.entityCatalog, book, "extractor-v2")
}

// resnapshotOf replaces a book's current snapshot by a new one of extractor
// version — the same version takes another file (md5) — without credits.
func resnapshotOf(c *entityCatalog, book int64, version string) int64 {
	c.t.Helper()
	c.exec(`UPDATE book_metadata_snapshot SET is_current = false WHERE book_id = ? AND is_current`, book)
	var snap int64
	_, err := c.db.QueryOne(pg.Scan(&snap), `INSERT INTO book_metadata_snapshot (book_id, book_md5, extractor_version,
			origin, outcome, archive_path, entry_name)
		SELECT book_id, md5(book_md5 || ?1), ?1, origin, outcome, archive_path, entry_name
		FROM book_metadata_snapshot WHERE book_id = ?0 ORDER BY id DESC LIMIT 1 RETURNING id`, book, version)
	require.NoError(c.t, err)
	return snap
}

// booksOf are the current books of a fingerprint.
func (v *vetoedCatalog) booksOf(fill byte) []int64 {
	v.t.Helper()
	var books []int64
	_, err := v.db.Query(&books, `SELECT DISTINCT s.book_id FROM book_contributor_credit c
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id AND s.is_current WHERE c.source_fingerprint = ?
		ORDER BY 1`, bytes.Repeat([]byte{fill}, 32))
	require.NoError(v.t, err)
	return books
}

// deleteBook deletes a book with its author layer, as a scan does.
func (v *vetoedCatalog) deleteBook(book int64) {
	v.t.Helper()
	require.NoError(v.t, v.db.RunInTransaction(context.Background(), func(tx *pg.Tx) error {
		for _, q := range []string{`DELETE FROM opds_catalog_bseries WHERE book_id = ?`,
			`DELETE FROM opds_catalog_bgenre WHERE book_id = ?`, `SELECT author_layer_delete_books(ARRAY[?]::bigint[])`,
			`DELETE FROM opds_catalog_book WHERE id = ?`} {
			if _, err := tx.Exec(q, book); err != nil {
				return err
			}
		}
		return nil
	}))
}

// swapExcluded is the swap stratum's revoked_pair exclusions of a pass.
func swapExcluded(rep *services.AuthorEntityBuildReport) int {
	counts := rep.Candidates.ByKind[authorentity.CandidateSwap]["Cyrl"]
	if counts == nil {
		return 0
	}
	return counts.Excluded[authorentity.ExcludedRevokedPair]
}

// A revoke records the veto: side a is the absorbing entity's two books,
// side b the absorbed one's book, each with the raw fingerprint. The next
// pass splits the pair, finds the veto anchored, excludes the pair as a
// candidate and stores the state; a pass after it rewrites nothing.
func TestARevokeIsAVetoInTheDatabase(t *testing.T) {
	v := newVetoedCatalog(t)
	assert.Equal(t, 2, v.count(fmt.Sprintf(`SELECT count(*) FROM author_entity_merge_veto_member
		WHERE veto_id = %d AND side = 'a' AND source_fingerprint = decode(repeat('61', 32), 'hex')`, v.vetoID)))
	assert.Equal(t, 1, v.count(fmt.Sprintf(`SELECT count(*) FROM author_entity_merge_veto_member
		WHERE veto_id = %d AND side = 'b' AND source_fingerprint = decode(repeat('62', 32), 'hex')`, v.vetoID)))
	assert.Positive(t, v.marks(), "a veto is build input")

	rep := v.build()
	assert.Equal(t, v.small, v.entityOf(0x62))
	assert.Empty(t, v.mergedInto(v.small))
	require.NotNil(t, rep.Veto)
	assert.Equal(t, map[string]int{authorentity.VetoAnchored: 1}, rep.Veto.States)
	assert.Equal(t, 1, swapExcluded(&rep), "the vetoed pair is no candidate")
	assert.Equal(t, "anchored held=3 lost=0 gone=0 widened=0", v.vetoState(v.vetoID))
	assert.False(t, v.unanchored())

	again := v.build()
	assert.Zero(t, again.Changes)
	assert.Nil(t, again.DecisionWrite, "an unchanged veto state is not rewritten")
	assert.Equal(t, 1, swapExcluded(&again))
}

// A version-only extractor bump keeps every raw fingerprint: the veto stays
// anchored and the pair stays apart (case 17).
func TestRevokeThenExtractorBump(t *testing.T) {
	v := newVetoedCatalog(t)
	v.build()
	v.reextract(0x61, 0x61, "Кинг", "Стивен")
	v.reextract(0x62, 0x62, "Стивен", "Кинг")
	rep := v.build()
	assert.Equal(t, "anchored held=3 lost=0 gone=0 widened=0", v.vetoState(v.vetoID))
	assert.Equal(t, 1, swapExcluded(&rep))
	assert.NotEqual(t, v.entityOf(0x61), v.entityOf(0x62))
	assert.False(t, v.unanchored())
}

// An extractor rewrite gives the big side's credits new raw fields: its
// books are lost, the veto unanchored and side a widened to the new
// fingerprint; the pair stays apart. A re-anchor on the two entities
// anchors it again on the new fingerprints, still apart.
func TestRevokeThenExtractorRewrite(t *testing.T) {
	v := newVetoedCatalog(t)
	v.build()
	v.reextract(0x61, 0x81, "Кинг", "Стивен")
	rep := v.build()
	assert.Equal(t, "unanchored held=1 lost=2 gone=0 widened=1", v.vetoState(v.vetoID))
	assert.Equal(t, 1, swapExcluded(&rep), "the widened side still excludes the pair")
	rewritten := v.entityOf(0x81)
	// A rewrite is a new fingerprint, which E1 gives a new entity (no stored
	// row names it); the plan's "A' keeps A's ID" does not hold, see the
	// report. B keeps its own, and the two stay apart.
	assert.NotEqual(t, v.big, rewritten)
	assert.Equal(t, v.small, v.entityOf(0x62))
	assert.True(t, v.unanchored())

	ctx := context.Background()
	_, err := database.ReanchorMergeVeto(ctx, v.db, v.vetoID, uuid.MustParse(rewritten), uuid.MustParse(rewritten), 0)
	assert.ErrorIs(t, err, database.ErrMergeVetoSides, "one entity is not two sides")
	_, err = database.ReanchorMergeVeto(ctx, v.db, v.vetoID, uuid.MustParse(v.entityOf(0x62)),
		uuid.MustParse(rewritten), 0)
	assert.ErrorIs(t, err, database.ErrMergeVetoSides, "the sides swapped")
	anchored, err := database.ReanchorMergeVeto(ctx, v.db, v.vetoID, uuid.MustParse(rewritten),
		uuid.MustParse(v.entityOf(0x62)), 0)
	require.NoError(t, err)
	assert.Equal(t, 1, v.count(fmt.Sprintf(`SELECT count(*) FROM author_entity_merge_veto
		WHERE id = %d AND retired_at IS NOT NULL`, v.vetoID)))
	again := v.build()
	assert.Equal(t, "anchored held=3 lost=0 gone=0 widened=0", v.vetoState(anchored))
	assert.Equal(t, 1, swapExcluded(&again))
	assert.False(t, v.unanchored(), "the retired veto's state holds nothing")
}

// A retire is the administrator's decision to let the pair be joined: the
// pair is a candidate again.
func TestARetiredVetoReleasesThePair(t *testing.T) {
	v := newVetoedCatalog(t)
	v.build()
	require.NoError(t, database.RetireMergeVeto(context.Background(), v.db, v.vetoID, 0))
	assert.ErrorIs(t, database.RetireMergeVeto(context.Background(), v.db, v.vetoID, 0), database.ErrMergeVetoNotLive)
	rep := v.build()
	assert.Zero(t, swapExcluded(&rep))
	assert.Equal(t, 1, rep.Candidates.ByKind[authorentity.CandidateSwap]["Cyrl"].Candidates)
	assert.Nil(t, rep.Veto)
}

// Continuity per recorded book: a deleted book is gone and keeps the veto
// anchored; a book that keeps other credits but lost the side's fingerprint
// unanchors it; a side whose every book is deleted makes it gone.
func TestVetoContinuityPerRecordedBook(t *testing.T) {
	t.Run("RecordedBookDeletedStaysAnchored", func(t *testing.T) {
		v := newVetoedCatalog(t)
		v.deleteBook(v.booksOf(0x61)[0])
		v.build()
		assert.Equal(t, "anchored held=2 lost=0 gone=1 widened=0", v.vetoState(v.vetoID))
		assert.False(t, v.unanchored())
	})
	t.Run("PersonRemovedFromBookUnanchors", func(t *testing.T) {
		v := newVetoedCatalog(t)
		book := v.booksOf(0x62)[0]
		v.creditV2(v.resnapshot(book), 0x91, "Страуб", "Питер")
		v.build()
		assert.Equal(t, "unanchored held=2 lost=1 gone=0 widened=1", v.vetoState(v.vetoID))
		assert.True(t, v.unanchored())
	})
	t.Run("VetoGoneWhenSideLeavesCatalog", func(t *testing.T) {
		v := newVetoedCatalog(t)
		for _, book := range v.booksOf(0x62) {
			v.deleteBook(book)
		}
		v.build()
		assert.Equal(t, "gone held=2 lost=0 gone=1 widened=0", v.vetoState(v.vetoID))
		assert.False(t, v.unanchored())
	})
}

// Two sides that share a raw fingerprint cannot be kept apart: the pass is
// refused whole, veto_overlap, its report naming the veto, nothing written.
func TestOverlappingVetoSidesHoldTheBuild(t *testing.T) {
	v := newVetoedCatalog(t)
	v.build()
	before := (&fencedCatalog{entityCatalog: v.entityCatalog}).layout()
	var overlap int64
	_, err := v.db.QueryOne(pg.Scan(&overlap), `INSERT INTO author_entity_merge_veto (revoke_decision_id, item_kind)
		VALUES (?, 'entity') RETURNING id`, v.revoke)
	require.NoError(t, err)
	book := v.booksOf(0x61)[0]
	v.exec(`INSERT INTO author_entity_merge_veto_member (veto_id, side, book_id, source_fingerprint)
		VALUES (?0, 'a', ?1, decode(repeat('61', 32), 'hex')), (?0, 'b', ?1, decode(repeat('61', 32), 'hex'))`,
		overlap, book)
	run, err := services.NewAuthorEntityAdmin(v.db).Start(context.Background(), 0)
	require.NoError(t, err)
	done := runUntilSettled(t, v.entityCatalog, run.ID)
	assert.Equal(t, database.AuthorEntityRunFailed, done.Status)
	require.NotNil(t, done.LastErrorClass)
	assert.Equal(t, services.AuthorEntityBuildErrorVetoOverlap, *done.LastErrorClass)
	var rep struct {
		Overlaps []authorentity.VetoOverlap `json:"veto_overlaps"`
	}
	require.NoError(t, json.Unmarshal(done.Report, &rep))
	assert.Equal(t, []authorentity.VetoOverlap{{VetoID: overlap, Shared: 1}}, rep.Overlaps)
	assert.Equal(t, before, (&fencedCatalog{entityCatalog: v.entityCatalog}).layout(), "the refused pass wrote")
}

// Review r1 blocker: an extraction that rewrites A's credits commits before
// the revoke. The veto records the books the decision was about (migration
// 65), so a rewritten book is lost and the side widens to the rewritten
// credits: A' (rewritten to B's very name, which D2 alone would join to B)
// stays apart from B — for a partial and a complete rewrite under a new
// extractor version, and for a rewrite under the same version (another
// file). The control retires the veto: A' and B are then one entity, so the
// veto is what keeps them apart.
func TestARevokeAfterARewriteStillVetoes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rewrite int // how many of A's two books
		version string
		state   string
		retire  bool
	}{
		{"partial", 1, "extractor-v2", "unanchored held=2 lost=1 gone=0 widened=1", false},
		{"complete", 2, "extractor-v2", "unanchored held=1 lost=2 gone=0 widened=1", false},
		{"same version after the decision", 1, "extractor-v1", "unanchored held=2 lost=1 gone=0 widened=1", false},
		{"control without the veto", 2, "extractor-v2", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, id := newMergedCatalog(t)
			for _, book := range v.booksOf(0x61)[:tc.rewrite] {
				snap := resnapshotOf(v.entityCatalog, book, tc.version)
				if tc.version == "extractor-v1" {
					v.credit(snap, 0x81, "Стивен", "Кинг")
				} else {
					v.creditV2(snap, 0x81, "Стивен", "Кинг")
				}
			}
			v.revokeDecision(id)
			assert.Equal(t, 2, v.count(fmt.Sprintf(`SELECT count(*) FROM author_entity_merge_veto_member
				WHERE veto_id = %d AND side = 'a'`, v.vetoID)), "both of A's books are recorded")
			if tc.retire {
				require.NoError(t, database.RetireMergeVeto(context.Background(), v.db, v.vetoID, 0))
			}
			v.build()
			if tc.retire {
				assert.Equal(t, v.entityOf(0x62), v.entityOf(0x81), "without the veto D2 joins A' and B")
				return
			}
			assert.Equal(t, tc.state, v.vetoState(v.vetoID))
			assert.NotEqual(t, v.entityOf(0x62), v.entityOf(0x81), "A' joined B across the veto")
			assert.True(t, v.unanchored())
			if tc.rewrite == 1 {
				assert.NotEqual(t, v.entityOf(0x62), v.entityOf(0x61))
			}
		})
	}
}

// Review r2 blocker: a book that carried A's fingerprint only in a snapshot
// replaced — under the same extractor version — before the decision was
// made is no book of the decision's side A. Book X was A's, then became B's;
// the decision, made after, is about X on side B alone. The veto records X
// on side B only: no false lost book, no false overlap, anchored.
func TestASnapshotReplacedBeforeTheDecisionIsNotRecorded(t *testing.T) {
	c := newEntityCatalog(t)
	small := c.seedCandidates()
	snapX := c.book("Икс")
	x := c.bookOf(snapX)
	c.credit(snapX, 0x61, "Кинг", "Стивен")
	c.build()
	c.credit(resnapshotOf(c, x, "extractor-v1"), small, "Стивен", "Кинг")
	c.build()
	v := &vetoedCatalog{entityCatalog: c, big: c.entityOf(0x61), small: c.entityOf(small)}
	id := c.decideSwap()
	c.build()
	require.Equal(t, v.big, c.mergedInto(v.small))
	v.revokeDecision(id)
	members := func(side string) int {
		return v.count(fmt.Sprintf(`SELECT count(*) FROM author_entity_merge_veto_member
			WHERE veto_id = %d AND side = '%s' AND book_id = %d`, v.vetoID, side, x))
	}
	assert.Zero(t, members("a"), "X recorded on side A from a snapshot replaced before the decision")
	assert.Equal(t, 1, members("b"))

	rep := v.build()
	assert.Equal(t, "anchored held=4 lost=0 gone=0 widened=0", v.vetoState(v.vetoID))
	assert.False(t, v.unanchored())
	require.NotNil(t, rep.Veto)
	assert.Empty(t, rep.Veto.Overlaps)
	assert.NotEqual(t, v.entityOf(0x61), v.entityOf(small))
}
