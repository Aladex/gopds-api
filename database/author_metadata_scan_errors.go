package database

import (
	"context"
	"fmt"
	"slices"
	"time"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// AuthorMetadataScanFailure is one book the newest author metadata run could
// not read: its archive and entry, and the closed class it failed with —
// never a name or a title.
type AuthorMetadataScanFailure struct {
	Archive string
	Entry   string
	Class   string
	At      time.Time
}

// AuthorMetadataScanFailures lists up to limit per-book failures of the
// newest run: items that ended in a failure status, and items still pending
// after a failed attempt, newest first. They belong to the scanning
// section's error list.
//
// Both halves start from what is rare, so the list stays cheap on a
// catalog-sized run: the failure statuses through their partial index, and
// the failed attempts — a pending item's latest attempt with a class —
// instead of every pending item's latest attempt.
func AuthorMetadataScanFailures(ctx context.Context, db pg.DBI, limit int) ([]AuthorMetadataScanFailure, error) {
	failures := []AuthorMetadataScanFailure{}
	_, err := db.QueryContext(ctx, &failures, `WITH newest AS MATERIALIZED (
			SELECT max(id) AS id FROM author_metadata_run
		), failed AS (
			SELECT i.id, i.book_id, i.status AS class, i.updated_at
			FROM author_metadata_run_item i
			WHERE i.run_id = (SELECT id FROM newest) AND i.status IN (?0)
			UNION ALL
			SELECT i.id, i.book_id, a.error_class, i.updated_at
			FROM author_metadata_run_item_attempt a
			JOIN author_metadata_run_item i ON i.id = a.run_item_id
			WHERE a.error_class IS NOT NULL
				AND i.run_id = (SELECT id FROM newest) AND i.status = 'pending'
				AND a.attempt_no = (SELECT max(attempt_no) FROM author_metadata_run_item_attempt l
					WHERE l.run_item_id = i.id)
		)
		SELECT b.path AS archive, b.filename AS entry, f.class, f.updated_at AS at
		FROM failed f
		JOIN opds_catalog_book b ON b.id = f.book_id
		ORDER BY f.updated_at DESC, f.id DESC
		LIMIT ?1`, pg.In(authorMetadataFailureStatuses), limit)
	if err != nil {
		return nil, fmt.Errorf("listing author metadata scan failures: %w", err)
	}
	for i := range failures {
		failures[i].Class = ClosedAuthorMetadataScanClass(failures[i].Class)
	}
	return failures, nil
}

// authorMetadataFailureStatuses are the item statuses of a book the run
// could not read.
var authorMetadataFailureStatuses = []string{
	string(models.AuthorMetadataRunItemEntryMissing), string(models.AuthorMetadataRunItemInvalidFB2),
	string(models.AuthorMetadataRunItemUnsupportedEncoding), string(models.AuthorMetadataRunItemMetadataParseFailed),
	string(models.AuthorMetadataRunItemArchiveMissing), string(models.AuthorMetadataRunItemArchiveUnreadable),
}

// authorMetadataScanFailureClasses is the closed vocabulary a per-book
// failure of a run may show: the failure statuses an item ends in and the
// attempt classes the extraction worker and the lease layer record. The
// schema checks only the token's shape, so anything else is closed here.
var authorMetadataScanFailureClasses = []string{
	string(models.AuthorMetadataRunItemEntryMissing), string(models.AuthorMetadataRunItemInvalidFB2),
	string(models.AuthorMetadataRunItemUnsupportedEncoding), string(models.AuthorMetadataRunItemMetadataParseFailed),
	string(models.AuthorMetadataRunItemArchiveMissing),
	LeaseErrorLeaseExpired, LeaseErrorMaxAttemptsExceeded, classTransientDatabase, classArchiveUnreadable,
	classExtractionFailed, classDatabaseInvariant, classVersionMismatch, classExtractorMisconfigured,
}

// authorMetadataScanFallbackClass is what a stored class outside the
// vocabulary reads as: a failure to extract, with no detail.
const authorMetadataScanFallbackClass = classExtractionFailed

// AuthorMetadataScanFailureClasses returns the closed vocabulary of a run's
// per-book failures.
func AuthorMetadataScanFailureClasses() []string {
	return slices.Clone(authorMetadataScanFailureClasses)
}

// ClosedAuthorMetadataScanClass returns class when it belongs to the closed
// vocabulary and the fallback class otherwise.
func ClosedAuthorMetadataScanClass(class string) string {
	if slices.Contains(authorMetadataScanFailureClasses, class) {
		return class
	}
	return authorMetadataScanFallbackClass
}
