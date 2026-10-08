package database

// Fixture helpers for the source repository tests (phase 6). They extend the
// phase-4 schema fixture of the same package with extraction inputs, manual
// override seeding, and a proxy that fails a caller-owned connection after a
// chosen step — the failure injection the atomicity tests need, without the
// repository ever owning a transaction boundary.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gopds-api/internal/authornorm"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/require"
)

// nameValue builds one canonical source value from ordered name-bearing
// children, exactly the way the extractor will.
func nameValue(t *testing.T, components ...authornorm.SourceComponent) authornorm.SourceValue {
	t.Helper()
	value, err := authornorm.NewSourceValue(components)
	require.NoError(t, err)
	return value
}

func nameComponent(kind authornorm.ComponentKind, value string) authornorm.SourceComponent {
	return authornorm.SourceComponent{Kind: kind, Value: value}
}

// creditOf builds a credit "First Last" of the given role.
func creditOf(t *testing.T, role models.ContributorRole, first, last string) ExtractionCredit {
	t.Helper()
	return ExtractionCredit{
		Role: role,
		Source: nameValue(t,
			nameComponent(authornorm.ComponentFirst, first),
			nameComponent(authornorm.ComponentLast, last)),
	}
}

// extractionSpec fills an ExtractionInput with a complete default payload;
// every field the tests do not name gets a valid, observable value.
type extractionSpec struct {
	book       int64
	md5        string
	extractor  string
	normalizer string
	origin     models.BookMetadataSnapshotOrigin
	run        *int64
	outcome    models.BookMetadataSnapshotOutcome
	credits    []ExtractionCredit
}

func (f *authorSchemaFixture) extraction(s *extractionSpec) *ExtractionInput {
	f.t.Helper()
	if s.md5 == "" {
		s.md5 = bookMD5(s.book)
	}
	if s.extractor == "" {
		s.extractor = "extractor-v1"
	}
	if s.normalizer == "" {
		s.normalizer = "normalizer-v1"
	}
	if s.origin == "" {
		s.origin = models.BookMetadataSnapshotLive
	}
	if s.outcome == "" {
		s.outcome = models.BookMetadataSnapshotExtracted
	}
	return &ExtractionInput{
		BookID:                s.book,
		BookMD5:               s.md5,
		ExtractorVersion:      s.extractor,
		NormalizerVersion:     s.normalizer,
		Origin:                s.origin,
		RunID:                 s.run,
		Outcome:               s.outcome,
		ArchivePath:           "fixture.zip",
		EntryName:             fmt.Sprintf("%d.fb2", s.book),
		XMLProvenance:         json.RawMessage(`{"root":"FictionBook"}`),
		SourceTitle:           ptr("Title"),
		SourceLang:            ptr(""),
		SourceISBNs:           []string{"", "978-5-1"},
		SourceCity:            ptr("Город"),
		SourceYear:            ptr("2001"),
		SourceDocumentID:      ptr("doc-id"),
		SourceDocumentVersion: ptr("1.1"),
		SourceSequences:       json.RawMessage(`[{"name":"S","number":"1"}]`),
		QualityFlags:          []string{"flag"},
		Credits:               s.credits,
	}
}

// count runs a scalar count query on the fixture transaction.
func (f *authorSchemaFixture) count(query string, params ...interface{}) int {
	f.t.Helper()
	var n int
	_, err := f.tx.QueryOne(pg.Scan(&n), query, params...)
	require.NoError(f.t, err, query)
	return n
}

// snapshotCount counts the book's snapshots, current or not.
func (f *authorSchemaFixture) snapshotCount(book int64) int {
	return f.count(`SELECT count(*) FROM book_metadata_snapshot WHERE book_id = ?`, book)
}

// currentSnapshots lists the book's current snapshot IDs; the contract allows
// exactly one, so callers assert on the whole slice.
func (f *authorSchemaFixture) currentSnapshots(book int64) []int64 {
	f.t.Helper()
	var ids []int64
	_, err := f.tx.Query(&ids, `SELECT id FROM book_metadata_snapshot
		WHERE book_id = ? AND is_current ORDER BY id`, book)
	require.NoError(f.t, err)
	return ids
}

// creditCountByFingerprint counts stored credits with the exact fingerprint.
func (f *authorSchemaFixture) creditCountByFingerprint(fingerprint []byte) int {
	return f.count(`SELECT count(*) FROM book_contributor_credit WHERE source_fingerprint = ?`, fingerprint)
}

// creditIDByFingerprint returns the credit of one snapshot with the exact
// fingerprint.
func (f *authorSchemaFixture) creditIDByFingerprint(snapshot int64, fingerprint []byte) int64 {
	f.t.Helper()
	return f.returningID(`SELECT id FROM book_contributor_credit
		WHERE snapshot_id = ? AND source_fingerprint = ?`, snapshot, fingerprint)
}

// jobCountByKey counts local normalization jobs with the exact key.
func (f *authorSchemaFixture) jobCountByKey(key [32]byte) int {
	return f.count(`SELECT count(*) FROM contributor_normalization_job WHERE normalization_key = ?`, key[:])
}

// manualResultFor seeds an immutable manual result about one fingerprint; the
// override tests build their overrides on it.
func (f *authorSchemaFixture) manualResultFor(fingerprint []byte, admin int64) int64 {
	f.t.Helper()
	return f.returningID(`INSERT INTO contributor_normalization_result
		(source_fingerprint, result_schema_version, method, kind, status, created_by_user_id)
		VALUES (?, 'manual-v1', 'manual', 'person', 'unresolved', ?)
		RETURNING id`, fingerprint, admin)
}

// fingerprintOverride seeds a fingerprint-scoped manual override.
func (f *authorSchemaFixture) fingerprintOverride(fingerprint []byte, result, admin int64) int64 {
	f.t.Helper()
	return f.returningID(`INSERT INTO contributor_manual_override
		(scope_fingerprint, source_fingerprint, result_id, created_by_user_id)
		VALUES (?, ?, ?, ?)
		RETURNING id`, fingerprint, fingerprint, result, admin)
}

// creditOverride seeds a credit-scoped manual override.
func (f *authorSchemaFixture) creditOverride(credit, result int64, fingerprint []byte, admin int64) int64 {
	f.t.Helper()
	return f.returningID(`INSERT INTO contributor_manual_override
		(scope_credit_id, source_fingerprint, result_id, created_by_user_id)
		VALUES (?, ?, ?, ?)
		RETURNING id`, credit, fingerprint, result, admin)
}

// errInjectedFailure is the error the failing proxy returns once a quota is
// spent; the repository must surface it unchanged.
var errInjectedFailure = errors.New("injected failure after the allowed statements")

// failAfterDBI wraps a caller-owned connection and refuses a statement once
// its marker's quota is spent: earlier statements ran for real, so the test
// sees exactly what a step failure leaves behind. It also refuses transaction
// control outright — the repository must drive only the caller's transaction.
type failAfterDBI struct {
	pg.DBI
	t      *testing.T
	quotas map[string]int
	counts map[string]int
}

func newFailAfterDBI(t *testing.T, conn pg.DBI, quotas map[string]int) *failAfterDBI {
	return &failAfterDBI{DBI: conn, t: t, quotas: quotas, counts: map[string]int{}}
}

// gate refuses transaction control always, and marked statements past their
// quota.
func (p *failAfterDBI) gate(query interface{}) error {
	sql, _ := query.(string)
	upper := strings.ToUpper(sql)
	if strings.HasPrefix(upper, "BEGIN") || strings.HasPrefix(upper, "COMMIT") ||
		strings.HasPrefix(upper, "ROLLBACK") || strings.HasPrefix(upper, "SAVEPOINT") {
		p.t.Fatalf("the repository drove the transaction itself: %s", sql)
	}
	for marker, quota := range p.quotas {
		if strings.Contains(sql, marker) {
			p.counts[marker]++
			if p.counts[marker] > quota {
				return errInjectedFailure
			}
		}
	}
	return nil
}

func (p *failAfterDBI) Exec(query interface{}, params ...interface{}) (pg.Result, error) {
	if err := p.gate(query); err != nil {
		return nil, err
	}
	return p.DBI.Exec(query, params...)
}

func (p *failAfterDBI) ExecOne(query interface{}, params ...interface{}) (pg.Result, error) {
	if err := p.gate(query); err != nil {
		return nil, err
	}
	return p.DBI.ExecOne(query, params...)
}

func (p *failAfterDBI) Query(model, query interface{}, params ...interface{}) (pg.Result, error) {
	if err := p.gate(query); err != nil {
		return nil, err
	}
	return p.DBI.Query(model, query, params...)
}

func (p *failAfterDBI) QueryOne(model, query interface{}, params ...interface{}) (pg.Result, error) {
	if err := p.gate(query); err != nil {
		return nil, err
	}
	return p.DBI.QueryOne(model, query, params...)
}

func (p *failAfterDBI) ExecContext(c context.Context, query interface{}, params ...interface{}) (pg.Result, error) {
	if err := p.gate(query); err != nil {
		return nil, err
	}
	return p.DBI.ExecContext(c, query, params...)
}

func (p *failAfterDBI) ExecOneContext(c context.Context, query interface{}, params ...interface{}) (pg.Result, error) {
	if err := p.gate(query); err != nil {
		return nil, err
	}
	return p.DBI.ExecOneContext(c, query, params...)
}

func (p *failAfterDBI) QueryContext(c context.Context, model, query interface{}, params ...interface{}) (pg.Result, error) {
	if err := p.gate(query); err != nil {
		return nil, err
	}
	return p.DBI.QueryContext(c, model, query, params...)
}

func (p *failAfterDBI) QueryOneContext(c context.Context, model, query interface{}, params ...interface{}) (pg.Result, error) {
	if err := p.gate(query); err != nil {
		return nil, err
	}
	return p.DBI.QueryOneContext(c, model, query, params...)
}

func (p *failAfterDBI) Begin() (*pg.Tx, error) {
	p.t.Fatal("the repository began its own transaction")
	return nil, nil
}

func (p *failAfterDBI) RunInTransaction(ctx context.Context, fn func(*pg.Tx) error) error {
	p.t.Fatal("the repository began its own transaction")
	return nil
}

// The concurrency test must commit its winner's rows for the race to resolve,
// and snapshots and credits are immutable once written, so it cannot clean
// them up. Its books come from a dedicated slice of the fixture ID range —
// above everything the rolled-back fixtures allocate, below the search
// fixture's precondition range — and each process run salts its start and
// probes upward, so reruns never collide with what earlier runs left behind.
const (
	concurrentBookBase int64 = 2_146_500_000
	// The span plus the probe headroom stays strictly below the search
	// fixture's precondition range, which starts at 2_147_000_000 and must
	// stay empty.
	concurrentBookSpan int64 = 300_000
	concurrentBookScan int64 = 100_000
)

var concurrentBookNext = func() *atomic.Int64 {
	next := new(atomic.Int64)
	next.Store(concurrentBookBase + time.Now().UnixNano()%concurrentBookSpan)
	return next
}()

// nextCommittedBook inserts and commits one fresh book row for the race. The
// row is deliberately retired from the reader catalog: approved = false keeps
// it out of every ordinary list (which the catalog tests draw their fixtures
// from), and the registerdate is backdated by an offset from now so no
// date-ordered view picks it up either.
func nextCommittedBook(t *testing.T, md5 string) int64 {
	t.Helper()
	requireDatabase(t)
	backdated := time.Now().Add(-20 * 365 * 24 * time.Hour)
	for i := int64(0); i < concurrentBookScan; i++ {
		id := concurrentBookNext.Add(1)
		res, err := db.Exec(`INSERT INTO opds_catalog_book
			(id, filename, path, format, registerdate, docdate, lang, title, annotation, md5, approved)
			VALUES (?, ?, 'race.zip', 'fb2', ?, '', 'ru', 'race fixture', '', ?, false)
			ON CONFLICT (id) DO NOTHING`, id, fmt.Sprintf("%d.fb2", id), backdated, md5)
		require.NoError(t, err)
		if res.RowsAffected() == 1 {
			return id
		}
	}
	t.Fatal("no free concurrent-fixture book ID found")
	return 0
}
