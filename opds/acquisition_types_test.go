package opds

import (
	"strings"
	"testing"
	"time"

	"gopds-api/models"
	"gopds-api/opdsutils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An entry's acquisition link must announce the media type DownloadBook
// actually serves for that format: a reader that trusts the feed names and
// opens the file by it.
func TestAcquisitionLinksAnnounceTheServedType(t *testing.T) {
	book := &models.Book{ID: 7, Title: "Солярис", Format: "fb2", Lang: "ru", RegisterDate: time.Now().Add(-time.Hour)}
	item := opdsutils.CreateItem(book, nil, models.BookEntryDetail{}, false)

	seen := 0
	for _, link := range item.Link {
		rest, ok := strings.CutPrefix(link.Href, "/opds/get/")
		if !ok {
			continue
		}
		format, _, _ := strings.Cut(rest, "/")
		served, known := bookTypes[format]
		require.True(t, known, "link %s names a format DownloadBook does not serve", link.Href)
		assert.Equal(t, served, link.Type, "link %s", link.Href)
		seen++
	}
	assert.Equal(t, 3, seen, "fb2, epub and mobi links")
}
