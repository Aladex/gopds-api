package services

import (
	"archive/zip"
	"bytes"
	"crypto/md5" // #nosec G501 -- the catalog's book-content identity, not a security use
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"gopds-api/internal/authornorm"
	"gopds-api/internal/parser"
	"gopds-api/internal/scanfixture"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The live and the backfill extraction must produce the same extraction key
// for the same file, so both read these two values; changing either is a new
// extractor contract, not a tweak.
func TestAuthorMetadataExtractorConfigIsPinned(t *testing.T) {
	assert.Equal(t, "fb2-metadata-v1", AuthorMetadataExtractorVersion)
	assert.Equal(t, int64(4<<20), int64(AuthorMetadataMaxBytes))
}

func metadataFixtureBytes(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "internal", "parser", "testdata", "metadata", name))
	require.NoError(t, err)
	return b
}

func md5Hex(b []byte) string {
	// #nosec G401 -- the catalog's book-content identity
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

// Contract 3.3: the document classes an extraction can end in. Anything else
// is not a property of the book and must not be swallowed as one.
func TestClassifyAuthorMetadataExtractionError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want AuthorMetadataExtractionStatus
		ok   bool
	}{
		{"damaged content", fmt.Errorf("%w: no root element", parser.ErrDamagedContent), AuthorMetadataInvalidFB2, true},
		{"unsupported charset", fmt.Errorf("%w: utf-32", parser.ErrUnsupportedCharset), AuthorMetadataUnsupportedEncoding, true},
		{"undeclared charset", parser.ErrUndeclaredCharset, AuthorMetadataUnsupportedEncoding, true},
		{"unsupported declared charset", parser.ErrUnsupportedDeclaredCharset, AuthorMetadataUnsupportedEncoding, true},
		{"metadata limit", parser.ErrMetadataLimit, AuthorMetadataParseFailed, true},
		{"xml syntax after the root", &xml.SyntaxError{Msg: "unexpected EOF", Line: 3}, AuthorMetadataParseFailed, true},
		{"invalid metadata shape", authornorm.ErrInvalidSourceMetadata, AuthorMetadataParseFailed, true},
		{"invalid extractor input is a configuration error", parser.ErrInvalidExtractorInput, "", false},
		{"source read error", io.ErrUnexpectedEOF, "", false},
		{"unknown error", errors.New("boom"), "", false},
		{"nil", nil, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ClassifyAuthorMetadataExtractionError(c.err)
			assert.Equal(t, c.ok, ok)
			assert.Equal(t, c.want, got)
		})
	}
}

// The classes the real extractor actually produces for the phase-2 fixtures.
func TestPrepareAuthorSourceClassifiesRealExtractorFailures(t *testing.T) {
	w := NewAuthorMetadataSourceWriter()
	cases := []struct {
		fixture string
		want    AuthorMetadataExtractionStatus
	}{
		{"foreign_root.fb2", AuthorMetadataInvalidFB2},
		{"enc_unsupported_declared.fb2", AuthorMetadataUnsupportedEncoding},
		{"malformed.fb2", AuthorMetadataParseFailed},
	}
	for _, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			content := metadataFixtureBytes(t, c.fixture)
			prepared, err := w.Prepare(content, "a.zip", c.fixture, md5Hex(content))
			require.NoError(t, err)
			assert.False(t, prepared.Extracted())
			assert.Equal(t, c.want, prepared.Failure())
		})
	}
}

func TestPrepareAuthorSourceExtractsFromTheGivenBytes(t *testing.T) {
	content := metadataFixtureBytes(t, "multi_contributor.fb2")
	// The scan's own MD5 is the file identity on both sides; a value that is
	// not the hash of these bytes shows that it is passed through rather
	// than recomputed.
	scanMD5 := "0123456789abcdef0123456789abcdef"
	require.NotEqual(t, md5Hex(content), scanMD5)

	prepared, err := NewAuthorMetadataSourceWriter().Prepare(content, "lib/a.zip", "multi.fb2", scanMD5)
	require.NoError(t, err)

	require.True(t, prepared.Extracted())
	assert.Empty(t, prepared.Failure())
	assert.Equal(t, scanMD5, prepared.metadata.BookMD5, "the scan's MD5 is echoed, never recomputed")
	assert.Equal(t, "lib/a.zip", prepared.metadata.ArchivePath)
	assert.Equal(t, "multi.fb2", prepared.metadata.EntryName)
	assert.Equal(t, AuthorMetadataExtractorVersion, prepared.metadata.ExtractorVersion)
	assert.Len(t, prepared.metadata.Contributors, 4)
}

// The configured limit is the one the extractor applies.
func TestPrepareAuthorSourceAppliesTheByteLimit(t *testing.T) {
	content := metadataFixtureBytes(t, "multi_contributor.fb2")
	w := &AuthorMetadataSourceWriter{extractor: parser.MetadataExtractor{
		MetadataMaxBytes: 64, ExtractorVersion: AuthorMetadataExtractorVersion,
	}}

	prepared, err := w.Prepare(content, "a.zip", "b.fb2", md5Hex(content))
	require.NoError(t, err)

	assert.False(t, prepared.Extracted())
	assert.Equal(t, AuthorMetadataParseFailed, prepared.Failure())
}

// A misconfigured extractor is a systemic error: it surfaces instead of
// turning every book into a silently skipped extraction.
func TestPrepareAuthorSourceSurfacesConfigurationErrors(t *testing.T) {
	content := metadataFixtureBytes(t, "multi_contributor.fb2")
	w := &AuthorMetadataSourceWriter{extractor: parser.MetadataExtractor{MetadataMaxBytes: 1 << 20}}

	_, err := w.Prepare(content, "a.zip", "b.fb2", md5Hex(content))
	require.ErrorIs(t, err, parser.ErrInvalidExtractorInput)
}

func TestNewBookScanServiceInstallsTheAuthorSourceWriter(t *testing.T) {
	s := NewBookScanService(t.TempDir(), t.TempDir(), nil, false, nil)

	require.NotNil(t, s.authorSource)
	assert.Equal(t, AuthorMetadataExtractorVersion, s.authorSource.extractor.ExtractorVersion)
	assert.Equal(t, int64(AuthorMetadataMaxBytes), s.authorSource.extractor.MetadataMaxBytes)
}

// inMemoryEntry is one zip entry backed by memory.
func inMemoryEntry(t *testing.T, name string, content []byte) *zip.File {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	require.NoError(t, err)
	_, err = w.Write(content)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	require.NoError(t, err)
	return zr.File[0]
}

// A scanner without a source writer must refuse to ingest rather than write
// legacy rows alone: a missing dependency may never disable the dual write
// silently. The refusal comes before any database access — this test has no
// database at all.
func TestProcessBookRefusesWithoutAuthorSourceWriter(t *testing.T) {
	scanner := &BookScanService{}
	entry := inMemoryEntry(t, "b.fb2", metadataFixtureBytes(t, "multi_contributor.fb2"))

	id, err := scanner.ProcessBook(entry, "a.zip")

	require.ErrorIs(t, err, ErrAuthorSourceWriterMissing)
	assert.Zero(t, id)
}

// countRows counts one table's rows on the scratch database.
func countRows(t *testing.T, db *pg.DB, table string) int {
	t.Helper()
	var n int
	_, err := db.QueryOne(pg.Scan(&n), "SELECT count(*) FROM "+table)
	require.NoError(t, err)
	return n
}

func insertPlainBook(t *testing.T, db *pg.DB, bookMD5 string) int64 {
	t.Helper()
	var id int64
	_, err := db.QueryOne(pg.Scan(&id), `INSERT INTO opds_catalog_book
		(filename, path, format, registerdate, docdate, lang, title, annotation, md5)
		VALUES ('b.fb2', 'a.zip', 'fb2', now(), '', 'ru', 'writer fixture', '', ?) RETURNING id`, bookMD5)
	require.NoError(t, err)
	return id
}

// The prepared extraction is bound to the inserted book: the snapshot names
// that book, never the provisional ID the extraction ran with.
func TestPersistAuthorSourceBindsTheInsertedBook(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	content := metadataFixtureBytes(t, "multi_contributor.fb2")
	sum := md5Hex(content)
	w := NewAuthorMetadataSourceWriter()
	prepared, err := w.Prepare(content, "a.zip", "b.fb2", sum)
	require.NoError(t, err)
	bookID := insertPlainBook(t, db, sum)

	require.NoError(t, db.RunInTransaction(t.Context(), func(tx *pg.Tx) error {
		return w.Persist(tx, bookID, prepared)
	}))

	var stored struct {
		BookID int64
		Origin string
		RunID  *int64
	}
	_, err = db.QueryOne(&stored, `SELECT book_id, origin, run_id FROM book_metadata_snapshot`)
	require.NoError(t, err)
	assert.Equal(t, bookID, stored.BookID)
	assert.Equal(t, "live", stored.Origin)
	assert.Nil(t, stored.RunID)
	assert.NotEqual(t, provisionalSourceBookID, stored.BookID)
}

// Persisting the same extraction key again writes no second snapshot, credit
// or job (plan RED 9).
func TestPersistAuthorSourceSameKeyWritesNothingNew(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	content := metadataFixtureBytes(t, "multi_contributor.fb2")
	sum := md5Hex(content)
	w := NewAuthorMetadataSourceWriter()
	prepared, err := w.Prepare(content, "a.zip", "b.fb2", sum)
	require.NoError(t, err)
	bookID := insertPlainBook(t, db, sum)

	for range 2 {
		require.NoError(t, db.RunInTransaction(t.Context(), func(tx *pg.Tx) error {
			return w.Persist(tx, bookID, prepared)
		}))
	}

	assert.Equal(t, 1, countRows(t, db, "book_metadata_snapshot"))
	assert.Equal(t, 4, countRows(t, db, "book_contributor_credit"))
	assert.Equal(t, 3, countRows(t, db, "contributor_normalization_job"))
}

// A prepared extraction that failed writes nothing and is not an error: the
// legacy book still ingests (decision recorded in the phase report).
func TestPersistAuthorSourceSkipsAFailedExtraction(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	content := metadataFixtureBytes(t, "foreign_root.fb2")
	w := NewAuthorMetadataSourceWriter()
	prepared, err := w.Prepare(content, "a.zip", "b.fb2", md5Hex(content))
	require.NoError(t, err)
	require.False(t, prepared.Extracted())
	bookID := insertPlainBook(t, db, md5Hex(content))

	require.NoError(t, db.RunInTransaction(t.Context(), func(tx *pg.Tx) error {
		return w.Persist(tx, bookID, prepared)
	}))

	assert.Zero(t, countRows(t, db, "book_metadata_snapshot"))
}

// A missing prepared source (Prepare never ran) is not a skipped
// extraction: persisting it is a programming error.
func TestPersistAuthorSourceRejectsAnUnpreparedSource(t *testing.T) {
	err := NewAuthorMetadataSourceWriter().Persist(nil, 1, nil)
	require.ErrorIs(t, err, ErrAuthorSourceNotPrepared)
}
