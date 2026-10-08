package services

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/internal/parser"
	"gopds-api/internal/scanfixture"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const mappingTestExtractorVersion = "fb2-metadata-mapping-test-v1"

// extractMetadataFixture runs the real metadata extractor over one of its own
// FB2 fixtures, so the mapping is tested on exactly what the extractor hands
// to the backfill worker and to the live dual write.
func extractMetadataFixture(t *testing.T, name string, bookID int64) authornorm.SourceMetadata {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "internal", "parser", "testdata", "metadata", name))
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	md, err := parser.MetadataExtractor{MetadataMaxBytes: 1 << 20, ExtractorVersion: mappingTestExtractorVersion}.
		Extract(parser.ExtractBookInput{Reader: f, BookID: bookID, ArchivePath: "mapping.zip", EntryName: name})
	require.NoError(t, err)
	return md
}

func strp(s string) *string { return &s }

// mappedCredit is the observable part of one mapped credit.
type mappedCredit struct {
	Role       models.ContributorRole
	First      *string
	Middle     *string
	Last       *string
	Nickname   *string
	SourceID   *string
	Display    string
	Flags      []string
	Provenance string
}

func observeCredits(in *database.ExtractionInput) []mappedCredit {
	out := make([]mappedCredit, len(in.Credits))
	for i := range in.Credits {
		c := &in.Credits[i]
		out[i] = mappedCredit{
			Role: c.Role, First: c.Source.First(), Middle: c.Source.Middle(), Last: c.Source.Last(),
			Nickname: c.Source.Nickname(), SourceID: c.SourceID, Display: c.Source.DisplayName(),
			Flags: c.QualityFlags, Provenance: string(c.XMLProvenance),
		}
	}
	return out
}

func TestExtractionInputCarriesDuplicateComponentFlag(t *testing.T) {
	md := extractMetadataFixture(t, "duplicate_components.fb2", 7)
	require.True(t, md.Contributors[0].Value.HasDuplicateComponent(), "fixture precondition")

	in, err := ExtractionInputFromSourceMetadata(&md, models.BookMetadataSnapshotLive, nil)
	require.NoError(t, err)

	// The first value stays in the named field, the repeated one only in the
	// display; the flag is the only trace of the repetition the fingerprint
	// does not carry, so it must reach the credit.
	assert.Equal(t, []mappedCredit{{
		Role: models.ContributorRoleAuthor, First: strp("Пётр"), Last: strp("Дубль"),
		Display: "Пётр Петро Дубль", Flags: []string{"duplicate_component"},
		Provenance: `{"section":"title-info","element":"author","position":0}`,
	}}, observeCredits(in))
	require.Len(t, in.Credits, 1)
	assert.True(t, in.Credits[0].Source.HasDuplicateComponent())
}

func TestExtractionInputPreservesRolesOrderSourceIDsAndProvenance(t *testing.T) {
	md := extractMetadataFixture(t, "multi_contributor.fb2", 11)

	in, err := ExtractionInputFromSourceMetadata(&md, models.BookMetadataSnapshotLive, nil)
	require.NoError(t, err)

	// XML order; the whitespace-only author creates no credit and consumes no
	// position; the present-but-empty first name is a structural flag.
	assert.Equal(t, []mappedCredit{
		{Role: models.ContributorRoleAuthor, First: strp("Иван"), Last: strp("Петров"), Nickname: strp("ivanp"),
			SourceID: strp("au-1"), Display: "Иван Петров ivanp", Flags: []string{},
			Provenance: `{"section":"title-info","element":"author","position":0}`},
		{Role: models.ContributorRoleAuthor, First: strp("Анна"), Middle: strp("Сергеевна"), Last: strp("Сидорова"),
			Display: "Анна Сергеевна Сидорова", Flags: []string{},
			Provenance: `{"section":"title-info","element":"author","position":1}`},
		{Role: models.ContributorRoleAuthor, First: strp(""), Last: strp("Пустов"),
			Display: "Пустов", Flags: []string{"empty_component"},
			Provenance: `{"section":"title-info","element":"author","position":2}`},
		{Role: models.ContributorRoleTranslator, First: strp("John"), Last: strp("Smith"),
			Display: "John Smith", Flags: []string{},
			Provenance: `{"section":"title-info","element":"translator","position":0}`},
	}, observeCredits(in))

	assert.Equal(t, int64(11), in.BookID)
	assert.Equal(t, md.BookMD5, in.BookMD5)
	assert.Len(t, in.BookMD5, 32)
	assert.Equal(t, mappingTestExtractorVersion, in.ExtractorVersion)
	assert.Equal(t, authornorm.NormalizerVersion, in.NormalizerVersion)
	assert.Equal(t, models.BookMetadataSnapshotLive, in.Origin)
	assert.Nil(t, in.RunID)
	assert.Equal(t, models.BookMetadataSnapshotExtracted, in.Outcome)
	assert.Equal(t, "mapping.zip", in.ArchivePath)
	assert.Equal(t, "multi_contributor.fb2", in.EntryName)
	assert.Equal(t, strp("Метаданные тест"), in.SourceTitle)
	assert.Equal(t, strp("ru"), in.SourceLang)
	assert.Equal(t, strp("en"), in.SourceSrcLang)
	assert.Equal(t, strp("doc-multi"), in.SourceDocumentID)
	assert.Equal(t, strp("1"), in.SourceDocumentVersion)
	assert.Equal(t, []string{}, in.QualityFlags)
}

func TestExtractionInputNoAuthorKeepsTranslators(t *testing.T) {
	md := extractMetadataFixture(t, "translator_only.fb2", 13)
	require.Equal(t, authornorm.OutcomeNoAuthor, md.Outcome, "fixture precondition")

	in, err := ExtractionInputFromSourceMetadata(&md, models.BookMetadataSnapshotLive, nil)
	require.NoError(t, err)

	assert.Equal(t, models.BookMetadataSnapshotExtractedNoAuthor, in.Outcome)
	assert.Equal(t, []mappedCredit{{
		Role: models.ContributorRoleTranslator, First: strp("Ольга"), Last: strp("Переводская"),
		Display: "Ольга Переводская", Flags: []string{},
		Provenance: `{"section":"title-info","element":"translator","position":0}`,
	}}, observeCredits(in))
}

func TestExtractionInputSequencesAndPublishInfo(t *testing.T) {
	md := extractMetadataFixture(t, "sequences_publish.fb2", 17)
	require.Len(t, md.Year, 1, "fixture precondition")

	in, err := ExtractionInputFromSourceMetadata(&md, models.BookMetadataSnapshotLive, nil)
	require.NoError(t, err)

	// Order, nesting and the null number survive; publish-info sequences are
	// recorded with the others.
	assert.JSONEq(t, `[
		{"index":0,"parent":-1,"name":"Миры","number":"2"},
		{"index":1,"parent":0,"name":"Хроники","number":null},
		{"index":2,"parent":-1,"name":"Миры","number":"3"},
		{"index":3,"parent":-1,"name":"Серия издателя","number":null}]`, string(in.SourceSequences))
	assert.Equal(t, []string{"978-5-17-1", "5-17-02"}, in.SourceISBNs)
	assert.Equal(t, strp("АСТ"), in.SourcePublisher)
	assert.Equal(t, strp("Москва"), in.SourceCity)
	require.NotNil(t, in.SourceYear)
	assert.Equal(t, md.Year[0], *in.SourceYear)
	year, err := json.Marshal([]string{md.Year[0]})
	require.NoError(t, err)
	assert.JSONEq(t, `{"publish_info":{"publisher":["АСТ"],"city":["Москва"],"year":`+string(year)+`}}`,
		string(in.XMLProvenance))
}

// handBuilt is a minimal valid extraction with one author.
func handBuilt(t *testing.T) authornorm.SourceMetadata {
	t.Helper()
	value, err := authornorm.NewSourceValue([]authornorm.SourceComponent{
		{Kind: authornorm.ComponentFirst, Value: "Имя"}, {Kind: authornorm.ComponentLast, Value: "Фамилия"},
	})
	require.NoError(t, err)
	return authornorm.SourceMetadata{
		BookID: 19, ArchivePath: "a.zip", EntryName: "b.fb2", ExtractorVersion: mappingTestExtractorVersion,
		BookMD5:      "0123456789abcdef0123456789abcdef",
		Contributors: []authornorm.Contributor{{Role: authornorm.RoleAuthor, Position: 0, Value: value}},
		Outcome:      authornorm.OutcomeExtracted,
	}
}

// Repeated publication fields keep their first value in the scalar column and
// every value, in document order, in the snapshot provenance.
func TestExtractionInputRepeatedPublishInfoStaysLossless(t *testing.T) {
	now := time.Now()
	first, second := strconv.Itoa(now.Year()), strconv.Itoa(now.AddDate(-1, 0, 0).Year())
	md := handBuilt(t)
	md.Publisher = []string{"Первое", "Второе"}
	md.City = []string{""}
	md.Year = []string{first, second}

	in, err := ExtractionInputFromSourceMetadata(&md, models.BookMetadataSnapshotLive, nil)
	require.NoError(t, err)

	assert.Equal(t, strp("Первое"), in.SourcePublisher)
	assert.Equal(t, strp(""), in.SourceCity, "a present empty element stays distinct from an absent one")
	assert.Equal(t, strp(first), in.SourceYear)
	assert.JSONEq(t, `{"publish_info":{"publisher":["Первое","Второе"],"city":[""],"year":["`+first+`","`+second+`"]}}`,
		string(in.XMLProvenance))
}

// Absent lists are empty arrays, never null, and absent scalars stay nil.
func TestExtractionInputEmptyShapes(t *testing.T) {
	md := handBuilt(t)

	in, err := ExtractionInputFromSourceMetadata(&md, models.BookMetadataSnapshotLive, nil)
	require.NoError(t, err)

	assert.Equal(t, `[]`, string(in.SourceSequences))
	assert.Equal(t, []string{}, in.SourceISBNs)
	assert.JSONEq(t, `{"publish_info":{"publisher":[],"city":[],"year":[]}}`, string(in.XMLProvenance))
	assert.Nil(t, in.SourceTitle, "an absent title stays nil")
	assert.Nil(t, in.SourceLang)
	assert.Nil(t, in.SourceSrcLang)
	assert.Nil(t, in.SourcePublisher)
	assert.Nil(t, in.SourceCity)
	assert.Nil(t, in.SourceYear)
	assert.Nil(t, in.SourceDocumentID)
	assert.Nil(t, in.SourceDocumentVersion)
	require.Len(t, in.Credits, 1)
	assert.NotNil(t, in.Credits[0].QualityFlags)
	assert.Empty(t, in.Credits[0].QualityFlags)
}

func TestExtractionInputBackfillOriginKeepsRunID(t *testing.T) {
	md := handBuilt(t)
	run := int64(23)

	in, err := ExtractionInputFromSourceMetadata(&md, models.BookMetadataSnapshotBackfill, &run)
	require.NoError(t, err)

	assert.Equal(t, models.BookMetadataSnapshotBackfill, in.Origin)
	require.NotNil(t, in.RunID)
	assert.Equal(t, run, *in.RunID)
}

// Contract 3.2 for the title: absent stays nil, a present empty element stays a
// pointer to "", and any text — whitespace included — is kept exactly.
func TestExtractionInputTitlePresence(t *testing.T) {
	for _, title := range []*string{nil, strp(""), strp("   "), strp(" Название ")} {
		md := handBuilt(t)
		md.Title = title

		in, err := ExtractionInputFromSourceMetadata(&md, models.BookMetadataSnapshotLive, nil)
		require.NoError(t, err)

		assert.Equal(t, title, in.SourceTitle)
	}
}

// ownershipFixture is a full extraction: every pointer and list of the
// metadata is set, so a mapped value that aliases any of them shows up.
func ownershipFixture(t *testing.T) authornorm.SourceMetadata {
	t.Helper()
	md := extractMetadataFixture(t, "sequences_publish.fb2", 29)
	md.SrcLang = strp("en")
	md.Contributors[0].SourceID = strp("au-1")
	require.NotNil(t, md.Title)
	require.NotNil(t, md.Lang)
	require.NotNil(t, md.DocumentID)
	require.NotNil(t, md.DocumentVersion)
	require.NotNil(t, md.Sequences[0].Number)
	require.NotEmpty(t, md.ISBNs)
	require.NotEmpty(t, md.Publisher)
	require.NotEmpty(t, md.City)
	require.NotEmpty(t, md.Year)
	return md
}

// The mapping copies nothing the caller could later rewrite through the
// extraction value or the run ID: every scalar pointer, every list element,
// sequence numbers and contributor source IDs are owned by the input.
func TestExtractionInputDoesNotAliasTheExtraction(t *testing.T) {
	pristine := ownershipFixture(t)
	pristineRun := int64(31)
	want, err := ExtractionInputFromSourceMetadata(&pristine, models.BookMetadataSnapshotBackfill, &pristineRun)
	require.NoError(t, err)

	md := ownershipFixture(t)
	run := int64(31)
	in, err := ExtractionInputFromSourceMetadata(&md, models.BookMetadataSnapshotBackfill, &run)
	require.NoError(t, err)
	require.Equal(t, want, in, "precondition: the same extraction maps to the same input")

	for _, p := range []*string{md.Title, md.Lang, md.SrcLang, md.DocumentID, md.DocumentVersion,
		md.Sequences[0].Number, md.Contributors[0].SourceID} {
		*p = "changed"
	}
	for _, list := range [][]string{md.ISBNs, md.Publisher, md.City, md.Year} {
		list[0] = "changed"
	}
	run = 99

	assert.Equal(t, want, in)
}

func TestExtractionInputRejectsInvalidMetadata(t *testing.T) {
	_, err := ExtractionInputFromSourceMetadata(nil, models.BookMetadataSnapshotLive, nil)
	require.ErrorIs(t, err, authornorm.ErrInvalidSourceMetadata)

	gap := handBuilt(t)
	gap.Contributors[0].Position = 1
	_, err = ExtractionInputFromSourceMetadata(&gap, models.BookMetadataSnapshotLive, nil)
	require.ErrorIs(t, err, authornorm.ErrInvalidSourceMetadata)

	contradiction := handBuilt(t)
	contradiction.Outcome = authornorm.OutcomeNoAuthor
	_, err = ExtractionInputFromSourceMetadata(&contradiction, models.BookMetadataSnapshotLive, nil)
	require.ErrorIs(t, err, authornorm.ErrInvalidSourceMetadata)
}

// persistedCredit is one stored credit row as the phase-10 worker reads it.
type persistedCredit struct {
	Role         models.ContributorRole
	Position     int
	First        *string
	Middle       *string
	Last         *string
	Nickname     *string
	SourceID     *string
	Display      string
	QualityFlags []string `pg:",array"`
	Provenance   string
}

// End to end on a scratch database: the real extractor, the mapping and the
// phase-6 repository inside one transaction leave credit rows that carry the
// structural flags, positions, roles and provenance of the source elements.
func TestExtractionMappingPersistsCreditsEndToEnd(t *testing.T) {
	db := scanfixture.ScratchDB(t)

	cases := []struct {
		fixture string
		outcome models.BookMetadataSnapshotOutcome
		jobs    int
		credits []persistedCredit
	}{
		{"duplicate_components.fb2", models.BookMetadataSnapshotExtracted, 1, []persistedCredit{
			{Role: models.ContributorRoleAuthor, Position: 0, First: strp("Пётр"), Last: strp("Дубль"),
				Display: "Пётр Петро Дубль", QualityFlags: []string{"duplicate_component"},
				Provenance: `{"section":"title-info","element":"author","position":0}`},
		}},
		{"multi_contributor.fb2", models.BookMetadataSnapshotExtracted, 3, []persistedCredit{
			{Role: models.ContributorRoleAuthor, Position: 0, First: strp("Иван"), Last: strp("Петров"),
				Nickname: strp("ivanp"), SourceID: strp("au-1"), Display: "Иван Петров ivanp", QualityFlags: []string{},
				Provenance: `{"section":"title-info","element":"author","position":0}`},
			{Role: models.ContributorRoleAuthor, Position: 1, First: strp("Анна"), Middle: strp("Сергеевна"),
				Last: strp("Сидорова"), Display: "Анна Сергеевна Сидорова", QualityFlags: []string{},
				Provenance: `{"section":"title-info","element":"author","position":1}`},
			{Role: models.ContributorRoleAuthor, Position: 2, First: strp(""), Last: strp("Пустов"),
				Display: "Пустов", QualityFlags: []string{"empty_component"},
				Provenance: `{"section":"title-info","element":"author","position":2}`},
			{Role: models.ContributorRoleTranslator, Position: 0, First: strp("John"), Last: strp("Smith"),
				Display: "John Smith", QualityFlags: []string{},
				Provenance: `{"section":"title-info","element":"translator","position":0}`},
		}},
		{"translator_only.fb2", models.BookMetadataSnapshotExtractedNoAuthor, 0, []persistedCredit{
			{Role: models.ContributorRoleTranslator, Position: 0, First: strp("Ольга"), Last: strp("Переводская"),
				Display: "Ольга Переводская", QualityFlags: []string{},
				Provenance: `{"section":"title-info","element":"translator","position":0}`},
		}},
	}
	for i, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			bookID := int64(i + 1)
			_, err := db.Exec(`INSERT INTO opds_catalog_book
				(id, filename, path, format, registerdate, docdate, lang, title, annotation, md5)
				VALUES (?, ?, 'mapping.zip', 'fb2', now(), '', 'ru', 'mapping fixture', '', '')`, bookID, c.fixture)
			require.NoError(t, err)

			md := extractMetadataFixture(t, c.fixture, bookID)
			in, err := ExtractionInputFromSourceMetadata(&md, models.BookMetadataSnapshotLive, nil)
			require.NoError(t, err)
			var result database.PersistExtractionResult
			require.NoError(t, db.RunInTransaction(t.Context(), func(tx *pg.Tx) error {
				var persistErr error
				result, persistErr = database.PersistExtraction(tx, in)
				return persistErr
			}))
			assert.Equal(t, database.PersistNewSnapshot, result.Outcome)
			assert.Equal(t, len(c.credits), result.CreditsWritten)
			assert.Equal(t, c.jobs, result.JobsEnqueued)

			var snapshot models.BookMetadataSnapshot
			require.NoError(t, db.Model(&snapshot).Where("id = ?", result.SnapshotID).Select())
			assert.Equal(t, c.outcome, snapshot.Outcome)
			assert.Equal(t, md.BookMD5, snapshot.BookMD5)
			assert.True(t, snapshot.IsCurrent)

			var got []persistedCredit
			_, err = db.Query(&got, `SELECT role, position, source_first_name AS first, source_middle_name AS middle,
					source_last_name AS last, source_nickname AS nickname, source_id, source_display_name AS display,
					quality_flags, xml_provenance::text AS provenance
				FROM book_contributor_credit WHERE snapshot_id = ? ORDER BY role, position`, result.SnapshotID)
			require.NoError(t, err)
			require.Len(t, got, len(c.credits))
			for j := range got {
				// jsonb renders with its own spacing; compare the values.
				assert.JSONEq(t, c.credits[j].Provenance, got[j].Provenance)
				got[j].Provenance = c.credits[j].Provenance
			}
			assert.Equal(t, c.credits, got)
		})
	}
}

// presenceFB2 is a minimal FB2 with one author whose scalar source fields are
// all rendered by one element form: "" leaves them out, otherwise form turns
// an element name into its markup.
func presenceFB2(form func(element string) string) []byte {
	el := func(name string) string {
		if form == nil {
			return ""
		}
		return form(name)
	}
	return []byte(`<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
<description>
<title-info>
<author><first-name>Имя</first-name></author>
` + el("book-title") + el("lang") + el("src-lang") + `
</title-info>
<document-info>
` + el("id") + el("version") + `
</document-info>
<publish-info>
` + el("publisher") + el("city") + el("year") + el("isbn") + `
</publish-info>
</description>
<body><section><p>body text</p></section></body>
</FictionBook>
`)
}

// storedScalars are the snapshot's nullable source columns as stored.
type storedScalars struct {
	Title, Lang, SrcLang, DocumentID, DocumentVersion, Publisher, City, Year *string
	ISBNs                                                                    []string
}

func scalarsOf(s *models.BookMetadataSnapshot) storedScalars {
	return storedScalars{
		Title: s.SourceTitle, Lang: s.SourceLang, SrcLang: s.SourceSrcLang,
		DocumentID: s.SourceDocumentID, DocumentVersion: s.SourceDocumentVersion,
		Publisher: s.SourcePublisher, City: s.SourceCity, Year: s.SourceYear, ISBNs: s.SourceISBNs,
	}
}

// End to end, contract 3.2: an absent element is stored as NULL and a present
// empty one as a non-NULL "" in every nullable source column of the snapshot.
func TestExtractionMappingPersistsScalarPresenceEndToEnd(t *testing.T) {
	db := scanfixture.ScratchDB(t)

	empty := func() *string { return strp("") }
	cases := []struct {
		name string
		form func(element string) string
		want storedScalars
	}{
		{"absent", nil, storedScalars{ISBNs: []string{}}},
		{"self-closing", func(e string) string { return "<" + e + "/>" }, storedScalars{
			Title: empty(), Lang: empty(), SrcLang: empty(), DocumentID: empty(), DocumentVersion: empty(),
			Publisher: empty(), City: empty(), Year: empty(), ISBNs: []string{""},
		}},
		{"text", func(e string) string { return "<" + e + "> " + e + " </" + e + ">" }, storedScalars{
			Title: strp(" book-title "), Lang: strp(" lang "), SrcLang: strp(" src-lang "),
			DocumentID: strp(" id "), DocumentVersion: strp(" version "),
			Publisher: strp(" publisher "), City: strp(" city "), Year: strp(" year "), ISBNs: []string{" isbn "},
		}},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bookID := int64(i + 1)
			_, err := db.Exec(`INSERT INTO opds_catalog_book
				(id, filename, path, format, registerdate, docdate, lang, title, annotation, md5)
				VALUES (?, ?, 'presence.zip', 'fb2', now(), '', 'ru', 'presence fixture', '', '')`, bookID, c.name+".fb2")
			require.NoError(t, err)

			md, err := parser.MetadataExtractor{MetadataMaxBytes: 1 << 20, ExtractorVersion: mappingTestExtractorVersion}.
				Extract(parser.ExtractBookInput{Reader: bytes.NewReader(presenceFB2(c.form)), BookID: bookID,
					ArchivePath: "presence.zip", EntryName: c.name + ".fb2"})
			require.NoError(t, err)
			in, err := ExtractionInputFromSourceMetadata(&md, models.BookMetadataSnapshotLive, nil)
			require.NoError(t, err)
			var result database.PersistExtractionResult
			require.NoError(t, db.RunInTransaction(t.Context(), func(tx *pg.Tx) error {
				var persistErr error
				result, persistErr = database.PersistExtraction(tx, in)
				return persistErr
			}))
			require.Equal(t, database.PersistNewSnapshot, result.Outcome)

			var snapshot models.BookMetadataSnapshot
			require.NoError(t, db.Model(&snapshot).Where("id = ?", result.SnapshotID).Select())
			assert.Equal(t, c.want, scalarsOf(&snapshot))

			// The same presence, read with SQL NULL semantics rather than
			// through the ORM's pointer scan.
			var nulls []bool
			_, err = db.QueryOne(pg.Scan(pg.Array(&nulls)), `SELECT ARRAY[
					source_title IS NULL, source_lang IS NULL, source_src_lang IS NULL,
					source_document_id IS NULL, source_document_version IS NULL,
					source_publisher IS NULL, source_city IS NULL, source_year IS NULL]
				FROM book_metadata_snapshot WHERE id = ?`, result.SnapshotID)
			require.NoError(t, err)
			wantNull := c.want.Title == nil
			for j, isNull := range nulls {
				assert.Equal(t, wantNull, isNull, "column %d", j)
			}
		})
	}
}
