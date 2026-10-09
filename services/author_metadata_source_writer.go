package services

import (
	"bytes"
	"crypto/md5" // #nosec G501 -- book-content identity, as the scan computes it
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"math"

	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/internal/parser"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// The source side of the live dual write: new books get their author
// metadata source snapshot in the same transaction as their legacy rows
// (contract 3.10). The extraction runs on the FB2 bytes the scan already
// holds, before the transaction; only the database writes happen inside it.

// AuthorMetadataExtractionStatus is the closed document class a failed
// extraction ends in (contract 3.3).
type AuthorMetadataExtractionStatus string

const (
	AuthorMetadataInvalidFB2          AuthorMetadataExtractionStatus = "invalid_fb2"
	AuthorMetadataUnsupportedEncoding AuthorMetadataExtractionStatus = "unsupported_encoding"
	AuthorMetadataParseFailed         AuthorMetadataExtractionStatus = "metadata_parse_failed"
)

var (
	// ErrAuthorSourceWriterMissing marks a scanner without a source writer:
	// it refuses to ingest instead of silently writing the legacy side alone.
	ErrAuthorSourceWriterMissing = errors.New("services: book scan has no author metadata source writer")
	// ErrAuthorSourceNotPrepared marks a persist call for a source that never
	// went through Prepare.
	ErrAuthorSourceNotPrepared = errors.New("services: author metadata source was not prepared")
)

// provisionalSourceBookID is the book ID the extraction runs with before the
// legacy insert has assigned the real one; Persist binds the extraction to
// the inserted book before anything is mapped or stored. It is out of reach of
// the catalog sequence, so a missed binding fails on the book foreign key
// instead of naming another book.
const provisionalSourceBookID int64 = math.MaxInt64

// ClassifyAuthorMetadataExtractionError maps an extraction error to its
// document class. Errors that are not a property of the document — a
// misconfigured extractor, a source read failure — are not classified.
func ClassifyAuthorMetadataExtractionError(err error) (AuthorMetadataExtractionStatus, bool) {
	var syntaxErr *xml.SyntaxError
	switch {
	case err == nil:
		return "", false
	case errors.Is(err, parser.ErrDamagedContent):
		return AuthorMetadataInvalidFB2, true
	case errors.Is(err, parser.ErrUnsupportedCharset),
		errors.Is(err, parser.ErrUndeclaredCharset),
		errors.Is(err, parser.ErrUnsupportedDeclaredCharset):
		return AuthorMetadataUnsupportedEncoding, true
	case errors.Is(err, parser.ErrMetadataLimit),
		errors.As(err, &syntaxErr),
		errors.Is(err, authornorm.ErrInvalidSourceMetadata):
		return AuthorMetadataParseFailed, true
	}
	return "", false
}

// AuthorMetadataSourceWriter extracts and persists the live source snapshot
// of a newly scanned book.
type AuthorMetadataSourceWriter struct {
	extractor parser.MetadataExtractor
}

// NewAuthorMetadataSourceWriter is the production source writer.
func NewAuthorMetadataSourceWriter() *AuthorMetadataSourceWriter {
	return &AuthorMetadataSourceWriter{extractor: parser.MetadataExtractor{
		MetadataMaxBytes: AuthorMetadataMaxBytes,
		ExtractorVersion: AuthorMetadataExtractorVersion,
	}}
}

// PreparedAuthorSource is the outcome of extracting one book before its
// transaction: either the source metadata or the document class it failed
// with.
type PreparedAuthorSource struct {
	metadata authornorm.SourceMetadata
	failure  AuthorMetadataExtractionStatus
}

// Extracted reports whether the extraction produced source metadata.
func (p *PreparedAuthorSource) Extracted() bool { return p != nil && p.failure == "" }

// Failure is the document class of a failed extraction, empty otherwise.
func (p *PreparedAuthorSource) Failure() AuthorMetadataExtractionStatus {
	if p == nil {
		return ""
	}
	return p.failure
}

// Prepare extracts the source metadata from the entry bytes the scan already
// read, with the MD5 it already computed. A document-class failure is part of
// the result, not an error: the legacy book still ingests. Any other error
// is systemic and returned.
func (w *AuthorMetadataSourceWriter) Prepare(content []byte, archivePath, entryName, bookMD5 string) (*PreparedAuthorSource, error) {
	md, err := w.extractor.Extract(parser.ExtractBookInput{
		Reader:      bytes.NewReader(content),
		BookID:      provisionalSourceBookID,
		ArchivePath: archivePath,
		EntryName:   entryName,
		KnownMD5:    bookMD5,
	})
	if err != nil {
		class, ok := ClassifyAuthorMetadataExtractionError(err)
		if !ok {
			return nil, fmt.Errorf("extracting author metadata: %w", err)
		}
		return &PreparedAuthorSource{failure: class}, nil
	}
	return &PreparedAuthorSource{metadata: md}, nil
}

// Persist writes a prepared extraction for the inserted book inside the
// caller's transaction. A failed extraction writes nothing and logs only the
// book ID and the closed class — never a name, a title or a file name.
func (w *AuthorMetadataSourceWriter) Persist(tx pg.DBI, bookID int64, p *PreparedAuthorSource) error {
	if p == nil {
		return ErrAuthorSourceNotPrepared
	}
	if !p.Extracted() {
		LogAuthorMetadataEvent(AuthorMetadataEventWarn, &AuthorMetadataEvent{
			Name: AuthorMetadataEventSourceSkipped, Stage: AuthorMetadataStageDualWrite, BookID: bookID, Status: string(p.failure),
		})
		return nil
	}
	md := p.metadata
	md.BookID = bookID
	in, err := ExtractionInputFromSourceMetadata(&md, models.BookMetadataSnapshotLive, nil)
	if err != nil {
		return fmt.Errorf("mapping author metadata: %w", err)
	}
	if _, err := database.PersistExtraction(tx, in); err != nil {
		return fmt.Errorf("persisting author metadata: %w", err)
	}
	LogAuthorMetadataEvent(AuthorMetadataEventDebug, &AuthorMetadataEvent{
		Name: AuthorMetadataEventSourcePersisted, Stage: AuthorMetadataStageDualWrite, BookID: bookID,
	})
	return nil
}

// AuthorMetadataRefreshUnavailable is the closed class a rescan path reports
// when the author layer could not be written for a reason that is not the
// document's: a systemic extraction error or a failed database write.
const AuthorMetadataRefreshUnavailable = "author_metadata_unavailable"

// AuthorMetadataErrorPrefix starts the scan error a layer failure becomes:
// the prefix and a closed class, never a name or a title.
const AuthorMetadataErrorPrefix = "author_metadata: "

// authorSourceHeadBytes is how much of a book a deferred refresh keeps: with a
// known MD5 the extractor never reads past </description>, and one byte over
// the metadata window lets an oversized description fail exactly as on the
// whole file.
const authorSourceHeadBytes = AuthorMetadataMaxBytes + 1

// AuthorSourceHead returns the part of a book's bytes a later refresh needs,
// and the MD5 of the whole book.
func AuthorSourceHead(content []byte) (head []byte, bookMD5 string) {
	// #nosec G401 -- book-content identity, not a security primitive
	sum := md5.Sum(content)
	return content[:min(int64(len(content)), authorSourceHeadBytes)], hex.EncodeToString(sum[:])
}

// RefreshExisting writes the author layer of an existing book from the bytes
// a rescan path already read, inside the caller's transaction, with the
// semantics of ingest: an unchanged book is already_current, a changed one
// gets a new current snapshot and jobs. It never fails the caller's legacy
// update: a document failure, a systemic extraction error or a failed write
// comes back as the closed class to report (empty when the layer is
// written), and a failed write is rolled back to a savepoint so the
// transaction stays usable.
func (w *AuthorMetadataSourceWriter) RefreshExisting(tx *pg.Tx, bookID int64, content []byte, archivePath, entryName string) string {
	head, bookMD5 := AuthorSourceHead(content)
	return w.RefreshExistingHead(tx, bookID, head, bookMD5, archivePath, entryName)
}

// RefreshExistingHead is RefreshExisting from a kept head and the book's MD5
// (AuthorSourceHead), for a path that read the book earlier.
func (w *AuthorMetadataSourceWriter) RefreshExistingHead(
	tx *pg.Tx, bookID int64, head []byte, bookMD5, archivePath, entryName string,
) string {
	p, err := w.Prepare(head, archivePath, entryName, bookMD5)
	if err != nil {
		logAuthorSourceRefreshFailed(bookID, err)
		return AuthorMetadataRefreshUnavailable
	}
	if !p.Extracted() {
		// Logs the skip with the book ID and the closed class only.
		_ = w.Persist(tx, bookID, p)
		return string(p.Failure())
	}
	if _, err = tx.Exec(`SAVEPOINT author_metadata_refresh`); err != nil {
		logAuthorSourceRefreshFailed(bookID, err)
		return AuthorMetadataRefreshUnavailable
	}
	if err = w.Persist(tx, bookID, p); err != nil {
		logAuthorSourceRefreshFailed(bookID, err)
		if _, rollbackErr := tx.Exec(`ROLLBACK TO SAVEPOINT author_metadata_refresh`); rollbackErr != nil {
			logAuthorSourceRefreshFailed(bookID, rollbackErr)
		}
		return AuthorMetadataRefreshUnavailable
	}
	if _, err = tx.Exec(`RELEASE SAVEPOINT author_metadata_refresh`); err != nil {
		logAuthorSourceRefreshFailed(bookID, err)
		return AuthorMetadataRefreshUnavailable
	}
	return ""
}

func logAuthorSourceRefreshFailed(bookID int64, err error) {
	LogAuthorMetadataEvent(AuthorMetadataEventWarn, &AuthorMetadataEvent{
		Name: AuthorMetadataEventSourceRefreshFailed, Stage: AuthorMetadataStageDualWrite, BookID: bookID,
		SQLState: AuthorMetadataSQLState(err),
	})
}
