package database

import (
	"encoding/json"
	"testing"

	"gopds-api/internal/authornorm"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The phase-6 source repository tests: every property below is the plan's RED
// list for "Фаза 6" over a real PostgreSQL. The repository never owns a
// transaction: each test drives the boundary itself, and the failing proxy in
// the fixture file proves the repository neither begins nor commits.

// RED 1: PersistExtraction writes the snapshot, the ordered author and
// translator credits, and local jobs for the author credits, all inside the
// caller's transaction.
func TestPersistExtractionWritesSnapshotCreditsAndJobs(t *testing.T) {
	f := withAuthorSchemaTx(t)
	book := f.book()
	run := f.run(&runSpec{})

	authorOne := creditOf(t, models.ContributorRoleAuthor, "Анна", "Иванова")
	authorTwo := ExtractionCredit{
		Role: models.ContributorRoleAuthor,
		Source: nameValue(t,
			nameComponent(authornorm.ComponentFirst, "John"),
			nameComponent(authornorm.ComponentLast, "Smith"),
			// A present but empty nickname stays distinct from an absent one.
			nameComponent(authornorm.ComponentNickname, "")),
		QualityFlags:  []string{"duplicate_component"},
		XMLProvenance: json.RawMessage(`{"line":3}`),
	}
	// A translator with a source of its own: no job, but a full credit.
	translator := creditOf(t, models.ContributorRoleTranslator, "Пер", "Водчик")

	input := f.extraction(&extractionSpec{
		book:    book,
		origin:  models.BookMetadataSnapshotBackfill,
		run:     &run,
		credits: []ExtractionCredit{authorOne, authorTwo, translator},
	})

	result, err := PersistExtraction(f.tx, input)
	require.NoError(t, err)
	assert.Equal(t, PersistNewSnapshot, result.Outcome)
	assert.Equal(t, 3, result.CreditsWritten)
	assert.Equal(t, 2, result.JobsEnqueued, "one job per distinct author key; translators get none")

	var snapshot models.BookMetadataSnapshot
	require.NoError(t, f.tx.Model(&snapshot).Where("id = ?", result.SnapshotID).Select())
	assert.True(t, snapshot.IsCurrent)
	assert.Equal(t, book, snapshot.BookID)
	assert.Equal(t, bookMD5(book), snapshot.BookMD5)
	assert.Equal(t, "extractor-v1", snapshot.ExtractorVersion)
	assert.Equal(t, models.BookMetadataSnapshotBackfill, snapshot.Origin)
	require.NotNil(t, snapshot.RunID)
	assert.Equal(t, run, *snapshot.RunID)
	assert.Equal(t, models.BookMetadataSnapshotExtracted, snapshot.Outcome)
	assert.Equal(t, "fixture.zip", snapshot.ArchivePath)
	assert.JSONEq(t, `{"root":"FictionBook"}`, string(snapshot.XMLProvenance))
	assert.Equal(t, ptr("Title"), snapshot.SourceTitle)
	assert.Equal(t, ptr(""), snapshot.SourceLang, "a present empty lang stays distinct from NULL")
	assert.Nil(t, snapshot.SourceSrcLang)
	assert.Equal(t, []string{"", "978-5-1"}, snapshot.SourceISBNs)
	assert.Nil(t, snapshot.SourcePublisher)
	assert.Equal(t, ptr("Город"), snapshot.SourceCity)
	assert.Equal(t, ptr("2001"), snapshot.SourceYear)
	assert.Equal(t, ptr("doc-id"), snapshot.SourceDocumentID)
	assert.Equal(t, ptr("1.1"), snapshot.SourceDocumentVersion)
	assert.JSONEq(t, `[{"name":"S","number":"1"}]`, string(snapshot.SourceSequences))
	assert.Equal(t, []string{"flag"}, snapshot.QualityFlags)
	assert.False(t, snapshot.CreatedAt.IsZero())

	var credits []models.BookContributorCredit
	require.NoError(t, f.tx.Model(&credits).
		Where("snapshot_id = ?", result.SnapshotID).Order("id").Select())
	require.Len(t, credits, 3)

	one := authornorm.SourceFingerprint(authorOne.Source)
	two := authornorm.SourceFingerprint(authorTwo.Source)
	assert.Equal(t, models.ContributorRoleAuthor, credits[0].Role)
	assert.Zero(t, credits[0].Position)
	assert.Equal(t, ptr("Анна"), credits[0].SourceFirstName)
	assert.Nil(t, credits[0].SourceMiddleName)
	assert.Equal(t, ptr("Иванова"), credits[0].SourceLastName)
	assert.Nil(t, credits[0].SourceNickname)
	assert.Equal(t, "Анна Иванова", credits[0].SourceDisplayName)
	assert.Equal(t, one[:], credits[0].SourceFingerprint)
	assert.Empty(t, credits[0].QualityFlags)

	assert.Equal(t, models.ContributorRoleAuthor, credits[1].Role)
	assert.Equal(t, 1, credits[1].Position, "positions count within the role, in XML order")
	assert.Equal(t, ptr(""), credits[1].SourceNickname, "present and empty, not NULL")
	assert.Equal(t, "John Smith", credits[1].SourceDisplayName)
	assert.Equal(t, two[:], credits[1].SourceFingerprint)
	assert.Equal(t, []string{"duplicate_component"}, credits[1].QualityFlags)
	assert.JSONEq(t, `{"line":3}`, string(credits[1].XMLProvenance))

	assert.Equal(t, models.ContributorRoleTranslator, credits[2].Role)
	assert.Zero(t, credits[2].Position, "positions restart within each role")

	jobKeyOf := func(credit ExtractionCredit) [32]byte {
		key, keyErr := authornorm.NormalizationKey(
			authornorm.SourceFingerprint(credit.Source), "extractor-v1", "normalizer-v1")
		require.NoError(t, keyErr)
		return key
	}
	oneKey, twoKey := jobKeyOf(authorOne), jobKeyOf(authorTwo)
	assert.Equal(t, 1, f.jobCountByKey(oneKey))
	assert.Equal(t, 1, f.jobCountByKey(twoKey))

	job := new(models.ContributorNormalizationJob)
	require.NoError(t, f.tx.Model(job).Where("normalization_key = ?", oneKey[:]).Select())
	assert.Equal(t, one[:], job.SourceFingerprint)
	assert.Equal(t, "extractor-v1", job.ExtractorVersion)
	assert.Equal(t, "normalizer-v1", job.NormalizerVersion)
	assert.Equal(t, models.NormalizationJobPending, job.Status)
	assert.Nil(t, job.ResultID)
	translatorKey, err := authornorm.NormalizationKey(
		authornorm.SourceFingerprint(translator.Source), "extractor-v1", "normalizer-v1")
	require.NoError(t, err)
	assert.Equal(t, 0, f.jobCountByKey(translatorKey), "translators get no local job")
}

// RED 2: a failure after any step rolls the whole write back and leaves the
// previous current snapshot in place.
func TestPersistExtractionFailureRollsBackEverything(t *testing.T) {
	f := withAuthorSchemaTx(t)
	book := f.book()
	run := f.run(&runSpec{})
	author := creditOf(t, models.ContributorRoleAuthor, "Первая", "Версия")

	first, err := PersistExtraction(f.tx, f.extraction(&extractionSpec{
		book: book, origin: models.BookMetadataSnapshotBackfill, run: &run,
		credits: []ExtractionCredit{author},
	}))
	require.NoError(t, err)
	require.Equal(t, PersistNewSnapshot, first.Outcome)

	for _, injection := range []struct {
		name   string
		quotas map[string]int
	}{
		{"before anything is written", map[string]int{"UPDATE book_metadata_snapshot": 0}},
		{"after the snapshot step", map[string]int{"INSERT INTO book_contributor_credit": 0}},
		{"after the credit step", map[string]int{"INSERT INTO contributor_normalization_job": 0}},
	} {
		t.Run(injection.name, func(t *testing.T) {
			sf := f.on(t)
			sf.exec("SAVEPOINT phase6_inject")
			proxy := newFailAfterDBI(t, sf.tx, injection.quotas)

			next, err := authornorm.NormalizationKey(
				authornorm.SourceFingerprint(creditOf(t, models.ContributorRoleAuthor, "Вторая", "Версия").Source),
				"extractor-v1", "normalizer-v1")
			require.NoError(t, err)

			_, err = PersistExtraction(proxy, sf.extraction(&extractionSpec{
				book:   book,
				md5:    bookMD5(book + 1_000),
				origin: models.BookMetadataSnapshotBackfill, run: &run,
				credits: []ExtractionCredit{creditOf(t, models.ContributorRoleAuthor, "Вторая", "Версия")},
			}))
			require.ErrorIs(t, err, errInjectedFailure)

			// The caller rolls back; nothing of the failed attempt survives.
			sf.exec("ROLLBACK TO SAVEPOINT phase6_inject")
			sf.exec("RELEASE SAVEPOINT phase6_inject")

			assert.Equal(t, 1, sf.snapshotCount(book), "the failed attempt left no snapshot")
			assert.Equal(t, []int64{first.SnapshotID}, sf.currentSnapshots(book),
				"the previous current snapshot stays current")
			assert.Equal(t, 1, sf.count(`SELECT count(*) FROM book_contributor_credit c
				JOIN book_metadata_snapshot s ON s.id = c.snapshot_id WHERE s.book_id = ?`, book))
			assert.Equal(t, 0, sf.jobCountByKey(next), "the failed attempt left no job")
		})
	}
}

// RED 3: an exact duplicate returns already_current, the previous ID, and
// zero new rows.
func TestPersistExtractionDuplicateIsAlreadyCurrent(t *testing.T) {
	f := withAuthorSchemaTx(t)
	book := f.book()
	run := f.run(&runSpec{})
	credits := []ExtractionCredit{creditOf(t, models.ContributorRoleAuthor, "Тот", "Же")}

	first, err := PersistExtraction(f.tx, f.extraction(&extractionSpec{
		book: book, origin: models.BookMetadataSnapshotBackfill, run: &run, credits: credits,
	}))
	require.NoError(t, err)
	require.Equal(t, PersistNewSnapshot, first.Outcome)

	// Even with an extra translator the key does not change, and nothing new
	// is written: the credits of an identical extraction already exist.
	duplicate := f.extraction(&extractionSpec{
		book: book, origin: models.BookMetadataSnapshotBackfill, run: &run,
		credits: append(append([]ExtractionCredit{}, credits...),
			creditOf(t, models.ContributorRoleTranslator, "Лишний", "Перевод")),
	})
	second, err := PersistExtraction(f.tx, duplicate)
	require.NoError(t, err)
	assert.Equal(t, PersistAlreadyCurrent, second.Outcome)
	assert.Equal(t, first.SnapshotID, second.SnapshotID)
	assert.Zero(t, second.CreditsWritten)
	assert.Zero(t, second.JobsEnqueued)

	assert.Equal(t, 1, f.snapshotCount(book))
	assert.Equal(t, []int64{first.SnapshotID}, f.currentSnapshots(book))
	duplicateFingerprint := authornorm.SourceFingerprint(credits[0].Source)
	assert.Equal(t, 1, f.creditCountByFingerprint(duplicateFingerprint[:]))
	duplicateKey, err := authornorm.NormalizationKey(duplicateFingerprint, "extractor-v1", "normalizer-v1")
	require.NoError(t, err)
	assert.Equal(t, 1, f.jobCountByKey(duplicateKey))
}

// RED 4: a new MD5 or extractor version switches the current marker
// atomically; the old history stays.
func TestPersistExtractionSwitchesCurrentAtomically(t *testing.T) {
	f := withAuthorSchemaTx(t)
	book := f.book()
	credit := []ExtractionCredit{creditOf(t, models.ContributorRoleAuthor, "Автор", "Книги")}
	persist := func(spec *extractionSpec) PersistExtractionResult {
		result, err := PersistExtraction(f.tx, f.extraction(spec))
		require.NoError(t, err)
		return result
	}

	v1 := persist(&extractionSpec{book: book, credits: credit})
	assert.Equal(t, []int64{v1.SnapshotID}, f.currentSnapshots(book))

	// A changed file: new MD5, same extractor.
	v2 := persist(&extractionSpec{book: book, md5: bookMD5(book + 1_000), credits: credit})
	assert.Equal(t, PersistNewSnapshot, v2.Outcome)
	assert.NotEqual(t, v1.SnapshotID, v2.SnapshotID)
	assert.Equal(t, []int64{v2.SnapshotID}, f.currentSnapshots(book))
	assert.Equal(t, 2, f.snapshotCount(book), "the superseded snapshot stays as history")

	// A new extractor version over the same file.
	v3 := persist(&extractionSpec{
		book: book, md5: bookMD5(book + 1_000), extractor: "extractor-v2", credits: credit})
	assert.Equal(t, []int64{v3.SnapshotID}, f.currentSnapshots(book))
	assert.Equal(t, 3, f.snapshotCount(book))

	// The file returns to its first version: the same key persists again,
	// becomes current once more, and still writes nothing new. The three
	// versioned snapshots keep one credit each.
	again := persist(&extractionSpec{book: book, credits: credit})
	assert.Equal(t, PersistAlreadyCurrent, again.Outcome)
	assert.Equal(t, v1.SnapshotID, again.SnapshotID)
	assert.Equal(t, []int64{v1.SnapshotID}, f.currentSnapshots(book))
	assert.Equal(t, 3, f.snapshotCount(book))
	fingerprint := authornorm.SourceFingerprint(credit[0].Source)
	assert.Equal(t, 3, f.creditCountByFingerprint(fingerprint[:]))
}

// raceResult is one racer's PersistExtraction outcome: a committed success
// or a rolled-back error.
type raceResult struct {
	result PersistExtractionResult
	err    error
}

// racePersistExtraction runs two PersistExtraction calls, each in its own
// real transaction from its own goroutine, behind a start barrier: both
// transactions are open before either call begins. Successes are committed by
// the racer — the caller role the repository leaves the boundary to — and
// failures roll back. Outcomes return in completion order.
func racePersistExtraction(t *testing.T, scratch *pg.DB, first, second *ExtractionInput) []raceResult {
	t.Helper()
	ready := make(chan struct{}, 2)
	start := make(chan struct{})
	results := make(chan raceResult, 2)
	racer := func(input *ExtractionInput) {
		go func() {
			tx, err := scratch.Begin()
			if err != nil {
				results <- raceResult{err: err}
				return
			}
			ready <- struct{}{}
			<-start
			result, err := PersistExtraction(tx, input)
			if err != nil {
				_ = tx.Rollback()
				results <- raceResult{err: err}
				return
			}
			if err := tx.Commit(); err != nil {
				results <- raceResult{err: err}
				return
			}
			results <- raceResult{result: result}
		}()
	}
	racer(first)
	racer(second)
	<-ready
	<-ready
	close(start)
	return []raceResult{<-results, <-results}
}

// RED 5: two concurrent writers with two real transactions and a barrier
// converge on one current snapshot and one complete result set. Run with
// -count=20: the command is recorded in the phase report. The race commits
// its winner, so the whole test runs in a scratch database that is dropped
// afterwards: nothing it commits ever touches the integration database.
func TestPersistExtractionConcurrentWriters(t *testing.T) {
	s := raceDB(t)

	for _, scenario := range []struct {
		name    string
		sameKey bool
	}{
		{"same extraction key", true},
		{"different extractor versions", false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			author := creditOf(t, models.ContributorRoleAuthor, "Гонка", "Писатель")
			fingerprint := authornorm.SourceFingerprint(author.Source)
			keys := [2][32]byte{}
			for i, extractor := range []string{"extractor-v1", "extractor-v2"} {
				key, err := authornorm.NormalizationKey(fingerprint, extractor, "normalizer-v1")
				require.NoError(t, err)
				keys[i] = key
			}
			// The scenarios share one scratch database, and jobs are keyed
			// by normalization input alone, so each scenario clears its own
			// jobs for the next one.
			t.Cleanup(func() {
				for _, key := range keys {
					_, _ = s.Exec(`DELETE FROM contributor_normalization_job WHERE normalization_key = ?`, key[:])
				}
			})

			// The book must be visible to both transactions, so it is
			// committed before the race; everything else stays in the racers'
			// own transactions.
			book := nextCommittedBook(t, s, bookMD5(0))

			inputFor := func(extractor string) *ExtractionInput {
				return &ExtractionInput{
					BookID:           book,
					BookMD5:          bookMD5(0),
					ExtractorVersion: extractor, NormalizerVersion: "normalizer-v1",
					Origin:      models.BookMetadataSnapshotLive,
					Outcome:     models.BookMetadataSnapshotExtracted,
					ArchivePath: "race.zip", EntryName: "race.fb2",
					XMLProvenance: json.RawMessage(`{}`), SourceISBNs: []string{},
					SourceSequences: json.RawMessage(`[]`), QualityFlags: []string{},
					Credits: []ExtractionCredit{author},
				}
			}

			second := "extractor-v1"
			if !scenario.sameKey {
				second = "extractor-v2"
			}
			outcomes := racePersistExtraction(t, s, inputFor("extractor-v1"), inputFor(second))

			var winner *raceResult
			currentCount, duplicateCount, conflictCount := 0, 0, 0
			for i := range outcomes {
				o := &outcomes[i]
				switch {
				case o.err != nil:
					require.ErrorIs(t, o.err, ErrSnapshotCurrentConflict,
						"the losing writer reports the typed conflict, got: %v", o.err)
					conflictCount++
				case o.result.Outcome == PersistNewSnapshot:
					winner = o
					currentCount++
				case o.result.Outcome == PersistAlreadyCurrent:
					duplicateCount++
				default:
					t.Fatalf("unexpected race outcome: %+v", o.result)
				}
			}
			require.Equal(t, 1, currentCount, "exactly one writer writes the snapshot")
			if scenario.sameKey {
				assert.Equal(t, 1, duplicateCount, "the other writer converges on already_current")
				assert.Zero(t, conflictCount)
				for _, o := range outcomes {
					if o.result.Outcome == PersistAlreadyCurrent {
						assert.Equal(t, winner.result.SnapshotID, o.result.SnapshotID)
					}
				}
			} else {
				assert.Equal(t, 1, conflictCount, "the other writer reports the conflict")
				assert.Zero(t, duplicateCount)
			}

			// The database ends with exactly one current snapshot and one
			// complete result set, whichever racer won.
			var current []int64
			_, err := s.Query(&current, `SELECT id FROM book_metadata_snapshot WHERE book_id = ? AND is_current`, book)
			require.NoError(t, err)
			assert.Equal(t, []int64{winner.result.SnapshotID}, current)

			var snapshots int
			_, err = s.QueryOne(pg.Scan(&snapshots), `SELECT count(*) FROM book_metadata_snapshot WHERE book_id = ?`, book)
			require.NoError(t, err)
			assert.Equal(t, 1, snapshots, "the loser left no half-written snapshot")

			var credits int
			_, err = s.QueryOne(pg.Scan(&credits), `SELECT count(*) FROM book_contributor_credit c
				JOIN book_metadata_snapshot s ON s.id = c.snapshot_id WHERE s.book_id = ?`, book)
			require.NoError(t, err)
			assert.Equal(t, 1, credits)

			if scenario.sameKey {
				var jobs int
				_, err = s.QueryOne(pg.Scan(&jobs), `SELECT count(*) FROM contributor_normalization_job
					WHERE normalization_key = ?`, keys[0][:])
				require.NoError(t, err)
				assert.Equal(t, 1, jobs)
			} else {
				won, lost := 0, 0
				for _, i := range []int{0, 1} {
					var jobs int
					_, err = s.QueryOne(pg.Scan(&jobs), `SELECT count(*) FROM contributor_normalization_job
						WHERE normalization_key = ?`, keys[i][:])
					require.NoError(t, err)
					if jobs == 1 {
						won++
					} else {
						lost++
					}
				}
				assert.Equal(t, 1, won, "only the winner's key has a job")
				assert.Equal(t, 1, lost)
			}
		})
	}

	// The reactivation path: a committed history (v1 superseded by v2), then
	// one racer re-extracts v1 exactly — the file reverted — while another
	// writes a brand-new v3. The loser must report the same typed conflict as
	// the new-snapshot path: a reactivation's marker switch is a switch like
	// any other.
	t.Run("historical reactivation races a new version", func(t *testing.T) {
		author := creditOf(t, models.ContributorRoleAuthor, "Возврат", "Истории")
		fingerprint := authornorm.SourceFingerprint(author.Source)
		keys := map[string][32]byte{}
		for _, extractor := range []string{"extractor-v1", "extractor-v2", "extractor-v3"} {
			key, err := authornorm.NormalizationKey(fingerprint, extractor, "normalizer-v1")
			require.NoError(t, err)
			keys[extractor] = key
		}
		t.Cleanup(func() {
			for _, key := range keys {
				_, _ = s.Exec(`DELETE FROM contributor_normalization_job WHERE normalization_key = ?`, key[:])
			}
		})

		book := nextCommittedBook(t, s, bookMD5(0))
		inputFor := func(extractor string) *ExtractionInput {
			return &ExtractionInput{
				BookID:           book,
				BookMD5:          bookMD5(0),
				ExtractorVersion: extractor, NormalizerVersion: "normalizer-v1",
				Origin:      models.BookMetadataSnapshotLive,
				Outcome:     models.BookMetadataSnapshotExtracted,
				ArchivePath: "race.zip", EntryName: "race.fb2",
				XMLProvenance: json.RawMessage(`{}`), SourceISBNs: []string{},
				SourceSequences: json.RawMessage(`[]`), QualityFlags: []string{},
				Credits: []ExtractionCredit{author},
			}
		}

		// Committed history: v1 written, then superseded by v2.
		commit := func(extractor string) PersistExtractionResult {
			tx, err := s.Begin()
			require.NoError(t, err)
			result, err := PersistExtraction(tx, inputFor(extractor))
			require.NoError(t, err)
			require.NoError(t, tx.Commit())
			return result
		}
		v1 := commit("extractor-v1")
		v2 := commit("extractor-v2")
		require.NotEqual(t, v1.SnapshotID, v2.SnapshotID)

		outcomes := racePersistExtraction(t, s, inputFor("extractor-v1"), inputFor("extractor-v3"))

		var winner *raceResult
		written, conflicts := 0, 0
		for i := range outcomes {
			o := &outcomes[i]
			if o.err != nil {
				require.ErrorIs(t, o.err, ErrSnapshotCurrentConflict,
					"the losing switch reports the typed conflict, got: %v", o.err)
				conflicts++
				continue
			}
			require.Contains(t, []PersistOutcome{PersistNewSnapshot, PersistAlreadyCurrent}, o.result.Outcome)
			winner = o
			written++
		}
		require.Equal(t, 1, written, "exactly one switch wins")
		require.Equal(t, 1, conflicts, "the other switch reports the conflict")

		reactivated := winner.result.Outcome == PersistAlreadyCurrent
		if reactivated {
			assert.Equal(t, v1.SnapshotID, winner.result.SnapshotID, "the reactivation wins")
		}

		var current []int64
		_, err := s.Query(&current, `SELECT id FROM book_metadata_snapshot WHERE book_id = ? AND is_current`, book)
		require.NoError(t, err)
		assert.Equal(t, []int64{winner.result.SnapshotID}, current)

		versions := 2
		if !reactivated {
			versions = 3
		}
		var snapshots int
		_, err = s.QueryOne(pg.Scan(&snapshots), `SELECT count(*) FROM book_metadata_snapshot WHERE book_id = ?`, book)
		require.NoError(t, err)
		assert.Equal(t, versions, snapshots, "the loser left no half-written snapshot; history stays")

		var credits int
		_, err = s.QueryOne(pg.Scan(&credits), `SELECT count(*) FROM book_contributor_credit c
			JOIN book_metadata_snapshot s ON s.id = c.snapshot_id WHERE s.book_id = ?`, book)
		require.NoError(t, err)
		assert.Equal(t, versions, credits)

		for extractor, key := range keys {
			var jobs int
			_, err = s.QueryOne(pg.Scan(&jobs), `SELECT count(*) FROM contributor_normalization_job
				WHERE normalization_key = ?`, key[:])
			require.NoError(t, err)
			want := 1
			if extractor == "extractor-v3" && reactivated {
				want = 0
			}
			assert.Equal(t, want, jobs, "jobs of %s", extractor)
		}
	})
}

// RED 6: extracted_no_author admits translators but creates zero
// normalization jobs.
func TestPersistExtractionNoAuthorOutcomeKeepsTranslatorsWithoutJobs(t *testing.T) {
	f := withAuthorSchemaTx(t)
	book := f.book()
	translator := creditOf(t, models.ContributorRoleTranslator, "Только", "Переводчик")

	result, err := PersistExtraction(f.tx, f.extraction(&extractionSpec{
		book:    book,
		outcome: models.BookMetadataSnapshotExtractedNoAuthor,
		credits: []ExtractionCredit{translator},
	}))
	require.NoError(t, err)
	assert.Equal(t, PersistNewSnapshot, result.Outcome)
	assert.Equal(t, 1, result.CreditsWritten)
	assert.Zero(t, result.JobsEnqueued)

	var snapshot models.BookMetadataSnapshot
	require.NoError(t, f.tx.Model(&snapshot).Where("id = ?", result.SnapshotID).Select())
	assert.Equal(t, models.BookMetadataSnapshotExtractedNoAuthor, snapshot.Outcome)
	translatorFingerprint := authornorm.SourceFingerprint(translator.Source)
	assert.Equal(t, 1, f.creditCountByFingerprint(translatorFingerprint[:]))

	key, err := authornorm.NormalizationKey(translatorFingerprint, "extractor-v1", "normalizer-v1")
	require.NoError(t, err)
	assert.Equal(t, 0, f.jobCountByKey(key))
}

// RED 7: the same source fingerprint in different books and roles creates
// distinct credits and one shared key.
func TestPersistExtractionSharedFingerprintDistinctCredits(t *testing.T) {
	f := withAuthorSchemaTx(t)
	bookOne, bookTwo, bookThree := f.book(), f.book(), f.book()
	source := creditOf(t, models.ContributorRoleAuthor, "Общий", "Источник")
	fingerprint := authornorm.SourceFingerprint(source.Source)
	key, err := authornorm.NormalizationKey(fingerprint, "extractor-v1", "normalizer-v1")
	require.NoError(t, err)

	first, err := PersistExtraction(f.tx, f.extraction(&extractionSpec{
		book: bookOne, credits: []ExtractionCredit{source}}))
	require.NoError(t, err)
	assert.Equal(t, 1, first.JobsEnqueued)

	// Another book, the same author source: a second credit, no second job.
	second, err := PersistExtraction(f.tx, f.extraction(&extractionSpec{
		book: bookTwo, credits: []ExtractionCredit{source}}))
	require.NoError(t, err)
	assert.Equal(t, PersistNewSnapshot, second.Outcome)
	assert.Equal(t, 0, second.JobsEnqueued, "the key is shared")

	assert.Equal(t, 2, f.creditCountByFingerprint(fingerprint[:]), "distinct credits per book")
	assert.Equal(t, 1, f.jobCountByKey(key), "one shared job")

	// The same book, both roles, the same source: two credits, still one job
	// — the key has been shared since the first book.
	third, err := PersistExtraction(f.tx, f.extraction(&extractionSpec{
		book: bookThree,
		credits: []ExtractionCredit{
			source,
			creditOf(t, models.ContributorRoleTranslator, "Общий", "Источник"),
		}}))
	require.NoError(t, err)
	assert.Equal(t, 2, third.CreditsWritten)
	assert.Equal(t, 0, third.JobsEnqueued, "the shared key was already queued")
	assert.Equal(t, 4, f.creditCountByFingerprint(fingerprint[:]))
	assert.Equal(t, 1, f.jobCountByKey(key))
}

// RED 8: a fingerprint override that existed before a new credit is found for
// future selection; a source change does not inherit it (contract 3.11).
func TestActiveOverrideLookup(t *testing.T) {
	f := withAuthorSchemaTx(t)
	admin := f.user()

	value := nameValue(t,
		nameComponent(authornorm.ComponentFirst, "Старое"),
		nameComponent(authornorm.ComponentLast, "Имя"))
	fingerprint := authornorm.SourceFingerprint(value)

	// The override predates any credit with this source.
	result := f.manualResultFor(fingerprint[:], admin)
	fpOverride := f.fingerprintOverride(fingerprint[:], result, admin)

	bookOne := f.book()
	first, err := PersistExtraction(f.tx, f.extraction(&extractionSpec{
		book: bookOne, credits: []ExtractionCredit{{Role: models.ContributorRoleAuthor, Source: value}}}))
	require.NoError(t, err)
	credit := f.creditIDByFingerprint(first.SnapshotID, fingerprint[:])

	found, err := ActiveOverrideForCredit(f.tx, credit)
	require.NoError(t, err)
	require.NotNil(t, found, "an override that predates the credit is found")
	assert.Equal(t, fpOverride, found.ID)
	assert.Equal(t, fingerprint[:], found.ScopeFingerprint)

	direct, err := ActiveOverrideForFingerprint(f.tx, fingerprint[:])
	require.NoError(t, err)
	require.NotNil(t, direct)
	assert.Equal(t, fpOverride, direct.ID)

	// Any source change is another fingerprint and inherits nothing.
	changed := nameValue(t,
		nameComponent(authornorm.ComponentFirst, "Новое"),
		nameComponent(authornorm.ComponentLast, "Имя"))
	changedFingerprint := authornorm.SourceFingerprint(changed)
	second, err := PersistExtraction(f.tx, f.extraction(&extractionSpec{
		book: f.book(), credits: []ExtractionCredit{{Role: models.ContributorRoleAuthor, Source: changed}}}))
	require.NoError(t, err)
	changedCredit := f.creditIDByFingerprint(second.SnapshotID, changedFingerprint[:])
	none, err := ActiveOverrideForCredit(f.tx, changedCredit)
	require.NoError(t, err)
	assert.Nil(t, none, "a changed source does not inherit the override")

	// Precedence: a credit-scoped override outranks the fingerprint one.
	creditResult := f.manualResultFor(fingerprint[:], admin)
	creditScoped := f.creditOverride(credit, creditResult, fingerprint[:], admin)
	top, err := ActiveOverrideForCredit(f.tx, credit)
	require.NoError(t, err)
	require.NotNil(t, top)
	assert.Equal(t, creditScoped, top.ID)

	// The newest fingerprint override of a scope is the active one.
	newestResult := f.manualResultFor(fingerprint[:], admin)
	newest := f.fingerprintOverride(fingerprint[:], newestResult, admin)
	third, err := PersistExtraction(f.tx, f.extraction(&extractionSpec{
		book:    f.book(),
		credits: []ExtractionCredit{{Role: models.ContributorRoleAuthor, Source: value}},
	}))
	require.NoError(t, err)
	anotherCredit := f.creditIDByFingerprint(third.SnapshotID, fingerprint[:])
	fresh, err := ActiveOverrideForCredit(f.tx, anotherCredit)
	require.NoError(t, err)
	require.NotNil(t, fresh)
	assert.Equal(t, newest, fresh.ID)
}

// RED 9: the repository writes nothing to the reader author tables — not an
// insert, update or delete, on the touched book's own rows or on any other
// row of opds_catalog_author or opds_catalog_bauthor. Counting rows only
// catches inserts, so the test digests both whole tables before and after the
// call: any payload change anywhere fails. The digests are taken in a
// REPEATABLE READ transaction, so the only writer that can change them
// between the two reads is the call under test.
func TestPersistExtractionLeavesReaderAuthorTablesAlone(t *testing.T) {
	requireDatabase(t)

	tx, err := db.Begin()
	require.NoError(t, err, "beginning the reader-preservation transaction")
	t.Cleanup(func() { _ = tx.Rollback() })
	// The first statement of the transaction fixes the snapshot everything
	// below reads, including both digests.
	_, err = tx.Exec(`SET TRANSACTION ISOLATION LEVEL REPEATABLE READ`)
	require.NoError(t, err)

	next := authorSchemaIDBase
	f := &authorSchemaFixture{t: t, tx: tx, next: &next}

	book := f.book()
	// Existing reader rows the extraction could plausibly touch: real
	// authors and links of this very book, not just an empty book.
	authorA := f.returningID(`INSERT INTO opds_catalog_author (full_name) VALUES ('Читатель Один') RETURNING id`)
	authorB := f.returningID(`INSERT INTO opds_catalog_author (full_name) VALUES ('Читатель Два') RETURNING id`)
	f.exec(`INSERT INTO opds_catalog_bauthor (book_id, author_id) VALUES (?, ?), (?, ?)`,
		book, authorA, book, authorB)

	readerDigest := func() (authors, links string) {
		_, digestErr := tx.QueryOne(pg.Scan(&authors), `
			SELECT count(*) || ':' || coalesce(sum(hashtextextended(id::text || ':' || full_name, 0)), 0)
			FROM opds_catalog_author`)
		require.NoError(t, digestErr)
		_, digestErr = tx.QueryOne(pg.Scan(&links), `
			SELECT count(*) || ':' || coalesce(sum(hashtextextended(id::text || ':' || book_id::text || ':' || author_id::text, 0)), 0)
			FROM opds_catalog_bauthor`)
		require.NoError(t, digestErr)
		return authors, links
	}

	beforeAuthors, beforeLinks := readerDigest()

	_, err = PersistExtraction(f.tx, f.extraction(&extractionSpec{
		book: book,
		credits: []ExtractionCredit{
			creditOf(t, models.ContributorRoleAuthor, "Читатель", "Не Трогать"),
			creditOf(t, models.ContributorRoleAuthor, "Второй", "Автор"),
			creditOf(t, models.ContributorRoleTranslator, "Пер", "Водчик"),
		}}))
	require.NoError(t, err)

	afterAuthors, afterLinks := readerDigest()
	assert.Equal(t, beforeAuthors, afterAuthors,
		"opds_catalog_author changed: no reader author row may be inserted, updated or deleted")
	assert.Equal(t, beforeLinks, afterLinks,
		"opds_catalog_bauthor changed: no reader link may be inserted, updated or deleted")

	assert.Equal(t, 2, f.count(`SELECT count(*) FROM opds_catalog_bauthor WHERE book_id = ?`, book),
		"the book keeps exactly the reader links it had before the call")
	assert.Equal(t, 3, f.count(`SELECT count(*) FROM book_contributor_credit c
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id WHERE s.book_id = ?`, book),
		"the credits live only in the source layer")
}

// RED 10: nil/empty/negative IDs and invalid versions give a typed validation
// error before any SQL — the connection is nil, so a statement would panic.
func TestPersistExtractionValidatesBeforeSQL(t *testing.T) {
	source, err := authornorm.NewSourceValue([]authornorm.SourceComponent{
		nameComponent(authornorm.ComponentFirst, "Валидный"),
		nameComponent(authornorm.ComponentLast, "Вход"),
	})
	require.NoError(t, err)

	valid := func() ExtractionInput {
		return ExtractionInput{
			BookID:            7,
			BookMD5:           bookMD5(7),
			ExtractorVersion:  "extractor-v1",
			NormalizerVersion: "normalizer-v1",
			Origin:            models.BookMetadataSnapshotLive,
			Outcome:           models.BookMetadataSnapshotExtracted,
			ArchivePath:       "fixture.zip",
			EntryName:         "7.fb2",
			Credits:           []ExtractionCredit{{Role: models.ContributorRoleAuthor, Source: source}},
		}
	}

	for _, broken := range []struct {
		name   string
		breaks func(*ExtractionInput)
		want   error
	}{
		{"zero book ID", func(in *ExtractionInput) { in.BookID = 0 }, ErrInvalidExtractionBookID},
		{"negative book ID", func(in *ExtractionInput) { in.BookID = -3 }, ErrInvalidExtractionBookID},
		{"backfill without run", func(in *ExtractionInput) {
			in.Origin = models.BookMetadataSnapshotBackfill
		}, ErrInvalidExtractionRunID},
		{"backfill with zero run", func(in *ExtractionInput) {
			in.Origin = models.BookMetadataSnapshotBackfill
			in.RunID = ptr(int64(0))
		}, ErrInvalidExtractionRunID},
		{"backfill with negative run", func(in *ExtractionInput) {
			in.Origin = models.BookMetadataSnapshotBackfill
			in.RunID = ptr(int64(-2))
		}, ErrInvalidExtractionRunID},
		{"live with a run", func(in *ExtractionInput) { in.RunID = ptr(int64(9)) }, ErrInvalidExtractionRunID},
		{"unknown origin", func(in *ExtractionInput) { in.Origin = "sideload" }, ErrInvalidExtractionOrigin},
		{"empty extractor version", func(in *ExtractionInput) { in.ExtractorVersion = "" }, authornorm.ErrEmptyVersion},
		{"blank extractor version", func(in *ExtractionInput) { in.ExtractorVersion = " \t " }, authornorm.ErrEmptyVersion},
		{"empty normalizer version", func(in *ExtractionInput) { in.NormalizerVersion = "" }, authornorm.ErrEmptyVersion},
		{"extracted without author credits", func(in *ExtractionInput) {
			in.Credits = []ExtractionCredit{{Role: models.ContributorRoleTranslator, Source: source}}
		}, ErrInvalidExtractionOutcome},
		{"extracted_no_author with an author credit", func(in *ExtractionInput) {
			in.Outcome = models.BookMetadataSnapshotExtractedNoAuthor
		}, ErrInvalidExtractionOutcome},
		{"unknown outcome", func(in *ExtractionInput) { in.Outcome = "lost" }, ErrInvalidExtractionOutcome},
		{"unknown credit role", func(in *ExtractionInput) {
			in.Credits = []ExtractionCredit{{Role: "editor", Source: source}}
		}, ErrInvalidCreditRole},
		{"credit without name components", func(in *ExtractionInput) {
			in.Credits = []ExtractionCredit{{Role: models.ContributorRoleAuthor, Source: authornorm.SourceValue{}}}
		}, authornorm.ErrNoNameComponents},
	} {
		t.Run(broken.name, func(t *testing.T) {
			input := valid()
			broken.breaks(&input)
			result, err := PersistExtraction(nil, &input)
			assert.ErrorIs(t, err, broken.want)
			assert.Equal(t, PersistExtractionResult{}, result)
		})
	}
}
