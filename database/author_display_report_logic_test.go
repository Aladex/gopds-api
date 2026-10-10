package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The comparison report sorts each book into the categories of the read-path
// design (§4.1) by the words of its two author lists, and into the display
// decision the author line takes for it, with the IDs of a few books of each
// for a person to look at. It never carries a name.

func reportOf(books map[int64]*bookAuthorSources, order ...int64) *AuthorDisplayReport {
	r := newAuthorDisplayReport(0)
	for _, id := range order {
		r.add(id, books[id])
	}
	return r
}

func TestReportSortsBooksByTheWordsOfTheirTwoLists(t *testing.T) {
	books := map[int64]*bookAuthorSources{
		// The same words in another order.
		1: {legacy: []legacyAuthor{legacy(10, "Иванов Иван")}, credits: []creditName{selected("Иван Иванов")}},
		// A patronymic the legacy name dropped.
		2: {legacy: []legacyAuthor{legacy(11, "Петров Пётр")}, credits: []creditName{selected("Пётр Ильич Петров")}},
		// The catalog names more than the file: an added co-author.
		3: {legacy: []legacyAuthor{legacy(12, "Сидоров Олег"), legacy(13, "Новиков Ян")},
			credits: []creditName{selected("Олег Сидоров")}},
		// Each has words the other lacks: the authors replaced by hand.
		4: {legacy: []legacyAuthor{legacy(14, "Исправленный Автор")}, credits: []creditName{fromFile("Лев Толстой")}},
		// No credits.
		5: {legacy: []legacyAuthor{legacy(15, "Гоголь Николай")}},
		// Credits and no legacy author: a nickname the scan lost.
		6: {credits: []creditName{fromFile("Аноним")}},
		// Neither.
		7: {},
	}
	r := reportOf(books, 1, 2, 3, 4, 5, 6, 7)

	assert.Equal(t, 7, r.Books)
	want := map[string][]int64{
		ReportWordsSame:        {1},
		ReportWordsLayerExtra:  {2},
		ReportWordsLegacyExtra: {3},
		ReportWordsBothDiffer:  {4},
		ReportWordsNoLayer:     {5, 7},
		ReportWordsNoLegacy:    {6},
	}
	for category, ids := range want {
		require.Contains(t, r.Categories, category)
		assert.Equal(t, len(ids), r.Categories[category].Books, category)
		assert.Equal(t, ids, r.Categories[category].Samples, category)
	}
}

func TestReportCountsTheDisplayDecisionAndItsLinks(t *testing.T) {
	books := map[int64]*bookAuthorSources{
		1: {legacy: []legacyAuthor{legacy(10, "Иванов Иван")}, credits: []creditName{selected("Иван Иванов")}},
		// Shown from the file, one credit without a link while the book has
		// a legacy author: the case an edit that removed an author would make.
		2: {legacy: []legacyAuthor{legacy(11, "Петров Пётр")},
			credits: []creditName{selected("Пётр Петров"), fromFile("Мастер")}},
		// Shown from the file, without links, no legacy author to edit.
		3: {legacy: []legacyAuthor{legacy(12, "Автор неизвестен")}, credits: []creditName{fromFile("АНОНИМНЫЙ ПИСАТЕЛЬ")}},
		4: {legacy: []legacyAuthor{legacy(13, "Сидоров Олег"), legacy(14, "Новиков Ян")},
			credits: []creditName{selected("Олег Сидоров")}},
		5: {legacy: []legacyAuthor{legacy(15, "Гоголь Николай")}},
	}
	r := reportOf(books, 1, 2, 3, 4, 5)

	for category, ids := range map[string][]int64{
		ReportDisplayLayer:            {1, 2, 3},
		ReportDisplayLegacyNoCredits:  {5},
		ReportDisplayLegacyUnmatched:  {4},
		ReportLayerUnlinkedName:       {2, 3},
		ReportLayerUnlinkedWithLegacy: {2},
		ReportMultiAuthor:             {2},
		ReportAuthorCountDiffers:      {2, 4},
		ReportLegacyPlaceholder:       {3},
		ReportFileNamesTitleCased:     {3},
	} {
		require.Contains(t, r.Categories, category)
		assert.Equal(t, ids, r.Categories[category].Samples, category)
		assert.Equal(t, len(ids), r.Categories[category].Books, category)
	}
	// Links are counted over the credits shown: books 1, 2 and 3.
	assert.Equal(t, 2, r.CreditsLinked)
	assert.Equal(t, 2, r.CreditsUnlinked)
}

// A category keeps the IDs of its first books only; its count goes on.
func TestReportKeepsAFewSamplesPerCategory(t *testing.T) {
	r := newAuthorDisplayReport(0)
	for id := int64(1); id <= reportSamples+5; id++ {
		r.add(id, &bookAuthorSources{legacy: []legacyAuthor{legacy(id, "Иванов Иван")}})
	}
	got := r.Categories[ReportDisplayLegacyNoCredits]
	assert.Equal(t, reportSamples+5, got.Books)
	require.Len(t, got.Samples, reportSamples)
	assert.Equal(t, int64(1), got.Samples[0])
	assert.Equal(t, int64(reportSamples), got.Samples[reportSamples-1])
}

// Every category is in the report, empty or not, so a reader can tell a zero
// from a category that was never counted.
func TestReportNamesEveryCategory(t *testing.T) {
	r := newAuthorDisplayReport(0)
	assert.Len(t, r.Categories, len(ReportCategories))
	for _, c := range ReportCategories {
		require.Contains(t, r.Categories, c)
		assert.NotNil(t, r.Categories[c].Samples, "samples serialize as [], not null")
	}
}
