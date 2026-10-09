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
func AuthorMetadataScanFailures(ctx context.Context, db pg.DBI, limit int) ([]AuthorMetadataScanFailure, error) {
	failures := []AuthorMetadataScanFailure{}
	_, err := db.QueryContext(ctx, &failures, `SELECT b.path AS archive, b.filename AS entry,
			CASE WHEN i.status = 'pending' THEN a.error_class ELSE i.status END AS class,
			i.updated_at AS at
		FROM author_metadata_run_item i
		JOIN opds_catalog_book b ON b.id = i.book_id
		LEFT JOIN LATERAL (
			SELECT error_class FROM author_metadata_run_item_attempt
			WHERE run_item_id = i.id ORDER BY attempt_no DESC LIMIT 1
		) a ON true
		WHERE i.run_id = (SELECT max(id) FROM author_metadata_run)
			AND (i.status IN ('entry_missing', 'invalid_fb2', 'unsupported_encoding', 'metadata_parse_failed')
				OR (i.status = 'pending' AND a.error_class IS NOT NULL))
		ORDER BY i.updated_at DESC, i.id DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("listing author metadata scan failures: %w", err)
	}
	for i := range failures {
		failures[i].Class = ClosedAuthorMetadataScanClass(failures[i].Class)
	}
	return failures, nil
}

// authorMetadataScanFailureClasses is the closed vocabulary a per-book
// failure of a run may show: the failure statuses an item ends in and the
// attempt classes the extraction worker and the lease layer record. The
// schema checks only the token's shape, so anything else is closed here.
var authorMetadataScanFailureClasses = []string{
	string(models.AuthorMetadataRunItemEntryMissing), string(models.AuthorMetadataRunItemInvalidFB2),
	string(models.AuthorMetadataRunItemUnsupportedEncoding), string(models.AuthorMetadataRunItemMetadataParseFailed),
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
