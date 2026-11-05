package api

import (
	"archive/zip"
	"bytes"
	"io"
	"testing"
	"time"

	"gopds-api/internal/archivestore"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// paddedArchive is one entry followed by enough stored bytes to push it out of
// the tail a check loads, so that reading it takes a request of its own.
func paddedArchive(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	f, err := w.Create("0.fb2")
	require.NoError(t, err)
	_, err = io.WriteString(f, "<FictionBook/>")
	require.NoError(t, err)
	stored, err := w.CreateHeader(&zip.FileHeader{Name: "padding", Method: zip.Store})
	require.NoError(t, err)
	_, err = stored.Write(make([]byte, 2<<20))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return out.Bytes()
}

// A check that takes most of the budget leaves the mobi conversion's read only
// what is left. With a budget of its own that read would go through.
func TestMobiConversionSharesOneBudgetWithItsCheck(t *testing.T) {
	const delay = 150 * time.Millisecond
	entry := "0.fb2"
	server := bucketLibrary(t, archivestore.Config{ReadBudget: delay * 5 / 2}, &entry)
	server.Put("archives/"+bucketArchive, paddedArchive(t))
	server.SetDelay(delay)

	err := ConvertBookToMobi(1)
	assert.ErrorIs(t, err, archivestore.ErrUnavailable)
}
