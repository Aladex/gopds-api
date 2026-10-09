package services

// Phase 11 service tests: the admin DTOs over the review repository — one
// transaction per decision, the cursor through the service, linked books and
// the schema-incompatible flag on the detail, retries under the configured
// normalizer version, and the decision history view. The suite runs in its
// own scratch database.

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"gopds-api/config"
	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const reviewExtractor = "extractor-v1"

// reviewFixture seeds one ambiguous input behind one open review item on a
// committed transaction, through the real repositories.
type reviewFixture struct {
	t  *testing.T
	s  *pg.DB
	tx *pg.Tx
}

func newReviewFixture(t *testing.T) *reviewFixture {
	t.Helper()
	s := localWorkerDB(t)
	tx, err := s.Begin()
	require.NoError(t, err)
	return &reviewFixture{t: t, s: s, tx: tx}
}

func (f *reviewFixture) commit() {
	f.t.Helper()
	require.NoError(f.t, f.tx.Commit())
}

func (f *reviewFixture) exec(query string, params ...interface{}) {
	f.t.Helper()
	_, err := f.s.Exec(query, params...)
	require.NoError(f.t, err)
}

// seedItem writes one book, one current author credit of an ambiguous
// source, the stored local result, and the open review item the empty
// policy's review outcome opens. Returns the item's id and the credit's id.
func (f *reviewFixture) seedItem(pairs ...string) (itemID, credit int64) {
	f.t.Helper()
	source := source(f.t, pairs...)
	fp := authornorm.SourceFingerprint(source)

	book := time.Now().UnixNano() % 2_000_000
	f.exec(`INSERT INTO opds_catalog_book
		(id, filename, path, format, registerdate, docdate, lang, title, annotation, md5)
		VALUES (?, ?, 'fixture.zip', 'fb2', now(), '', 'ru', 'fixture', '', ?)`,
		book, "book.fb2", md5hex(book))

	in := &database.ExtractionInput{
		BookID: book, BookMD5: md5hex(book),
		ExtractorVersion: reviewExtractor, NormalizerVersion: authornorm.NormalizerVersion,
		Origin: models.BookMetadataSnapshotLive, Outcome: models.BookMetadataSnapshotExtracted,
		ArchivePath: "fixture.zip", EntryName: "book.fb2",
		XMLProvenance: []byte(`{}`), SourceISBNs: []string{},
		SourceSequences: []byte(`[]`), QualityFlags: []string{},
		Credits: []database.ExtractionCredit{{Role: models.ContributorRoleAuthor, Source: source}},
	}
	written, err := database.PersistExtraction(f.tx, in)
	require.NoError(f.t, err)
	var credits []int64
	_, err = f.tx.Query(&credits, `SELECT id FROM book_contributor_credit WHERE snapshot_id = ?`, written.SnapshotID)
	require.NoError(f.t, err)
	require.Len(f.t, credits, 1)
	credit = credits[0]

	result, err := authornorm.Normalize(source, reviewExtractor)
	require.NoError(f.t, err)
	require.Equal(f.t, authornorm.ClassInitials, result.DecisionClass,
		"fixture source no longer has the class the case needs")
	resultID, err := database.InsertLocalResult(context.Background(), f.tx, &result)
	require.NoError(f.t, err)
	completed, err := f.tx.Exec(`UPDATE contributor_normalization_job
		SET status = 'completed', result_id = ?, finished_at = now()
		WHERE normalization_key = ?`, resultID, result.NormalizationKey[:])
	require.NoError(f.t, err)
	require.Equal(f.t, 1, completed.RowsAffected(), "the fixture must complete the job it seeded")

	policy, err := database.LoadAcceptancePolicy(context.Background(), f.tx, 1, authornorm.NormalizerVersion)
	require.NoError(f.t, err)
	decision, err := policy.Decide(&result)
	require.NoError(f.t, err)
	_, err = database.ResolveCredits(context.Background(), f.tx, fp[:], reviewExtractor, &database.AutomaticOutcome{
		ResultID: resultID, DecisionClass: result.DecisionClass, Decision: decision,
	})
	require.NoError(f.t, err)

	var item int64
	_, err = f.tx.QueryOne(pg.Scan(&item), `SELECT id FROM contributor_review_item
		WHERE scope_fingerprint = ? AND status = 'open'`, fp[:])
	require.NoError(f.t, err)
	return item, credit
}

func md5hex(seed int64) string {
	return fmt.Sprintf("%032x", seed)
}

func reviewService(t *testing.T, s *pg.DB, cfg AuthorMetadataReviewConfig) *AuthorMetadataReviewService {
	t.Helper()
	service, err := NewAuthorMetadataReviewService(s, cfg)
	require.NoError(t, err)
	return service
}

func TestAuthorMetadataReviewServiceConfig(t *testing.T) {
	_, err := NewAuthorMetadataReviewService(nil, DefaultAuthorMetadataReviewConfig())
	assert.ErrorIs(t, err, ErrInvalidAuthorMetadataReviewConfig)
	for _, cfg := range []AuthorMetadataReviewConfig{
		{},
		{NormalizerVersion: " ", ResultSchemaVersion: 1, RetryMaxAttempts: 1},
		{NormalizerVersion: "v", ResultSchemaVersion: 0, RetryMaxAttempts: 1},
		{NormalizerVersion: "v", ResultSchemaVersion: 1, RetryMaxAttempts: 0},
	} {
		_, err = NewAuthorMetadataReviewService(&pg.DB{}, cfg)
		assert.ErrorIs(t, err, ErrInvalidAuthorMetadataReviewConfig, "cfg %+v", cfg)
	}
	assert.Equal(t, authornorm.NormalizerVersion, DefaultAuthorMetadataReviewConfig().NormalizerVersion)
	assert.Equal(t, authornorm.ResultSchemaVersion, DefaultAuthorMetadataReviewConfig().ResultSchemaVersion)
	assert.Equal(t, DefaultAuthorMetadataLocalWorkerConfig().Retry.MaxAttempts,
		DefaultAuthorMetadataReviewConfig().RetryMaxAttempts, "the retry budget is the shipped worker's")
}

// The configured retry budget is the configured local workers' budget, not
// the shipped default or the extraction stream's.
func TestAuthorMetadataReviewConfigFromServerConfig(t *testing.T) {
	c := config.AuthorMetadataConfig{
		Extraction:         config.AuthorMetadataStageConfig{MaxAttempts: 7},
		LocalNormalization: config.AuthorMetadataStageConfig{MaxAttempts: 2},
	}
	cfg := AuthorMetadataReviewConfigFrom(&c)
	assert.Equal(t, 2, cfg.RetryMaxAttempts)
	assert.Equal(t, AuthorMetadataLocalWorkerConfigFrom(&c).Retry.MaxAttempts, cfg.RetryMaxAttempts)
	assert.Equal(t, DefaultAuthorMetadataReviewConfig().NormalizerVersion, cfg.NormalizerVersion)
	assert.Equal(t, DefaultAuthorMetadataReviewConfig().ResultSchemaVersion, cfg.ResultSchemaVersion)
}

func TestAuthorMetadataReviewService(t *testing.T) {
	f := newReviewFixture(t)
	item, credit := f.seedItem("first", "И.", "last", "Петров")
	f.commit()
	ctx := context.Background()
	service := reviewService(t, f.s, AuthorMetadataReviewConfig{
		NormalizerVersion:   authornorm.NormalizerVersion,
		ResultSchemaVersion: authornorm.ResultSchemaVersion,
		RetryMaxAttempts:    DefaultAuthorMetadataReviewConfig().RetryMaxAttempts,
	})

	t.Run("list pages with the cursor", func(t *testing.T) {
		first, err := service.List(ctx, ReviewListOptions{Limit: 1})
		require.NoError(t, err)
		require.Len(t, first.Items, 1)
		assert.Equal(t, item, first.Items[0].ID)
		assert.Equal(t, models.ReviewAmbiguousDecision, first.Items[0].Reason)
		assert.NotEmpty(t, first.Items[0].Fingerprint)
		assert.Nil(t, first.Items[0].ScopeCredit)
		require.NotNil(t, first.Next)

		second, err := service.List(ctx, ReviewListOptions{Limit: 1, After: first.Next})
		require.NoError(t, err)
		assert.Empty(t, second.Items, "the queue holds one item")
		assert.Nil(t, second.Next)

		filtered, err := service.List(ctx, ReviewListOptions{Reason: models.ReviewIncompatibleManualSchema})
		require.NoError(t, err)
		assert.Empty(t, filtered.Items)
	})

	t.Run("detail carries the proposal and the linked books", func(t *testing.T) {
		detail, err := service.Detail(ctx, item)
		require.NoError(t, err)
		assert.Equal(t, item, detail.Item.ID)
		require.NotNil(t, detail.Proposal)
		assert.Equal(t, "И.", detail.Proposal.GivenName)
		assert.Equal(t, authornorm.ClassInitials, authornorm.DecisionClass(detail.Proposal.Class))
		assert.False(t, detail.SchemaIncompatible)
		require.Len(t, detail.LinkedBooks, 1)
		assert.Equal(t, "fixture", detail.LinkedBooks[0].Title)
	})

	t.Run("decide through the service, then refuse the repeat", func(t *testing.T) {
		correction := AdminCorrection{
			GivenName: "Иван", FamilyName: "Петров", DisplayName: "Иван Петров",
			SortName: "Петров, Иван", SearchKey: "иван петров", Script: "Cyrl", Kind: models.NormalizationPerson,
		}
		report, err := service.Edit(ctx, item, 7, &correction)
		require.NoError(t, err)
		assert.NotZero(t, report.ResultID)

		selected := selectionOf(t, f.s, credit)
		require.NotNil(t, selected)
		assert.Equal(t, models.CreditSelectionSelected, selected.State)
		require.NotNil(t, selected.ResultID)
		assert.Equal(t, report.ResultID, *selected.ResultID)

		// The actor lives on the immutable override, not on the selection the
		// resolver wrote.
		var actor int64
		_, err = f.s.QueryOne(pg.Scan(&actor), `SELECT created_by_user_id
			FROM contributor_manual_override WHERE id = ?`, *selected.OverrideID)
		require.NoError(t, err)
		assert.Equal(t, int64(7), actor)

		_, err = service.Accept(ctx, item, 7)
		assert.ErrorIs(t, err, database.ErrReviewConflict)

		history, err := service.DecisionHistory(ctx, credit)
		require.NoError(t, err)
		require.NotEmpty(t, history)
		assert.Equal(t, string(models.CreditSelectionSelected), history[len(history)-1].State)

		for _, id := range []int64{0, -1} {
			_, err = service.Edit(ctx, id, 7, &correction)
			assert.ErrorIs(t, err, ErrInvalidReviewRequest)
			_, err = service.Accept(ctx, item, id)
			assert.ErrorIs(t, err, ErrInvalidReviewRequest)
		}
		_, err = service.Detail(ctx, 0)
		assert.ErrorIs(t, err, ErrInvalidReviewRequest)
		_, err = service.DecisionHistory(ctx, 0)
		assert.ErrorIs(t, err, ErrInvalidReviewRequest)
	})
}

func TestAuthorMetadataReviewServiceRetryUsesConfiguredVersion(t *testing.T) {
	f := newReviewFixture(t)
	item, _ := f.seedItem("first", "С.", "last", "Другой")
	f.commit()
	ctx := context.Background()

	t.Run("a changed configured version queues a new job", func(t *testing.T) {
		service := reviewService(t, f.s, AuthorMetadataReviewConfig{
			NormalizerVersion: "authornorm-local-v9", ResultSchemaVersion: authornorm.ResultSchemaVersion,
			RetryMaxAttempts: DefaultAuthorMetadataReviewConfig().RetryMaxAttempts,
		})
		report, err := service.Retry(ctx, item, 3)
		require.NoError(t, err)
		assert.True(t, report.JobQueued, "a new version is a new key, so a new job")
		var jobs int
		_, err = f.s.QueryOne(pg.Scan(&jobs), `SELECT count(*) FROM contributor_normalization_job
			WHERE normalizer_version = 'authornorm-local-v9' AND status = 'pending'`)
		require.NoError(t, err)
		assert.Equal(t, 1, jobs)
		var decided models.ContributorReviewItem
		require.NoError(t, f.s.Model(&decided).Where("id = ?", item).Select())
		require.NotNil(t, decided.Resolution)
		assert.Equal(t, models.ReviewRetried, *decided.Resolution)
	})
}

func TestAuthorMetadataReviewServiceSchemaFlag(t *testing.T) {
	f := newReviewFixture(t)
	item, credit := f.seedItem("first", "И.", "last", "Петров")
	// An old-schema manual result behind a credit-scoped override, decided
	// directly through the repository on its own transaction.
	fp := authornorm.SourceFingerprint(source(f.t, "first", "И.", "last", "Петров"))
	f.commit()
	var oldResult int64
	_, err := f.s.QueryOne(pg.Scan(&oldResult), `INSERT INTO contributor_normalization_result
		(source_fingerprint, result_schema_version, method, kind, status, display_name, search_key, script,
		 quality_flags, created_by_user_id)
		VALUES (?, '0', 'manual', 'person', 'normalized', 'Старая Правка', 'старая правка', 'Cyrl', '{}', 5)
		RETURNING id`, fp[:])
	require.NoError(t, err)
	var override int64
	_, err = f.s.QueryOne(pg.Scan(&override), `INSERT INTO contributor_manual_override
		(scope_credit_id, source_fingerprint, result_id, created_by_user_id)
		VALUES (?, ?, ?, 5)
		RETURNING id`, credit, fp[:], oldResult)
	require.NoError(t, err)

	ctx := context.Background()
	service := reviewService(t, f.s, DefaultAuthorMetadataReviewConfig())

	opened, err := service.FlagIncompatibleManualSchemas(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, opened)

	var flagged int64
	_, err = f.s.QueryOne(pg.Scan(&flagged), `SELECT id FROM contributor_review_item
		WHERE scope_credit_id = ? AND reason = 'incompatible_manual_schema' AND status = 'open'`, credit)
	require.NoError(t, err)

	detail, err := service.Detail(ctx, flagged)
	require.NoError(t, err)
	assert.True(t, detail.SchemaIncompatible, "the detail reports the flag")
	require.NotNil(t, detail.Proposal, "the item carries the old manual result as its proposal")
	assert.Equal(t, "Старая Правка", detail.Proposal.DisplayName)
	require.Len(t, detail.LinkedBooks, 1)

	// The admin decides the flagged item like any other.
	report, err := service.Classify(ctx, flagged, 5, models.NormalizationCollective)
	require.NoError(t, err)
	assert.NotZero(t, report.ResultID)
	selected := selectionOf(t, f.s, credit)
	require.NotNil(t, selected)
	assert.Equal(t, models.CreditSelectionSelected, selected.State)

	opened, err = service.FlagIncompatibleManualSchemas(ctx)
	require.NoError(t, err)
	assert.Zero(t, opened, "the newest override is current-schema, nothing to flag")
	_ = item
}

func selectionOf(t *testing.T, s *pg.DB, credit int64) *models.BookContributorCreditSelection {
	t.Helper()
	selected := new(models.BookContributorCreditSelection)
	err := s.Model(selected).Where("credit_id = ?", credit).Select()
	if err == pg.ErrNoRows {
		return nil
	}
	require.NoError(t, err)
	return selected
}

// Probe for review finding 3: a default-sized page carries its continuation
// cursor, so a full page of 100 leaves the 101st item reachable.
func TestReviewServiceDefaultPageKeepsCursor(t *testing.T) {
	f := newReviewFixture(t)
	ctx := context.Background()

	seed, err := f.s.Begin()
	require.NoError(t, err)
	fp := byte(1)
	for i := 0; i < 101; i++ {
		if i == 100 {
			fp = 2
		}
		raw := make([]byte, 32)
		raw[0] = fp
		raw[1] = byte(i + 1)
		_, err = seed.Exec(`INSERT INTO contributor_review_item
			(scope_fingerprint, source_fingerprint, reason)
			VALUES (?, ?, 'ambiguous_decision')`, raw, raw)
		require.NoError(t, err)
	}
	require.NoError(t, seed.Commit())

	service := reviewService(t, f.s, DefaultAuthorMetadataReviewConfig())
	first, err := service.List(ctx, ReviewListOptions{})
	require.NoError(t, err)
	require.Len(t, first.Items, 100, "the default page is full")
	require.NotNil(t, first.Next, "a full default page must offer its continuation cursor")

	second, err := service.List(ctx, ReviewListOptions{After: first.Next})
	require.NoError(t, err)
	require.Len(t, second.Items, 1)
	assert.Nil(t, second.Next)
}

// openReviewItemOf returns the one open review item of a source.
func openReviewItemOf(t *testing.T, s *pg.DB, fp [32]byte) int64 {
	t.Helper()
	var item int64
	_, err := s.QueryOne(pg.Scan(&item), `SELECT id FROM contributor_review_item
		WHERE scope_fingerprint = ? AND status = 'open'`, fp[:])
	require.NoError(t, err)
	return item
}

// Fix round 2, finding (a): a manual retry must never leave a credit pending
// with no open review and no terminal accounting. The worker's attempt budget
// is the retry's budget too, so a retry the worker can no longer claim is
// refused with the item left open, and an acknowledged retry whose attempts
// all fail is accounted like any failed input.
func TestReviewServiceRetryNeverOrphansTheCredit(t *testing.T) {
	s := localWorkerDB(t)
	ctx := context.Background()
	fp := authornorm.SourceFingerprint(initialed(t))

	t.Run("a retry at the exhausted budget is refused and the item stays open", func(t *testing.T) {
		resetPipeline(t, s)
		f := &workerFixture{t: t, db: s}
		credits := f.persistBook(author(initialed(t)))

		// The shipped budget, used up: every attempt but the last fails, the
		// last completes and opens the review.
		budget := DefaultAuthorMetadataLocalWorkerConfig().Retry.MaxAttempts
		calls := 0
		w := f.worker(func(c *AuthorMetadataLocalWorkerConfig) {
			c.Retry.MaxAttempts = budget
			c.Normalize = func(v authornorm.SourceValue, extractor string) (authornorm.Result, error) {
				calls++
				if calls < budget {
					return authornorm.Result{}, errors.New("transient normalizer failure")
				}
				return authornorm.Normalize(v, extractor)
			}
		})
		f.drain(w)
		require.Equal(t, budget, calls)
		item := openReviewItemOf(t, s, fp)

		_, retryErr := reviewService(t, s, DefaultAuthorMetadataReviewConfig()).Retry(ctx, item, 7)
		_, err := w.RunOnce(ctx)
		require.NoError(t, err)

		assert.ErrorIs(t, retryErr, database.ErrReviewRetryExhausted,
			"the worker cannot execute this retry, so it is not acknowledged")
		assert.Equal(t, 1, f.openReviewItems(fp), "the refused retry leaves the item open")
		assert.Equal(t, models.CreditSelectionReview, f.selection(credits[0]).State)
		assert.Equal(t, 1, f.count(`SELECT count(*) FROM contributor_normalization_job WHERE status = 'completed'`),
			"the refused retry leaves the job as it was")
		assert.Equal(t, budget, calls, "nothing ran the normalizer again")
		a := f.accounting()
		assert.True(t, a.Settled(), "no credit is left pending: %+v", a)
	})

	// The re-review's scenario: a valid budget of one attempt throughout.
	t.Run("a one-attempt budget refuses the same-version retry", func(t *testing.T) {
		resetPipeline(t, s)
		f := &workerFixture{t: t, db: s}
		f.persistBook(author(initialed(t)))
		normalizer := &countingNormalizer{}
		w := f.worker(func(c *AuthorMetadataLocalWorkerConfig) {
			c.Retry.MaxAttempts = 1
			c.Normalize = normalizer.normalize
		})
		report, err := w.RunOnce(ctx)
		require.NoError(t, err)
		require.Equal(t, 1, report.Completed)
		item := openReviewItemOf(t, s, fp)

		cfg := DefaultAuthorMetadataReviewConfig()
		cfg.RetryMaxAttempts = 1
		_, err = reviewService(t, s, cfg).Retry(ctx, item, 7)
		require.ErrorIs(t, err, database.ErrReviewRetryExhausted)
		_, err = w.RunOnce(ctx)
		require.NoError(t, err)

		assert.Equal(t, int64(1), normalizer.calls.Load())
		assert.Equal(t, 1, f.openReviewItems(fp))
		a := f.accounting()
		assert.True(t, a.Settled(), "no credit is left pending: %+v", a)
		assert.Equal(t, 1, a.Review)
	})

	t.Run("an acknowledged retry runs the normalizer again and reopens the review", func(t *testing.T) {
		resetPipeline(t, s)
		f := &workerFixture{t: t, db: s}
		f.persistBook(author(initialed(t)))
		const budget = 2
		normalizer := &countingNormalizer{}
		w := f.worker(func(c *AuthorMetadataLocalWorkerConfig) {
			c.Retry.MaxAttempts = budget
			c.Normalize = normalizer.normalize
		})
		report, err := w.RunOnce(ctx)
		require.NoError(t, err)
		require.Equal(t, 1, report.Completed)
		item := openReviewItemOf(t, s, fp)

		cfg := DefaultAuthorMetadataReviewConfig()
		cfg.RetryMaxAttempts = budget
		_, err = reviewService(t, s, cfg).Retry(ctx, item, 7)
		require.NoError(t, err)
		report, err = w.RunOnce(ctx)
		require.NoError(t, err)

		assert.Equal(t, 1, report.Completed)
		assert.Equal(t, int64(2), normalizer.calls.Load(), "the retry ran the local normalizer")
		assert.NotEqual(t, item, openReviewItemOf(t, s, fp), "the same ambiguity opens a new item")
		a := f.accounting()
		assert.True(t, a.Settled(), "no credit is left pending: %+v", a)
		assert.Equal(t, 1, a.Review)
	})

	t.Run("an acknowledged retry whose attempts fail is accounted", func(t *testing.T) {
		resetPipeline(t, s)
		f := &workerFixture{t: t, db: s}
		credits := f.persistBook(author(initialed(t)))
		const budget = 2
		normalizer := &countingNormalizer{}
		first := f.worker(func(c *AuthorMetadataLocalWorkerConfig) {
			c.Retry.MaxAttempts = budget
			c.Normalize = normalizer.normalize
		})
		report, err := first.RunOnce(ctx)
		require.NoError(t, err)
		require.Equal(t, 1, report.Completed)
		item := openReviewItemOf(t, s, fp)

		_, err = reviewService(t, s, DefaultAuthorMetadataReviewConfig()).Retry(ctx, item, 7)
		require.NoError(t, err, "one attempt of the budget is left")

		failing := f.worker(func(c *AuthorMetadataLocalWorkerConfig) {
			c.Retry.MaxAttempts = budget
			c.Normalize = func(v authornorm.SourceValue, extractor string) (authornorm.Result, error) {
				normalizer.calls.Add(1)
				return authornorm.Result{}, errors.New("normalizer broke")
			}
		})
		f.drain(failing)

		assert.Equal(t, int64(budget), normalizer.calls.Load(), "the retry ran the normalizer again")
		assert.Equal(t, 1, f.count(`SELECT count(*) FROM contributor_normalization_job WHERE status = 'failed'`))
		selected := f.selection(credits[0])
		require.NotNil(t, selected)
		assert.Equal(t, models.CreditSelectionUnresolved, selected.State)
		if assert.NotNil(t, selected.UnresolvedReason) {
			assert.Equal(t, models.UnresolvedNormalizerFailed, *selected.UnresolvedReason)
		}
		a := f.accounting()
		assert.True(t, a.Settled(), "no credit is left pending: %+v", a)
	})
}
