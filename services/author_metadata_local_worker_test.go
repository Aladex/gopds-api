package services

// Phase 10: the local normalization worker against a real PostgreSQL. The
// worker claims globally — any ready local job — and its claims commit, so
// the suite runs in a scratch database migrated from the real files and
// dropped afterwards, emptied before every case.

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/internal/migrate"
	"gopds-api/internal/testdb"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const workerExtractor = "extractor-v1"

// localWorkerDB creates and migrates a scratch database for the suite.
func localWorkerDB(t *testing.T) *pg.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	cfg, ok := testdb.Configured()
	if !ok {
		t.Skip(testdb.SkipReason)
	}
	admin, err := testdb.Connect(cfg, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close() })

	name := fmt.Sprintf("author_local_worker_test_%d", time.Now().UnixNano())
	_, err = admin.Exec("CREATE DATABASE " + name)
	require.NoError(t, err, "creating the scratch database")
	t.Cleanup(func() {
		if _, dropErr := admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)"); dropErr != nil {
			t.Errorf("dropping the scratch database %s: %v", name, dropErr)
		}
	})

	scratch := pg.Connect(&pg.Options{
		Addr: cfg.Host, User: cfg.User, Password: cfg.Password, Database: name, PoolSize: 16,
	})
	t.Cleanup(func() { _ = scratch.Close() })
	_, err = migrate.Run(context.Background(), scratch, os.DirFS(".."), "database_migrations", migrate.AppBaseline())
	require.NoError(t, err, "migrating the scratch database")
	return scratch
}

// resetPipeline empties every table the worker reads or writes. TRUNCATE is
// not a row DELETE, so the immutability guards do not apply.
func resetPipeline(t *testing.T, s *pg.DB) {
	t.Helper()
	_, err := s.Exec(`TRUNCATE book_contributor_credit_selection_audit, book_contributor_credit_selection,
		contributor_review_item, contributor_manual_override, contributor_normalization_job_attempt,
		contributor_normalization_job, contributor_normalization_result, book_contributor_credit,
		book_metadata_snapshot, author_acceptance_class, author_metadata_run_item_attempt,
		author_metadata_run_item, author_metadata_run CASCADE`)
	require.NoError(t, err)
}

// workerFixture seeds and inspects one scratch database.
type workerFixture struct {
	t  *testing.T
	db *pg.DB
}

func (f *workerFixture) count(query string, params ...interface{}) int {
	f.t.Helper()
	var n int
	_, err := f.db.QueryOne(pg.Scan(&n), query, params...)
	require.NoError(f.t, err, query)
	return n
}

func (f *workerFixture) exec(query string, params ...interface{}) {
	f.t.Helper()
	_, err := f.db.Exec(query, params...)
	require.NoError(f.t, err, query)
}

func (f *workerFixture) book() int64 {
	f.t.Helper()
	var id int64
	_, err := f.db.QueryOne(pg.Scan(&id), `INSERT INTO opds_catalog_book
			(filename, path, format, registerdate, docdate, lang, title, annotation, md5)
		VALUES ('seed.fb2', 'seed.zip', 'fb2', now(), '', 'ru', 'seed', '', '')
		RETURNING id`)
	require.NoError(f.t, err)
	return id
}

// source builds a canonical value from kind/value pairs.
func source(t *testing.T, pairs ...string) authornorm.SourceValue {
	t.Helper()
	kinds := map[string]authornorm.ComponentKind{
		"first": authornorm.ComponentFirst, "middle": authornorm.ComponentMiddle,
		"last": authornorm.ComponentLast, "nickname": authornorm.ComponentNickname,
	}
	components := make([]authornorm.SourceComponent, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		components = append(components, authornorm.SourceComponent{Kind: kinds[pairs[i]], Value: pairs[i+1]})
	}
	v, err := authornorm.NewSourceValue(components)
	require.NoError(t, err)
	return v
}

// Golden sources of the phase-3 normalizer.
func tolstoy(t *testing.T) authornorm.SourceValue {
	return source(t, "first", "Лев", "middle", "Николаевич", "last", "Толстой")
}

func chekhov(t *testing.T) authornorm.SourceValue {
	return source(t, "first", "Антон", "middle", "Павлович", "last", "Чехов")
}

func initialed(t *testing.T) authornorm.SourceValue {
	return source(t, "first", "И.", "last", "Петров")
}

func garbage(t *testing.T) authornorm.SourceValue { return source(t, "last", "***") }

func unknownAuthor(t *testing.T) authornorm.SourceValue {
	return source(t, "last", "Автор неизвестен")
}

func author(v authornorm.SourceValue) database.ExtractionCredit {
	c := database.ExtractionCredit{Role: models.ContributorRoleAuthor, Source: v}
	if v.HasDuplicateComponent() {
		c.QualityFlags = []string{string(authornorm.FlagDuplicateComponent)}
	}
	return c
}

func translator(v authornorm.SourceValue) database.ExtractionCredit {
	return database.ExtractionCredit{Role: models.ContributorRoleTranslator, Source: v}
}

// persist writes one current extraction of a new book through the phase-6
// repository and returns the book and its credit IDs in input order.
func (f *workerFixture) persist(normalizerVersion string, credits ...database.ExtractionCredit) (book int64, ids []int64) {
	f.t.Helper()
	book = f.book()
	outcome := models.BookMetadataSnapshotExtracted
	hasAuthor := false
	for i := range credits {
		hasAuthor = hasAuthor || credits[i].Role == models.ContributorRoleAuthor
	}
	if !hasAuthor {
		outcome = models.BookMetadataSnapshotExtractedNoAuthor
	}
	var snapshot int64
	err := f.db.RunInTransaction(context.Background(), func(tx *pg.Tx) error {
		out, persistErr := database.PersistExtraction(tx, &database.ExtractionInput{
			BookID: book, BookMD5: fmt.Sprintf("%032x", book), ExtractorVersion: workerExtractor,
			NormalizerVersion: normalizerVersion, Origin: models.BookMetadataSnapshotLive,
			Outcome: outcome, ArchivePath: "seed.zip", EntryName: fmt.Sprintf("%d.fb2", book),
			Credits: credits,
		})
		snapshot = out.SnapshotID
		return persistErr
	})
	require.NoError(f.t, err)
	_, err = f.db.Query(&ids, `SELECT id FROM book_contributor_credit WHERE snapshot_id = ? ORDER BY id`, snapshot)
	require.NoError(f.t, err)
	return book, ids
}

func (f *workerFixture) persistBook(credits ...database.ExtractionCredit) []int64 {
	f.t.Helper()
	_, ids := f.persist(authornorm.NormalizerVersion, credits...)
	return ids
}

func (f *workerFixture) selection(credit int64) *models.BookContributorCreditSelection {
	f.t.Helper()
	s := new(models.BookContributorCreditSelection)
	err := f.db.Model(s).Where("credit_id = ?", credit).Select()
	if errors.Is(err, pg.ErrNoRows) {
		return nil
	}
	require.NoError(f.t, err)
	return s
}

// registerStructuredPerson registers (structured_person, Cyrl) in policy
// version 2, the first a registration takes, for the shipped normalizer.
func (f *workerFixture) registerStructuredPerson() {
	f.t.Helper()
	f.registerPair("2", authornorm.ClassStructuredPerson, authornorm.ScriptCyrillic)
}

// registerPair registers one pair for the shipped normalizer, as a shipped
// registration citing a fixture evidence file.
func (f *workerFixture) registerPair(version string, class authornorm.DecisionClass, script authornorm.Script) {
	f.t.Helper()
	f.exec(`INSERT INTO author_acceptance_class
			(policy_version, decision_class, script, config_version, evidence_report_sha256, evidence_ref, source)
		VALUES (?, ?, ?, ?, decode(repeat('ee', 32), 'hex'), 'testdata/fixture-evidence.json', 'shipped')`,
		version, string(class), string(script), authornorm.NormalizerVersion)
}

// manualOverride seeds a manual result and an override on it, scoped to the
// credit when credit is positive and to the fingerprint otherwise.
func (f *workerFixture) manualOverride(fp [32]byte, credit int64) (resultID, overrideID int64) {
	f.t.Helper()
	_, err := f.db.QueryOne(pg.Scan(&resultID), `INSERT INTO contributor_normalization_result
		(source_fingerprint, result_schema_version, method, kind, status, display_name, search_key,
		 created_by_user_id)
		VALUES (?, 'manual-v1', 'manual', 'person', 'normalized', 'Manual', 'manual', 1)
		RETURNING id`, fp[:])
	require.NoError(f.t, err)
	var scopeCredit *int64
	var scopeFingerprint []byte
	if credit > 0 {
		scopeCredit = &credit
	} else {
		scopeFingerprint = fp[:]
	}
	_, err = f.db.QueryOne(pg.Scan(&overrideID), `INSERT INTO contributor_manual_override
		(scope_credit_id, scope_fingerprint, source_fingerprint, result_id, created_by_user_id)
		VALUES (?, ?, ?, ?, 1)
		RETURNING id`, scopeCredit, scopeFingerprint, fp[:], resultID)
	require.NoError(f.t, err)
	return resultID, overrideID
}

func (f *workerFixture) seedJob(fp [32]byte, normalizerVersion string) int64 {
	f.t.Helper()
	key, err := authornorm.NormalizationKey(fp, workerExtractor, normalizerVersion)
	require.NoError(f.t, err)
	var id int64
	_, err = f.db.QueryOne(pg.Scan(&id), `INSERT INTO contributor_normalization_job
		(normalization_key, source_fingerprint, extractor_version, normalizer_version)
		VALUES (?, ?, ?, ?) RETURNING id`, key[:], fp[:], workerExtractor, normalizerVersion)
	require.NoError(f.t, err)
	return id
}

func (f *workerFixture) attemptClasses(job int64) []string {
	f.t.Helper()
	var classes []string
	_, err := f.db.Query(&classes, `SELECT coalesce(error_class, '') FROM contributor_normalization_job_attempt
		WHERE job_id = ? ORDER BY attempt_no`, job)
	require.NoError(f.t, err)
	return classes
}

func (f *workerFixture) resultsFor(fp [32]byte) int {
	return f.count(`SELECT count(*) FROM contributor_normalization_result WHERE source_fingerprint = ?
		AND method IN ('structured', 'rules')`, fp[:])
}

func (f *workerFixture) openReviewItems(fp [32]byte) int {
	return f.count(`SELECT count(*) FROM contributor_review_item WHERE scope_fingerprint = ? AND status = 'open'`, fp[:])
}

func (f *workerFixture) audits(credit int64) int {
	return f.count(`SELECT count(*) FROM book_contributor_credit_selection_audit WHERE credit_id = ?`, credit)
}

// countingNormalizer is the real normalizer behind a call counter.
type countingNormalizer struct{ calls atomic.Int64 }

func (c *countingNormalizer) normalize(v authornorm.SourceValue, extractorVersion string) (authornorm.Result, error) {
	c.calls.Add(1)
	return authornorm.Normalize(v, extractorVersion)
}

// versioned is the real normalizer published under another version: the
// result is the same, its version and key are the new configuration's.
func versioned(version string) LocalNormalizer {
	return func(v authornorm.SourceValue, extractorVersion string) (authornorm.Result, error) {
		r, err := authornorm.Normalize(v, extractorVersion)
		if err != nil {
			return r, err
		}
		r.NormalizerVersion = version
		r.NormalizationKey, err = authornorm.NormalizationKey(r.SourceFingerprint, extractorVersion, version)
		return r, err
	}
}

func testWorkerConfig() AuthorMetadataLocalWorkerConfig {
	return AuthorMetadataLocalWorkerConfig{
		ClaimLimit: 10,
		Lease:      time.Minute,
		Retry: AuthorMetadataRetryPolicy{
			MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond,
			Jitter: func(time.Duration) time.Duration { return 0 },
		},
		NormalizerVersion: authornorm.NormalizerVersion,
		Normalize:         authornorm.Normalize,
	}
}

func (f *workerFixture) worker(mod func(*AuthorMetadataLocalWorkerConfig)) *AuthorMetadataLocalWorker {
	f.t.Helper()
	cfg := testWorkerConfig()
	if mod != nil {
		mod(&cfg)
	}
	w, err := NewAuthorMetadataLocalWorker(f.db, &cfg)
	require.NoError(f.t, err)
	return w
}

// drain runs the worker until no local job is pending, waiting out retry
// delays; it fails the test rather than spin forever.
func (f *workerFixture) drain(w *AuthorMetadataLocalWorker) {
	f.t.Helper()
	for i := 0; i < 200; i++ {
		report, err := w.RunOnce(context.Background())
		require.NoError(f.t, err)
		if report.Claimed > 0 {
			continue
		}
		if f.count(`SELECT count(*) FROM contributor_normalization_job WHERE status = 'pending'`) == 0 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	f.t.Fatal("local jobs still pending after the drain limit")
}

func (f *workerFixture) accounting() database.CreditAccounting {
	f.t.Helper()
	a, err := database.AuthorCreditAccounting(context.Background(), f.db)
	require.NoError(f.t, err)
	return a
}

func TestAuthorMetadataLocalWorker(t *testing.T) {
	s := localWorkerDB(t)
	cases := []struct {
		name string
		fn   func(t *testing.T, f *workerFixture)
	}{
		{"shared key is normalized once", workerSharedKeyNormalizedOnce},
		{"registered class selects every applicable credit", workerRegisteredClassSelects},
		{"empty policy selects nothing", workerEmptyPolicySelectsNothing},
		{"translator job is refused", workerTranslatorJobRefused},
		{"duplicate component fails closed", workerDuplicateComponentFailsClosed},
		{"stored source must match the job", workerStoredSourceMustMatchJob},
		{"misconfigured normalizer version is refused", workerMisconfiguredNormalizerRefused},
		{"impossible duplicate source is refused", workerImpossibleDuplicateSourceRefused},
		{"override has precedence", workerOverrideHasPrecedence},
		{"normalizer failure is closed and bounded", workerNormalizerFailureIsBounded},
		{"abandoned last attempt is accounted", workerAbandonedLastAttemptAccounted},
		{"normalizer version change", workerNormalizerVersionChange},
		{"concurrent replicas converge", workerConcurrentReplicasConverge},
		{"late owner cannot overwrite", workerLateOwnerCannotOverwrite},
		{"repeated resolution writes nothing", workerRepeatedResolutionWritesNothing},
		{"late credit of a completed key, empty policy", workerLateCreditEmptyPolicy},
		{"late credit of a completed key, registered policy", workerLateCreditRegisteredPolicy},
		{"late duplicate credit of a completed key fails closed", workerLateDuplicateCreditFailsClosed},
		{"duplicate credit resolved under the plain reading is reconciled", workerDuplicateResolvedAsPlainReconciled},
		{"completion accounting", workerCompletionAccounting},
		{"out-of-order completion", workerOutOfOrderCompletion},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// A nil dereference in one case must fail that case, not end
			// the binary and hide every case after it.
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("panic: %v", r)
				}
			}()
			resetPipeline(t, s)
			c.fn(t, &workerFixture{t: t, db: s})
		})
	}
}

func TestAuthorMetadataLocalWorkerConfigIsValidated(t *testing.T) {
	cases := map[string]func(*AuthorMetadataLocalWorkerConfig){
		"zero claim limit":      func(c *AuthorMetadataLocalWorkerConfig) { c.ClaimLimit = 0 },
		"negative lease":        func(c *AuthorMetadataLocalWorkerConfig) { c.Lease = -time.Second },
		"invalid retry":         func(c *AuthorMetadataLocalWorkerConfig) { c.Retry.MaxAttempts = 0 },
		"no normalizer version": func(c *AuthorMetadataLocalWorkerConfig) { c.NormalizerVersion = " " },
		"no normalizer":         func(c *AuthorMetadataLocalWorkerConfig) { c.Normalize = nil },
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := testWorkerConfig()
			mod(&cfg)
			_, err := NewAuthorMetadataLocalWorker(nil, &cfg)
			assert.ErrorIs(t, err, ErrInvalidAuthorMetadataLocalWorkerConfig)
		})
	}
	defaults := DefaultAuthorMetadataLocalWorkerConfig()
	_, err := NewAuthorMetadataLocalWorker(nil, &defaults)
	assert.NoError(t, err)
}

// RED 1: several credits of one key call the normalizer once and share the
// one immutable result.
func workerSharedKeyNormalizedOnce(t *testing.T, f *workerFixture) {
	spy := &countingNormalizer{}
	var credits []int64
	for i := 0; i < 3; i++ {
		credits = append(credits, f.persistBook(author(tolstoy(t)))...)
	}
	credits = append(credits, f.persistBook(author(tolstoy(t)), author(tolstoy(t)))...)

	f.drain(f.worker(func(c *AuthorMetadataLocalWorkerConfig) { c.Normalize = spy.normalize }))

	assert.Equal(t, int64(1), spy.calls.Load(), "one normalization per key")
	fp := authornorm.SourceFingerprint(tolstoy(t))
	assert.Equal(t, 1, f.resultsFor(fp))
	var result int64
	for _, credit := range credits {
		s := f.selection(credit)
		require.NotNil(t, s, "credit %d", credit)
		require.NotNil(t, s.ResultID, "credit %d", credit)
		if result == 0 {
			result = *s.ResultID
		}
		assert.Equal(t, result, *s.ResultID, "credits of one key share the result")
	}
}

// RED 2: a registered class is selected for every applicable credit, with the
// policy version on the selection and in the audit.
func workerRegisteredClassSelects(t *testing.T, f *workerFixture) {
	f.registerStructuredPerson()
	first := f.persistBook(author(tolstoy(t)), author(initialed(t)))
	second := f.persistBook(author(tolstoy(t)), translator(tolstoy(t)))

	f.drain(f.worker(nil))

	for _, credit := range []int64{first[0], second[0]} {
		s := f.selection(credit)
		require.NotNil(t, s)
		assert.Equal(t, models.CreditSelectionSelected, s.State)
		assert.Equal(t, models.CreditSelectionAutomatic, *s.Basis)
		assert.Equal(t, "2", *s.PolicyVersion)
		assert.Equal(t, 1, f.count(`SELECT count(*) FROM book_contributor_credit_selection_audit
			WHERE credit_id = ? AND state = 'selected' AND basis = 'automatic' AND policy_version = '2'`, credit))
	}
	assert.Equal(t, models.CreditSelectionReview, f.selection(first[1]).State, "an ambiguous class is never selected")
	assert.Nil(t, f.selection(second[1]), "a translator credit is never resolved")
}

// RED 3 + mutation (accept a valid candidate without the policy): under the
// empty production policy nothing is selected, and every credit goes where
// scope A3 sends it.
func workerEmptyPolicySelectsNothing(t *testing.T, f *workerFixture) {
	credits := f.persistBook(author(tolstoy(t)), author(initialed(t)), author(garbage(t)),
		author(unknownAuthor(t)), translator(chekhov(t)))

	f.drain(f.worker(nil))

	assert.Equal(t, 0, f.count(`SELECT count(*) FROM book_contributor_credit_selection WHERE state = 'selected'`))

	structured := f.selection(credits[0])
	require.NotNil(t, structured)
	assert.Equal(t, models.CreditSelectionUnresolved, structured.State)
	assert.Equal(t, models.UnresolvedPolicyNotRegistered, *structured.UnresolvedReason)
	assert.NotNil(t, structured.ResultID, "the result stays as a proposal")
	assert.Equal(t, 0, f.openReviewItems(authornorm.SourceFingerprint(tolstoy(t))))

	review := f.selection(credits[1])
	require.NotNil(t, review)
	assert.Equal(t, models.CreditSelectionReview, review.State, "an ambiguous class goes to review, not unresolved")
	assert.Equal(t, 1, f.openReviewItems(authornorm.SourceFingerprint(initialed(t))))

	assert.Equal(t, models.CreditSelectionInvalid, f.selection(credits[2]).State)
	placeholder := f.selection(credits[3])
	assert.Equal(t, models.CreditSelectionUnresolved, placeholder.State)
	assert.Equal(t, models.UnresolvedPolicyNotRegistered, *placeholder.UnresolvedReason)
	assert.Nil(t, f.selection(credits[4]))

	a := f.accounting()
	assert.True(t, a.Settled())
	assert.Equal(t, 0, a.Selected)
}

// RED 4 + mutation (translator job processed): a job no author credit stands
// behind — here seeded by hand for a translator — is refused with a closed
// class and never produces a result or a resolution.
func workerTranslatorJobRefused(t *testing.T, f *workerFixture) {
	spy := &countingNormalizer{}
	credits := f.persistBook(author(tolstoy(t)), translator(chekhov(t)))
	require.Equal(t, 1, f.count(`SELECT count(*) FROM contributor_normalization_job`), "the repository queues authors only")
	job := f.seedJob(authornorm.SourceFingerprint(chekhov(t)), authornorm.NormalizerVersion)

	f.drain(f.worker(func(c *AuthorMetadataLocalWorkerConfig) {
		c.Normalize = spy.normalize
		c.Retry.MaxAttempts = 2
	}))

	assert.Equal(t, []string{"no_author_credit", "max_attempts_exceeded"}, f.attemptClasses(job))
	assert.Equal(t, 0, f.resultsFor(authornorm.SourceFingerprint(chekhov(t))))
	assert.Equal(t, int64(1), spy.calls.Load(), "only the author's job reached the normalizer")
	assert.Nil(t, f.selection(credits[1]))
	assert.Equal(t, 0, f.count(`SELECT count(*) FROM contributor_normalization_job
		WHERE id = ? AND status = 'completed'`, job))
}

// The v1 fingerprint does not cover the duplicate flag, so a repeated empty
// child shares a key with the plain credit. The shared result fails closed to
// the duplicate reading, whichever credit came first.
func workerDuplicateComponentFailsClosed(t *testing.T, f *workerFixture) {
	plain := source(t, "first", "Иван", "last", "Петров")
	repeated := source(t, "first", "Иван", "first", "", "last", "Петров")
	require.True(t, repeated.HasDuplicateComponent())
	require.Equal(t, authornorm.SourceFingerprint(plain), authornorm.SourceFingerprint(repeated))
	first := f.persistBook(author(plain))
	second := f.persistBook(author(repeated))

	f.drain(f.worker(nil))

	assert.Equal(t, 1, f.count(`SELECT count(*) FROM contributor_normalization_result
		WHERE source_fingerprint = ? AND decision_class = 'duplicate_component'`, authornorm.SourceFingerprint(plain)))
	for _, credit := range []int64{first[0], second[0]} {
		assert.Equal(t, models.CreditSelectionReview, f.selection(credit).State)
	}
}

// A credit row whose stored fields do not reproduce its fingerprint is not a
// source the worker normalizes.
func workerStoredSourceMustMatchJob(t *testing.T, f *workerFixture) {
	book := f.book()
	var snapshot int64
	_, err := f.db.QueryOne(pg.Scan(&snapshot), `INSERT INTO book_metadata_snapshot
		(book_id, book_md5, extractor_version, origin, outcome, archive_path, entry_name, is_current)
		VALUES (?, ?, ?, 'live', 'extracted', 'seed.zip', 'seed.fb2', true) RETURNING id`,
		book, fmt.Sprintf("%032x", book), workerExtractor)
	require.NoError(t, err)
	wrong := authornorm.SourceFingerprint(chekhov(t))
	f.exec(`INSERT INTO book_contributor_credit
		(snapshot_id, role, position, source_first_name, source_middle_name, source_last_name,
		 source_display_name, source_fingerprint)
		VALUES (?, 'author', 0, 'Лев', 'Николаевич', 'Толстой', 'Лев Николаевич Толстой', ?)`, snapshot, wrong[:])
	job := f.seedJob(wrong, authornorm.NormalizerVersion)

	f.drain(f.worker(func(c *AuthorMetadataLocalWorkerConfig) { c.Retry.MaxAttempts = 2 }))

	assert.Equal(t, []string{"source_not_canonical", "max_attempts_exceeded"}, f.attemptClasses(job))
	assert.Equal(t, 0, f.resultsFor(wrong))
}

// fingerprintV1 computes the v1 source fingerprint of raw stored columns,
// following the byte contract of authornorm.SourceFingerprint, for rows no
// SourceValue can represent.
func fingerprintV1(fields [4]*string, display string) [32]byte {
	stream := []byte("author-source-v1")
	for _, field := range append(fields[:], &display) {
		if field == nil {
			stream = append(stream, make([]byte, 9)...)
			continue
		}
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(*field)))
		stream = append(append(append(stream, 1), length[:]...), *field...)
	}
	return sha256.Sum256(stream)
}

// Fix round 1 (review B3): a stored duplicate credit no extraction could
// produce — two children claiming one display occurrence — is refused even
// though its fingerprint matches the job.
func workerImpossibleDuplicateSourceRefused(t *testing.T, f *workerFixture) {
	require.Equal(t, authornorm.SourceFingerprint(tolstoy(t)),
		fingerprintV1([4]*string{ptrTo("Лев"), ptrTo("Николаевич"), ptrTo("Толстой"), nil}, "Лев Николаевич Толстой"),
		"the test fingerprint follows the v1 contract")
	name := "Иван"
	fp := fingerprintV1([4]*string{&name, nil, &name, nil}, name)
	book := f.book()
	var snapshot int64
	_, err := f.db.QueryOne(pg.Scan(&snapshot), `INSERT INTO book_metadata_snapshot
		(book_id, book_md5, extractor_version, origin, outcome, archive_path, entry_name, is_current)
		VALUES (?, ?, ?, 'live', 'extracted', 'seed.zip', 'seed.fb2', true) RETURNING id`,
		book, fmt.Sprintf("%032x", book), workerExtractor)
	require.NoError(t, err)
	f.exec(`INSERT INTO book_contributor_credit
		(snapshot_id, role, position, source_first_name, source_last_name, source_display_name,
		 source_fingerprint, quality_flags)
		VALUES (?, 'author', 0, ?, ?, ?, ?, '{duplicate_component}')`, snapshot, name, name, name, fp[:])
	job := f.seedJob(fp, authornorm.NormalizerVersion)

	f.drain(f.worker(func(c *AuthorMetadataLocalWorkerConfig) { c.Retry.MaxAttempts = 2 }))

	assert.Equal(t, []string{"source_not_canonical", "max_attempts_exceeded"}, f.attemptClasses(job))
	assert.Equal(t, 0, f.resultsFor(fp))
}

func ptrTo(s string) *string { return &s }

// A normalizer that publishes another version than the worker was configured
// with produces results of other keys; none of them is recorded.
func workerMisconfiguredNormalizerRefused(t *testing.T, f *workerFixture) {
	f.persistBook(author(tolstoy(t)))
	f.drain(f.worker(func(c *AuthorMetadataLocalWorkerConfig) {
		c.Normalize = versioned("authornorm-local-v2-test")
		c.Retry.MaxAttempts = 2
	}))

	var job int64
	_, err := f.db.QueryOne(pg.Scan(&job), `SELECT id FROM contributor_normalization_job`)
	require.NoError(t, err)
	assert.Equal(t, []string{"normalizer_version_mismatch", "max_attempts_exceeded"}, f.attemptClasses(job))
	assert.Equal(t, 0, f.resultsFor(authornorm.SourceFingerprint(tolstoy(t))))
}

// RED 5: an existing credit or fingerprint override wins over the automatic
// result, which is still stored and does not replace it.
func workerOverrideHasPrecedence(t *testing.T, f *workerFixture) {
	f.registerStructuredPerson()
	a1 := f.persistBook(author(tolstoy(t)))
	a2 := f.persistBook(author(tolstoy(t)))
	b1 := f.persistBook(author(chekhov(t)))
	b2 := f.persistBook(author(chekhov(t)))
	tolstoyFP, chekhovFP := authornorm.SourceFingerprint(tolstoy(t)), authornorm.SourceFingerprint(chekhov(t))
	byFingerprint, fingerprintOverride := f.manualOverride(tolstoyFP, 0)
	byCredit, creditOverride := f.manualOverride(chekhovFP, b1[0])

	f.drain(f.worker(nil))

	for _, credit := range []int64{a1[0], a2[0]} {
		s := f.selection(credit)
		assert.Equal(t, models.CreditSelectionFingerprintOverride, *s.Basis)
		assert.Equal(t, fingerprintOverride, *s.OverrideID)
		assert.Equal(t, byFingerprint, *s.ResultID)
	}
	sb1 := f.selection(b1[0])
	assert.Equal(t, models.CreditSelectionCreditOverride, *sb1.Basis)
	assert.Equal(t, creditOverride, *sb1.OverrideID)
	assert.Equal(t, byCredit, *sb1.ResultID)
	assert.Equal(t, models.CreditSelectionAutomatic, *f.selection(b2[0]).Basis, "the credit override covers one credit")

	assert.Equal(t, 1, f.resultsFor(tolstoyFP), "the automatic result is stored next to the override")
	assert.Equal(t, 1, f.count(`SELECT count(*) FROM contributor_normalization_result
		WHERE id = ? AND method = 'manual'`, byFingerprint), "the manual result is untouched")
}

// RED 6: a normalizer failure gets a closed class on every attempt, the
// attempts are bounded, and the credit ends unresolved with normalizer_failed
// — no review item and no endless re-enqueue.
func workerNormalizerFailureIsBounded(t *testing.T, f *workerFixture) {
	credits := f.persistBook(author(tolstoy(t)))
	failing := func(authornorm.SourceValue, string) (authornorm.Result, error) {
		return authornorm.Result{}, errors.New("normalizer broke")
	}
	w := f.worker(func(c *AuthorMetadataLocalWorkerConfig) { c.Normalize = failing })
	f.drain(w)

	var job int64
	_, err := f.db.QueryOne(pg.Scan(&job), `SELECT id FROM contributor_normalization_job`)
	require.NoError(t, err)
	assert.Equal(t, []string{"normalizer_failed", "normalizer_failed", "max_attempts_exceeded"}, f.attemptClasses(job))
	assert.Equal(t, 1, f.count(`SELECT count(*) FROM contributor_normalization_job WHERE status = 'failed'`))

	s := f.selection(credits[0])
	require.NotNil(t, s)
	assert.Equal(t, models.CreditSelectionUnresolved, s.State)
	assert.Equal(t, models.UnresolvedNormalizerFailed, *s.UnresolvedReason)
	assert.Nil(t, s.ResultID)
	assert.Equal(t, 0, f.count(`SELECT count(*) FROM contributor_review_item`))

	report, err := w.RunOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, report.Claimed, "nothing is re-enqueued")
	a := f.accounting()
	assert.True(t, a.Settled())
	assert.Equal(t, map[models.UnresolvedReason]int{models.UnresolvedNormalizerFailed: 1}, a.Unresolved)
}

// A worker that dies holding the last attempt leaves the job to the next
// claim, which ends it failed; the credits are still accounted.
func workerAbandonedLastAttemptAccounted(t *testing.T, f *workerFixture) {
	credits := f.persistBook(author(tolstoy(t)))
	claims, err := database.ClaimLocalNormalizationJobs(context.Background(), f.db, database.NewLeaseOwner(),
		database.LeaseClaimOptions{Limit: 1, Lease: time.Millisecond, MaxAttempts: 1})
	require.NoError(t, err)
	require.Len(t, claims, 1)
	time.Sleep(5 * time.Millisecond)

	report, err := f.worker(func(c *AuthorMetadataLocalWorkerConfig) { c.Retry.MaxAttempts = 1 }).
		RunOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, report.Claimed)
	assert.Equal(t, []string{"max_attempts_exceeded"}, f.attemptClasses(claims[0].ID))

	s := f.selection(credits[0])
	require.NotNil(t, s, "the abandoned job's credit must not stay hidden")
	assert.Equal(t, models.UnresolvedNormalizerFailed, *s.UnresolvedReason)
	assert.True(t, f.accounting().Settled())
}

// RED 7: a new normalizer version creates a new automatic result under a new
// key; a manual source override survives it, a credit without one moves to
// the new result, and a job of another version is refused, not applied.
func workerNormalizerVersionChange(t *testing.T, f *workerFixture) {
	const v2 = "authornorm-local-v2-test"
	credits := f.persistBook(author(tolstoy(t)), author(chekhov(t)))
	f.drain(f.worker(nil))
	before := f.selection(credits[1])
	require.Equal(t, models.CreditSelectionUnresolved, before.State)

	tolstoyFP, chekhovFP := authornorm.SourceFingerprint(tolstoy(t)), authornorm.SourceFingerprint(chekhov(t))
	manual, override := f.manualOverride(tolstoyFP, 0)
	require.NoError(t, f.db.RunInTransaction(context.Background(), func(tx *pg.Tx) error {
		return database.ResolveCreditSelection(context.Background(), tx, credits[0], nil)
	}))

	f.seedJob(tolstoyFP, v2)
	f.seedJob(chekhovFP, v2)
	_, stale := f.persist(authornorm.NormalizerVersion, author(initialed(t)))
	v2Worker := f.worker(func(c *AuthorMetadataLocalWorkerConfig) {
		c.NormalizerVersion = v2
		c.Normalize = versioned(v2)
		c.Retry.MaxAttempts = 2
	})
	f.drain(v2Worker)

	assert.Equal(t, 2, f.resultsFor(tolstoyFP), "one automatic result per normalizer version")
	assert.Equal(t, 2, f.resultsFor(chekhovFP))

	kept := f.selection(credits[0])
	assert.Equal(t, models.CreditSelectionFingerprintOverride, *kept.Basis)
	assert.Equal(t, override, *kept.OverrideID)
	assert.Equal(t, manual, *kept.ResultID)

	moved := f.selection(credits[1])
	assert.NotEqual(t, *before.ResultID, *moved.ResultID, "the credit rests on the new version's result")
	assert.Equal(t, 1, f.count(`SELECT count(*) FROM contributor_normalization_result
		WHERE id = ? AND normalizer_version = ?`, *moved.ResultID, v2))

	var staleJob int64
	_, err := f.db.QueryOne(pg.Scan(&staleJob), `SELECT id FROM contributor_normalization_job
		WHERE source_fingerprint = ?`, authornorm.SourceFingerprint(initialed(t)))
	require.NoError(t, err)
	assert.Equal(t, []string{"normalizer_version_mismatch", "max_attempts_exceeded"}, f.attemptClasses(staleJob))
	assert.Nil(t, f.selection(stale[0]), "another version's job resolves nothing")
	assert.Equal(t, 0, f.resultsFor(authornorm.SourceFingerprint(initialed(t))))
}

// RED 8: replicas running concurrently produce one result per key, one
// resolution and one audit record per credit.
func workerConcurrentReplicasConverge(t *testing.T, f *workerFixture) {
	f.registerStructuredPerson()
	sources := []authornorm.SourceValue{tolstoy(t), chekhov(t), initialed(t), garbage(t), unknownAuthor(t)}
	var credits []int64
	for round := 0; round < 4; round++ {
		for _, v := range sources {
			credits = append(credits, f.persistBook(author(v))...)
		}
	}

	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		w := f.worker(func(c *AuthorMetadataLocalWorkerConfig) { c.ClaimLimit = 1 })
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				report, err := w.RunOnce(context.Background())
				if err != nil {
					errs <- err
					return
				}
				if report.Claimed == 0 {
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	f.drain(f.worker(nil))

	for _, v := range sources {
		assert.Equal(t, 1, f.resultsFor(authornorm.SourceFingerprint(v)))
	}
	assert.Equal(t, len(sources), f.count(`SELECT count(*) FROM contributor_normalization_job WHERE status = 'completed'`))
	assert.Equal(t, len(credits), f.count(`SELECT count(*) FROM book_contributor_credit_selection`))
	for _, credit := range credits {
		assert.Equal(t, 1, f.audits(credit), "credit %d", credit)
	}
	assert.Equal(t, 1, f.openReviewItems(authornorm.SourceFingerprint(initialed(t))))
}

// RED 8 (fencing half): a worker whose lease was reclaimed cannot record its
// late result; the new owner's result and resolutions stand alone.
func workerLateOwnerCannotOverwrite(t *testing.T, f *workerFixture) {
	credits := f.persistBook(author(tolstoy(t)))
	ctx := context.Background()
	late := database.NewLeaseOwner()
	claims, err := database.ClaimLocalNormalizationJobs(ctx, f.db, late,
		database.LeaseClaimOptions{Limit: 1, Lease: time.Millisecond, MaxAttempts: 3})
	require.NoError(t, err)
	require.Len(t, claims, 1)
	time.Sleep(5 * time.Millisecond)

	f.drain(f.worker(nil))
	winner := f.selection(credits[0])
	require.NotNil(t, winner)

	r, err := authornorm.Normalize(tolstoy(t), workerExtractor)
	require.NoError(t, err)
	d, err := authornorm.ProductionAcceptancePolicy().Decide(&r)
	require.NoError(t, err)
	err = f.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		_, recordErr := database.RecordLocalNormalization(ctx, tx, claims[0].ID, late, &r, d)
		return recordErr
	})
	assert.ErrorIs(t, err, authornorm.ErrLeaseLost)
	assert.Equal(t, 1, f.resultsFor(authornorm.SourceFingerprint(tolstoy(t))))
	assert.Equal(t, 1, f.audits(credits[0]))
	assert.Equal(t, *winner, *f.selection(credits[0]))
}

// Resolving a credit again in a later transaction with the outcome it already
// has changes no row: no audit record, no new decision time, no second review
// item. (Inside one transaction now() would hide a rewrite.)
func workerRepeatedResolutionWritesNothing(t *testing.T, f *workerFixture) {
	credits := f.persistBook(author(tolstoy(t)), author(initialed(t)))
	f.drain(f.worker(nil))
	ctx := context.Background()

	for i, v := range []authornorm.SourceValue{tolstoy(t), initialed(t)} {
		before := f.selection(credits[i])
		require.NotNil(t, before)
		require.NotNil(t, before.ResultID)
		r, err := authornorm.Normalize(v, workerExtractor)
		require.NoError(t, err)
		d, err := authornorm.ProductionAcceptancePolicy().Decide(&r)
		require.NoError(t, err)
		err = f.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
			_, resolveErr := database.ResolveCredits(ctx, tx, r.SourceFingerprint[:], workerExtractor,
				&database.AutomaticOutcome{ResultID: *before.ResultID, DecisionClass: r.DecisionClass, Decision: d})
			return resolveErr
		})
		require.NoError(t, err)

		assert.Equal(t, 1, f.audits(credits[i]))
		assert.Equal(t, before.DecidedAt, f.selection(credits[i]).DecidedAt)
	}
	assert.Equal(t, 1, f.openReviewItems(authornorm.SourceFingerprint(initialed(t))))
}

// Fix round 1 (review B1): a credit persisted after its normalization key's
// job completed gets no job of its own (the key is queued once). It must still
// be resolved from the key's stored result, under the current policy, without
// normalizing again and without a second result.

// lateCredit persists v, drains, then persists another book with late and
// drains again with the same counting normalizer. It returns the first and
// the late credit.
func (f *workerFixture) lateCredit(v, late authornorm.SourceValue, spy *countingNormalizer) (first, second int64) {
	f.t.Helper()
	w := f.worker(func(c *AuthorMetadataLocalWorkerConfig) { c.Normalize = spy.normalize })
	firstIDs := f.persistBook(author(v))
	f.drain(w)
	require.NotNil(f.t, f.selection(firstIDs[0]))
	secondIDs := f.persistBook(author(late))
	f.drain(w)
	report, err := w.RunOnce(context.Background())
	require.NoError(f.t, err)
	assert.Equal(f.t, 0, report.Claimed, "the late credit brings no job of its own")
	return firstIDs[0], secondIDs[0]
}

func workerLateCreditEmptyPolicy(t *testing.T, f *workerFixture) {
	spy := &countingNormalizer{}
	first, second := f.lateCredit(tolstoy(t), tolstoy(t), spy)
	lateInitials := f.persistBook(author(initialed(t)))
	f.drain(f.worker(nil))
	lateInitialsAgain := f.persistBook(author(initialed(t)))
	f.drain(f.worker(nil))

	s := f.selection(second)
	require.NotNil(t, s, "a late credit of a completed key must be resolved")
	assert.Equal(t, models.CreditSelectionUnresolved, s.State)
	assert.Equal(t, models.UnresolvedPolicyNotRegistered, *s.UnresolvedReason)
	assert.Equal(t, *f.selection(first).ResultID, *s.ResultID, "the late credit shares the key's result")
	assert.Equal(t, int64(1), spy.calls.Load(), "the stored result is reused, not recomputed")
	assert.Equal(t, 1, f.resultsFor(authornorm.SourceFingerprint(tolstoy(t))))
	assert.Equal(t, 1, f.audits(first), "the earlier credit is not rewritten")

	require.NotNil(t, f.selection(lateInitialsAgain[0]))
	assert.Equal(t, models.CreditSelectionReview, f.selection(lateInitialsAgain[0]).State)
	assert.Equal(t, models.CreditSelectionReview, f.selection(lateInitials[0]).State)
	assert.Equal(t, 1, f.openReviewItems(authornorm.SourceFingerprint(initialed(t))))

	a := f.accounting()
	assert.Zero(t, a.Pending)
	assert.True(t, a.Settled())
}

func workerLateCreditRegisteredPolicy(t *testing.T, f *workerFixture) {
	f.registerStructuredPerson()
	spy := &countingNormalizer{}
	first, second := f.lateCredit(tolstoy(t), tolstoy(t), spy)

	s := f.selection(second)
	require.NotNil(t, s, "a late credit of a completed key must be resolved")
	assert.Equal(t, models.CreditSelectionSelected, s.State)
	assert.Equal(t, models.CreditSelectionAutomatic, *s.Basis)
	assert.Equal(t, "2", *s.PolicyVersion)
	assert.Equal(t, *f.selection(first).ResultID, *s.ResultID)
	assert.Equal(t, int64(1), spy.calls.Load())
	assert.Equal(t, 1, f.resultsFor(authornorm.SourceFingerprint(tolstoy(t))))
	assert.Zero(t, f.accounting().Pending)
}

// A late credit repeating an empty child keeps the fingerprint and key but
// carries the duplicate flag: the input is ambiguous now, so every credit of
// it — the earlier, already selected one included — goes to review instead
// of inheriting the registered decision.
func workerLateDuplicateCreditFailsClosed(t *testing.T, f *workerFixture) {
	f.registerStructuredPerson()
	plain := source(t, "first", "Иван", "last", "Петров")
	repeated := source(t, "first", "Иван", "first", "", "last", "Петров")
	require.Equal(t, authornorm.SourceFingerprint(plain), authornorm.SourceFingerprint(repeated))
	spy := &countingNormalizer{}
	first, second := f.lateCredit(plain, repeated, spy)

	late := f.selection(second)
	require.NotNil(t, late, "a late duplicate credit of a completed key must be resolved")
	assert.Equal(t, models.CreditSelectionReview, late.State)
	earlier := f.selection(first)
	assert.Equal(t, models.CreditSelectionReview, earlier.State, "the input is ambiguous for every credit now")
	assert.Equal(t, 0, f.count(`SELECT count(*) FROM book_contributor_credit_selection WHERE state = 'selected'`))
	assert.Equal(t, 1, f.count(`SELECT count(*) FROM contributor_review_item
		WHERE scope_fingerprint = ? AND status = 'open' AND decision_class = 'duplicate_component'`,
		authornorm.SourceFingerprint(plain)))
	assert.Equal(t, 1, f.resultsFor(authornorm.SourceFingerprint(plain)))
	assert.Zero(t, f.accounting().Pending)
}

// A duplicate credit can be persisted while its key's job is between loading
// its input and recording the result; the record then resolves it with the
// plain reading. The next batch moves every automatic resolution of the input
// to review, leaving overrides alone.
func workerDuplicateResolvedAsPlainReconciled(t *testing.T, f *workerFixture) {
	f.registerStructuredPerson()
	plain := source(t, "first", "Иван", "last", "Петров")
	repeated := source(t, "first", "Иван", "first", "", "last", "Петров")
	fp := authornorm.SourceFingerprint(plain)
	w := f.worker(nil)
	first := f.persistBook(author(plain))
	overridden := f.persistBook(author(plain))
	_, override := f.manualOverride(fp, overridden[0])
	f.drain(w)
	stored := f.selection(first[0])
	require.Equal(t, models.CreditSelectionSelected, stored.State)

	late := f.persistBook(author(repeated))
	ctx := context.Background()
	r, err := database.LoadLocalResult(ctx, f.db, *stored.ResultID)
	require.NoError(t, err)
	d, err := authornorm.ProductionAcceptancePolicy().Decide(&r)
	require.NoError(t, err)
	require.Equal(t, authornorm.OutcomeUnresolved, d.Outcome, "the shipped policy registers nothing")
	policy, err := database.LoadAcceptancePolicy(ctx, f.db, 2, authornorm.NormalizerVersion)
	require.NoError(t, err)
	d, err = policy.Decide(&r)
	require.NoError(t, err)
	err = f.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		_, resolveErr := database.ResolveCredits(ctx, tx, fp[:], workerExtractor,
			&database.AutomaticOutcome{ResultID: *stored.ResultID, DecisionClass: r.DecisionClass, Decision: d})
		return resolveErr
	})
	require.NoError(t, err)
	require.Equal(t, models.CreditSelectionSelected, f.selection(late[0]).State, "the race left the plain reading")

	_, err = w.RunOnce(ctx)
	require.NoError(t, err)
	for _, credit := range []int64{first[0], late[0]} {
		assert.Equal(t, models.CreditSelectionReview, f.selection(credit).State, "credit %d", credit)
	}
	kept := f.selection(overridden[0])
	assert.Equal(t, models.CreditSelectionCreditOverride, *kept.Basis)
	assert.Equal(t, override, *kept.OverrideID)

	report, err := w.RunOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, report.Reconciled, "a reconciled input is not listed again")
}

// RED 9: a run completes with an open review backlog, never with a pending
// local job or an unaccounted credit.
func workerCompletionAccounting(t *testing.T, f *workerFixture) {
	var run int64
	_, err := f.db.QueryOne(pg.Scan(&run), `INSERT INTO author_metadata_run
		(mode, status, extractor_version, normalizer_version, selector_archive, started_at)
		VALUES ('smoke', 'running', ?, ?, 'seed.zip', now()) RETURNING id`, workerExtractor, authornorm.NormalizerVersion)
	require.NoError(t, err)
	for _, v := range []authornorm.SourceValue{tolstoy(t), initialed(t)} {
		book, _ := f.persist(authornorm.NormalizerVersion, author(v))
		f.exec(`INSERT INTO author_metadata_run_item (run_id, book_id) VALUES (?, ?)`, run, book)
	}
	account := func() database.CreditAccounting {
		a, accountErr := database.AuthorCreditAccountingForRun(context.Background(), f.db, run)
		require.NoError(t, accountErr)
		return a
	}

	before := account()
	assert.Equal(t, 2, before.Pending)
	assert.False(t, before.Settled())

	w := f.worker(func(c *AuthorMetadataLocalWorkerConfig) { c.ClaimLimit = 1 })
	_, err = w.RunOnce(context.Background())
	require.NoError(t, err)
	half := account()
	assert.Equal(t, 1, half.Pending, "a pending local job keeps its credit unaccounted")
	assert.False(t, half.Settled())

	f.drain(w)
	after := account()
	assert.Equal(t, database.CreditAccounting{
		Credits:    2,
		Review:     1,
		Unresolved: map[models.UnresolvedReason]int{models.UnresolvedPolicyNotRegistered: 1},
	}, after)
	assert.True(t, after.Settled(), "an open review backlog does not block completion")
}

// outcomeKey is a credit's resolution in a form two seedings can compare.
type outcomeKey struct {
	State  models.CreditSelectionState
	Reason string
	Class  string
	Basis  string
}

// RED 10: the same seed processed in opposite job orders ends in the same
// resolutions and counts.
func workerOutOfOrderCompletion(t *testing.T, f *workerFixture) {
	sources := []authornorm.SourceValue{tolstoy(t), chekhov(t), initialed(t), garbage(t), unknownAuthor(t), tolstoy(t)}
	seedAndRun := func(reverse bool) ([]outcomeKey, database.CreditAccounting) {
		resetPipeline(t, f.db)
		f.registerStructuredPerson()
		var credits []int64
		for _, v := range sources {
			credits = append(credits, f.persistBook(author(v), author(initialed(t)))...)
		}
		order := "id"
		if reverse {
			order = "-id"
		}
		f.exec(`UPDATE contributor_normalization_job
			SET next_attempt_at = now() - interval '1 hour' + (` + order + `) * interval '1 second'`)
		f.drain(f.worker(func(c *AuthorMetadataLocalWorkerConfig) { c.ClaimLimit = 1 }))

		keys := make([]outcomeKey, len(credits))
		for i, credit := range credits {
			var k struct {
				State  models.CreditSelectionState
				Reason string
				Class  string
				Basis  string
			}
			_, err := f.db.QueryOne(&k, `SELECT s.state, coalesce(s.unresolved_reason, '') AS reason,
					coalesce(r.decision_class, '') AS class, coalesce(s.basis, '') AS basis
				FROM book_contributor_credit_selection s
				LEFT JOIN contributor_normalization_result r ON r.id = s.result_id
				WHERE s.credit_id = ?`, credit)
			require.NoError(t, err)
			keys[i] = outcomeKey(k)
		}
		return keys, f.accounting()
	}

	forward, forwardCounts := seedAndRun(false)
	backward, backwardCounts := seedAndRun(true)
	assert.Equal(t, forward, backward)
	assert.Equal(t, forwardCounts, backwardCounts)
	assert.True(t, forwardCounts.Settled())
	assert.Equal(t, 3, forwardCounts.Selected)
}

// Fix round 2 (re-review round 1): failed-job settlement must hold the credit
// lock from the override read to the selection write, like every other
// selection path, so a concurrent override cannot be overwritten by it.

// selectionWritePause pauses the first selection write of a connection's
// sessions, after the resolver has read whether an override exists.
type selectionWritePause struct {
	once    sync.Once
	ready   chan struct{}
	release chan struct{}
}

func (p *selectionWritePause) BeforeQuery(ctx context.Context, e *pg.QueryEvent) (context.Context, error) {
	if q, ok := e.Query.(string); ok && strings.Contains(q, "INSERT INTO book_contributor_credit_selection") {
		p.once.Do(func() {
			close(p.ready)
			<-p.release
		})
	}
	return ctx, nil
}

func (p *selectionWritePause) AfterQuery(context.Context, *pg.QueryEvent) error { return nil }

// waitBlockedOrDone waits until done is closed or some session of the
// database waits on a lock.
func waitBlockedOrDone(t *testing.T, s *pg.DB, done <-chan struct{}) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			return
		default:
		}
		var waiting int
		_, err := s.QueryOne(pg.Scan(&waiting), `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`)
		require.NoError(t, err)
		if waiting > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the manual resolution neither finished nor waited on a lock")
}

// The reviewer's reproduction through the worker itself: the worker settles a
// failed job's credit, reads that no override exists and is paused before its
// write; a manual transaction writes a credit override and resolves the
// credit; then the worker goes on.
func TestFailedSettlementKeepsConcurrentOverride(t *testing.T) {
	s := localWorkerDB(t)
	f := &workerFixture{t: t, db: s}
	ctx := context.Background()
	credits := f.persistBook(author(tolstoy(t)))
	fp := authornorm.SourceFingerprint(tolstoy(t))
	f.exec(`UPDATE contributor_normalization_job
		SET status = 'failed', last_error_class = 'normalizer_failed', finished_at = now()`)

	paused := pg.Connect(s.Options())
	t.Cleanup(func() { _ = paused.Close() })
	pause := &selectionWritePause{ready: make(chan struct{}), release: make(chan struct{})}
	paused.AddQueryHook(pause)
	cfg := testWorkerConfig()
	w, err := NewAuthorMetadataLocalWorker(paused, &cfg)
	require.NoError(t, err)
	workerDone := make(chan error, 1)
	go func() {
		_, runErr := w.RunOnce(ctx)
		workerDone <- runErr
	}()
	select {
	case <-pause.ready:
	case err = <-workerDone:
		t.Fatalf("the worker ended before settling: %v", err)
	}

	manualTx, err := s.Begin()
	require.NoError(t, err)
	var manual, override int64
	_, err = manualTx.QueryOne(pg.Scan(&manual), `INSERT INTO contributor_normalization_result
		(source_fingerprint, result_schema_version, method, kind, status, display_name, search_key, created_by_user_id)
		VALUES (?, 'manual-v1', 'manual', 'person', 'normalized', 'Manual', 'manual', 1) RETURNING id`, fp[:])
	require.NoError(t, err)
	_, err = manualTx.QueryOne(pg.Scan(&override), `INSERT INTO contributor_manual_override
		(scope_credit_id, source_fingerprint, result_id, created_by_user_id)
		VALUES (?, ?, ?, 1) RETURNING id`, credits[0], fp[:], manual)
	require.NoError(t, err)
	manualDone := make(chan struct{})
	var manualErr error
	go func() {
		defer close(manualDone)
		if manualErr = database.ResolveCreditSelection(ctx, manualTx, credits[0], nil); manualErr != nil {
			_ = manualTx.Rollback()
			return
		}
		manualErr = manualTx.Commit()
	}()
	waitBlockedOrDone(t, s, manualDone)

	close(pause.release)
	require.NoError(t, <-workerDone)
	<-manualDone
	require.NoError(t, manualErr)

	got := f.selection(credits[0])
	require.NotNil(t, got)
	require.NotNil(t, got.Basis, "state %s: the failed-job settlement overwrote the override", got.State)
	assert.Equal(t, models.CreditSelectionCreditOverride, *got.Basis)
	require.NotNil(t, got.OverrideID)
	assert.Equal(t, override, *got.OverrideID)
	assert.Equal(t, manual, *got.ResultID)
}
