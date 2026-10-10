package database

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"gopds-api/internal/authornorm"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// The atomic source repository of the author metadata pipeline: the one place
// that writes an extraction's snapshot, its contributor credits and the local
// normalization jobs those credits need. Every method runs inside the caller's
// transaction (a *pg.Tx satisfies pg.DBI) and never begins, commits or rolls
// back on its own: the caller owns the transaction boundary, so a failure
// anywhere leaves the rollback decision — and the previous current snapshot —
// with the caller.

var (
	// ErrInvalidExtractionBookID marks a non-positive book ID.
	ErrInvalidExtractionBookID = errors.New("database: extraction book ID must be positive")
	// ErrInvalidExtractionRunID marks a run ID that does not fit the origin:
	// a backfill extraction needs a positive one, a live extraction none.
	ErrInvalidExtractionRunID = errors.New("database: extraction run ID does not fit the origin")
	// ErrInvalidExtractionOrigin marks an origin outside the closed set.
	ErrInvalidExtractionOrigin = errors.New("database: unknown snapshot origin")
	// ErrInvalidExtractionOutcome marks an outcome the credits contradict, or
	// a value outside the closed set.
	ErrInvalidExtractionOutcome = errors.New("database: extraction outcome does not match the credits")
	// ErrInvalidCreditRole marks a contributor role outside the closed set.
	ErrInvalidCreditRole = errors.New("database: unknown contributor role")
	// ErrSnapshotCurrentConflict marks a writer that lost the race to switch
	// a book's current snapshot to a different version; the database still
	// holds exactly one current snapshot.
	ErrSnapshotCurrentConflict = errors.New("database: another writer switched this book's current snapshot")
)

const (
	// sqlstateUniqueViolation is the PostgreSQL unique-violation code; the
	// named constraint tells the two snapshot indexes apart.
	sqlstateUniqueViolation = "23505"
	// oneCurrentSnapshotIndex is the partial unique index that keeps one
	// current snapshot per book.
	oneCurrentSnapshotIndex = "book_metadata_snapshot_one_current"
)

// PersistOutcome is what a PersistExtraction call did. It is an explicit
// value, not an error string to compare against.
type PersistOutcome int

const (
	// PersistNewSnapshot: the snapshot (and its credits and jobs) were
	// written inside the caller's transaction.
	PersistNewSnapshot PersistOutcome = iota
	// PersistAlreadyCurrent: the same extraction key was already persisted;
	// nothing new was written and SnapshotID names the existing row.
	PersistAlreadyCurrent
)

// PersistExtractionResult reports one PersistExtraction call.
type PersistExtractionResult struct {
	Outcome        PersistOutcome
	SnapshotID     int64
	CreditsWritten int
	JobsEnqueued   int
}

// ExtractionCredit is one contributor element of an extraction, in XML order.
// The source value is canonical (already trimmed and NFC): the repository
// derives the display name and the fingerprint from it, so a stored credit
// always matches the byte contract of its fingerprint.
type ExtractionCredit struct {
	Role          models.ContributorRole
	Source        authornorm.SourceValue
	SourceID      *string
	QualityFlags  []string
	XMLProvenance json.RawMessage
}

// ExtractionInput is one extraction's persistable payload: one extractor
// version applied to one file version of one book.
type ExtractionInput struct {
	BookID                int64
	BookMD5               string
	ExtractorVersion      string
	NormalizerVersion     string
	Origin                models.BookMetadataSnapshotOrigin
	RunID                 *int64
	Outcome               models.BookMetadataSnapshotOutcome
	ArchivePath           string
	EntryName             string
	XMLProvenance         json.RawMessage
	SourceTitle           *string
	SourceLang            *string
	SourceSrcLang         *string
	SourceISBNs           []string
	SourcePublisher       *string
	SourceCity            *string
	SourceYear            *string
	SourceDocumentID      *string
	SourceDocumentVersion *string
	SourceSequences       json.RawMessage
	QualityFlags          []string
	Credits               []ExtractionCredit
}

// validateExtractionInput is the pure half of the repository: every rule
// fails before any SQL runs, with a typed error, so an invalid extraction can
// never reach the database (and a nil connection can never be touched).
func validateExtractionInput(in *ExtractionInput) error {
	if err := validateExtractionIdentity(in); err != nil {
		return err
	}
	return validateExtractionCredits(in)
}

func validateExtractionIdentity(in *ExtractionInput) error {
	if in.BookID <= 0 {
		return ErrInvalidExtractionBookID
	}
	switch in.Origin {
	case models.BookMetadataSnapshotBackfill:
		if in.RunID == nil || *in.RunID <= 0 {
			return ErrInvalidExtractionRunID
		}
	case models.BookMetadataSnapshotLive:
		if in.RunID != nil {
			return ErrInvalidExtractionRunID
		}
	default:
		return ErrInvalidExtractionOrigin
	}
	for _, version := range []string{in.ExtractorVersion, in.NormalizerVersion} {
		if strings.TrimSpace(version) == "" {
			return authornorm.ErrEmptyVersion
		}
	}
	return nil
}

func validateExtractionCredits(in *ExtractionInput) error {
	authors := 0
	for i := range in.Credits {
		switch in.Credits[i].Role {
		case models.ContributorRoleAuthor, models.ContributorRoleTranslator:
		default:
			return ErrInvalidCreditRole
		}
		if in.Credits[i].Role == models.ContributorRoleAuthor {
			authors++
		}
		if in.Credits[i].Source.DisplayName() == "" {
			return authornorm.ErrNoNameComponents
		}
	}
	switch in.Outcome {
	case models.BookMetadataSnapshotExtracted:
		if authors == 0 {
			return ErrInvalidExtractionOutcome
		}
	case models.BookMetadataSnapshotExtractedNoAuthor:
		if authors != 0 {
			return ErrInvalidExtractionOutcome
		}
	default:
		return ErrInvalidExtractionOutcome
	}
	return nil
}

// creditRow is one credit's insertable columns, derived purely from the input.
type creditRow struct {
	role        models.ContributorRole
	position    int
	first       *string
	middle      *string
	last        *string
	nickname    *string
	sourceID    *string
	display     string
	fingerprint [32]byte
	flags       []string
	provenance  json.RawMessage
}

// buildCreditRows maps the ordered contributor elements to insertable rows:
// positions are zero-based within each role and follow the input (XML) order,
// and every fingerprint derives from the canonical source value, so the
// stored fields always reproduce the fingerprinted bytes.
func buildCreditRows(credits []ExtractionCredit) []creditRow {
	rows := make([]creditRow, len(credits))
	nextPosition := make(map[models.ContributorRole]int)
	for i := range credits {
		flags := credits[i].QualityFlags
		if flags == nil {
			flags = []string{}
		}
		rows[i] = creditRow{
			role:        credits[i].Role,
			position:    nextPosition[credits[i].Role],
			first:       credits[i].Source.First(),
			middle:      credits[i].Source.Middle(),
			last:        credits[i].Source.Last(),
			nickname:    credits[i].Source.Nickname(),
			sourceID:    credits[i].SourceID,
			display:     credits[i].Source.DisplayName(),
			fingerprint: authornorm.SourceFingerprint(credits[i].Source),
			flags:       flags,
			provenance:  credits[i].XMLProvenance,
		}
		nextPosition[credits[i].Role]++
	}
	return rows
}

// jsonOrDefault renders an optional JSON column with its schema default.
func jsonOrDefault(raw json.RawMessage, fallback string) string {
	if len(raw) == 0 {
		return fallback
	}
	return string(raw)
}

// snapshotParams builds the insert parameters for the snapshot row.
func snapshotParams(in *ExtractionInput) []interface{} {
	isbns := in.SourceISBNs
	if isbns == nil {
		isbns = []string{}
	}
	flags := in.QualityFlags
	if flags == nil {
		flags = []string{}
	}
	return []interface{}{
		in.BookID, in.BookMD5, in.ExtractorVersion, in.Origin, in.RunID, in.Outcome,
		in.ArchivePath, in.EntryName,
		jsonOrDefault(in.XMLProvenance, "{}"),
		in.SourceTitle, in.SourceLang, in.SourceSrcLang,
		pg.Array(isbns),
		in.SourcePublisher, in.SourceCity, in.SourceYear,
		in.SourceDocumentID, in.SourceDocumentVersion,
		jsonOrDefault(in.SourceSequences, "[]"),
		pg.Array(flags),
	}
}

const insertSnapshotSQL = `INSERT INTO book_metadata_snapshot
	(book_id, book_md5, extractor_version, origin, run_id, outcome,
	 archive_path, entry_name, xml_provenance, source_title, source_lang, source_src_lang,
	 source_isbns, source_publisher, source_city, source_year, source_document_id,
	 source_document_version, source_sequences, quality_flags, is_current)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?::jsonb, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?::jsonb, ?, false)
ON CONFLICT (book_id, book_md5, extractor_version) DO NOTHING
RETURNING id`

const insertCreditSQL = `INSERT INTO book_contributor_credit
	(snapshot_id, role, position, source_first_name, source_middle_name, source_last_name,
	 source_nickname, source_id, source_display_name, source_fingerprint, quality_flags, xml_provenance)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?::jsonb)`

const insertJobSQL = `INSERT INTO contributor_normalization_job
	(normalization_key, source_fingerprint, extractor_version, normalizer_version)
VALUES (?, ?, ?, ?)
ON CONFLICT (normalization_key) DO NOTHING`

// snapshotKey identifies the row behind an extraction key.
type snapshotKey struct {
	ID        int64
	IsCurrent bool
}

// snapshotByKey loads the snapshot of one extraction key.
func snapshotByKey(conn pg.DBI, bookID int64, md5, extractorVersion string) (snapshotKey, bool, error) {
	var key snapshotKey
	_, err := conn.QueryOne(&key, `SELECT id, is_current FROM book_metadata_snapshot
		WHERE book_id = ? AND book_md5 = ? AND extractor_version = ?`, bookID, md5, extractorVersion)
	if errors.Is(err, pg.ErrNoRows) {
		return snapshotKey{}, false, nil
	}
	if err != nil {
		return snapshotKey{}, false, err
	}
	return key, true, nil
}

// makeSnapshotCurrent switches the current marker to one existing snapshot
// inside the caller's transaction: the previous current is un-marked first,
// so the one-current index is never asked to hold two rows and a failure
// rolls the whole switch back. A concurrent switch by another version
// surfaces as the same typed conflict as on the new-snapshot path.
func makeSnapshotCurrent(conn pg.DBI, snapshotID, bookID int64) error {
	if _, err := conn.Exec(`UPDATE book_metadata_snapshot
			SET is_current = false WHERE book_id = ? AND is_current AND id <> ?`, bookID, snapshotID); err != nil {
		return translateSnapshotError(err)
	}
	_, err := conn.Exec(`UPDATE book_metadata_snapshot
		SET is_current = true WHERE id = ? AND NOT is_current`, snapshotID)
	return translateSnapshotError(err)
}

// translateSnapshotError maps the one-current unique violation to the typed
// conflict; everything else passes through unchanged.
func translateSnapshotError(err error) error {
	var pgErr pg.Error
	if errors.As(err, &pgErr) &&
		pgErr.Field('C') == sqlstateUniqueViolation &&
		pgErr.Field('n') == oneCurrentSnapshotIndex {
		return ErrSnapshotCurrentConflict
	}
	return err
}

// alreadyPersisted settles the idempotent path for a snapshot that exists:
// when it is current the call wrote nothing at all, and when it is not — the
// file returned to a superseded version — the marker switches back to it in
// the caller's transaction, still without new rows.
func alreadyPersisted(conn pg.DBI, key snapshotKey, bookID int64) (PersistExtractionResult, error) {
	if !key.IsCurrent {
		if err := makeSnapshotCurrent(conn, key.ID, bookID); err != nil {
			return PersistExtractionResult{}, err
		}
		// Another version's credits are the book's now.
		if err := MarkAuthorDisplayDirty(context.Background(), conn, bookID); err != nil {
			return PersistExtractionResult{}, err
		}
	}
	return PersistExtractionResult{Outcome: PersistAlreadyCurrent, SnapshotID: key.ID}, nil
}

// enqueueNormalizationJobs writes one pending local job per distinct author
// normalization key. Translator credits share fingerprints with authors but
// never enter the queue, and a key already queued (another credit, another
// book) is left alone: the job is the unit of work, not the credit.
func enqueueNormalizationJobs(conn pg.DBI, in *ExtractionInput, rows []creditRow) (int, error) {
	enqueued := 0
	seen := make(map[[32]byte]bool, len(rows))
	for i := range rows {
		if rows[i].role != models.ContributorRoleAuthor || seen[rows[i].fingerprint] {
			continue
		}
		seen[rows[i].fingerprint] = true
		key, err := authornorm.NormalizationKey(rows[i].fingerprint, in.ExtractorVersion, in.NormalizerVersion)
		if err != nil {
			return 0, err
		}
		result, err := conn.Exec(insertJobSQL, key[:], rows[i].fingerprint[:], in.ExtractorVersion, in.NormalizerVersion)
		if err != nil {
			return 0, err
		}
		enqueued += result.RowsAffected()
	}
	return enqueued, nil
}

// insertSnapshotCredits writes the ordered credit rows of one new snapshot.
func insertSnapshotCredits(conn pg.DBI, snapshotID int64, rows []creditRow) error {
	for i := range rows {
		if _, err := conn.Exec(insertCreditSQL,
			snapshotID, rows[i].role, rows[i].position, rows[i].first, rows[i].middle, rows[i].last,
			rows[i].nickname, rows[i].sourceID, rows[i].display, rows[i].fingerprint[:],
			pg.Array(rows[i].flags), jsonOrDefault(rows[i].provenance, "{}")); err != nil {
			return err
		}
	}
	return nil
}

// PersistExtraction writes one extraction atomically inside the caller's
// transaction: the snapshot, its ordered credits, and one local normalization
// job per distinct author normalization key (translators get none). The
// snapshot is inserted as not current and marked current only after the
// insert succeeds, in the same transaction, with the previous current
// un-marked first — so a book never has two current snapshots and never
// loses the one it has to a failed attempt. Repeating the same extraction key
// returns PersistAlreadyCurrent with the existing snapshot ID and writes
// nothing; a new MD5 or extractor version writes a new current snapshot and
// supersedes the old one. A concurrent writer that loses the switch race to
// another version gets ErrSnapshotCurrentConflict after rollback; one that
// repeats the winner's key converges on PersistAlreadyCurrent.
func PersistExtraction(conn pg.DBI, in *ExtractionInput) (PersistExtractionResult, error) {
	if err := validateExtractionInput(in); err != nil {
		return PersistExtractionResult{}, err
	}
	rows := buildCreditRows(in.Credits)

	// The book first, then its snapshots: the order the layer deletion takes.
	if err := lockSourceBook(context.Background(), conn, in.BookID); err != nil {
		return PersistExtractionResult{}, err
	}
	existing, found, err := snapshotByKey(conn, in.BookID, in.BookMD5, in.ExtractorVersion)
	if err != nil {
		return PersistExtractionResult{}, err
	}
	if found {
		return alreadyPersisted(conn, existing, in.BookID)
	}

	if _, unmarkErr := conn.Exec(`UPDATE book_metadata_snapshot
			SET is_current = false WHERE book_id = ? AND is_current`, in.BookID); unmarkErr != nil {
		return PersistExtractionResult{}, unmarkErr
	}

	var id int64
	_, err = conn.QueryOne(pg.Scan(&id), insertSnapshotSQL, snapshotParams(in)...)
	if err != nil && !errors.Is(err, pg.ErrNoRows) {
		return PersistExtractionResult{}, translateSnapshotError(err)
	}
	if errors.Is(err, pg.ErrNoRows) {
		// ON CONFLICT skipped: a concurrent writer committed this key first.
		// The row was inserted as not current, so the skip can only be the
		// key index — never the one-current index — and the winner's row is
		// committed with its current marker already set.
		existing, found, lookupErr := snapshotByKey(conn, in.BookID, in.BookMD5, in.ExtractorVersion)
		if lookupErr != nil {
			return PersistExtractionResult{}, lookupErr
		}
		if !found {
			return PersistExtractionResult{}, errors.New("database: conflicting snapshot disappeared before it could be read")
		}
		return alreadyPersisted(conn, existing, in.BookID)
	}

	// The snapshot becomes current only after the insert succeeded: the
	// un-mark above cleared the way in this transaction, and a concurrent
	// switch by another version surfaces here as the typed conflict.
	if _, markErr := conn.Exec(`UPDATE book_metadata_snapshot
			SET is_current = true WHERE id = ?`, id); markErr != nil {
		return PersistExtractionResult{}, translateSnapshotError(markErr)
	}

	if lockErr := lockForNewCredits(context.Background(), conn, in.ExtractorVersion, rows); lockErr != nil {
		return PersistExtractionResult{}, lockErr
	}
	if creditsErr := insertSnapshotCredits(conn, id, rows); creditsErr != nil {
		return PersistExtractionResult{}, creditsErr
	}

	jobs, err := enqueueNormalizationJobs(conn, in, rows)
	if err != nil {
		return PersistExtractionResult{}, err
	}
	if markErr := MarkAuthorDisplayDirty(context.Background(), conn, in.BookID); markErr != nil {
		return PersistExtractionResult{}, markErr
	}
	return PersistExtractionResult{
		Outcome:        PersistNewSnapshot,
		SnapshotID:     id,
		CreditsWritten: len(rows),
		JobsEnqueued:   jobs,
	}, nil
}

// lockForNewCredits takes what a writer holds while it adds credits: the
// author layer, shared, so no credit is added while a layer deletion decides
// which fingerprints are orphaned; then the inputs the author rows join — the
// acceptance pass decides an input and selects its credits under that lock,
// so a new author credit joins the input only while holding it and the pass
// never selects a credit its own ambiguity check did not see.
func lockForNewCredits(ctx context.Context, conn pg.DBI, extractorVersion string, rows []creditRow) error {
	if err := lockAuthorLayerShared(ctx, conn); err != nil {
		return err
	}
	return lockAuthorInputs(ctx, conn, extractorVersion, rows)
}

// ActiveOverrideForFingerprint returns the newest fingerprint-scoped manual
// override for the exact source fingerprint, or nil when none exists: it
// applies to credits written after it, and any source change produces another
// fingerprint that does not inherit it (contract 3.11).
func ActiveOverrideForFingerprint(conn pg.DBI, fingerprint []byte) (*models.ContributorManualOverride, error) {
	override := new(models.ContributorManualOverride)
	err := conn.Model(override).
		Where("scope_fingerprint = ?", fingerprint).
		Order("id DESC").
		Limit(1).
		Select()
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return override, nil
}

// ActiveOverrideForCredit returns the credit's active manual override: a
// credit-scoped override wins over a fingerprint-scoped one (contract 3.11
// precedence). nil, nil when the credit has none.
func ActiveOverrideForCredit(conn pg.DBI, creditID int64) (*models.ContributorManualOverride, error) {
	credit := new(models.BookContributorCredit)
	err := conn.Model(credit).Where("id = ?", creditID).Select()
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	override := new(models.ContributorManualOverride)
	err = conn.Model(override).
		Where("scope_credit_id = ?", creditID).
		Order("id DESC").
		Limit(1).
		Select()
	if errors.Is(err, pg.ErrNoRows) {
		return ActiveOverrideForFingerprint(conn, credit.SourceFingerprint)
	}
	if err != nil {
		return nil, err
	}
	return override, nil
}
