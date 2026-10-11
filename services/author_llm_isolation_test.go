package services

import (
	"bytes"
	"errors"
	"testing"

	"gopds-api/database"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// One bad catalog value is that item's failure: a panic while building or
// judging it is caught, recorded with the item's identifier and a closed
// reason, and the rest of the batch or stage goes on.

func TestIsolateItemTurnsAPanicIntoTheItemsError(t *testing.T) {
	assert.NoError(t, isolateItem(func() error { return nil }))
	plain := errors.New("plain")
	assert.ErrorIs(t, isolateItem(func() error { return plain }), plain)
	err := isolateItem(func() error { panic("a value nobody expected") })
	assert.ErrorIs(t, err, errAuthorLLMItemPanic)
	assert.NotContains(t, err.Error(), "nobody", "the panic's value may carry a name; it is never kept")
}

func TestClassifyPopulationSkipsAPanickingRow(t *testing.T) {
	first, last := "Иван", "Петров"
	row := func(fp byte) database.AuthorLLMPopulationRow {
		return database.AuthorLLMPopulationRow{SourceFingerprint: bytes.Repeat([]byte{fp}, 32),
			DecisionClass: "initials", First: &first, Last: &last}
	}
	rows := []database.AuthorLLMPopulationRow{row(1), row(2), row(3)}
	poisoned := bytes.Repeat([]byte{2}, 32)
	previous := buildPopulationItem
	buildPopulationItem = func(r *database.AuthorLLMPopulationRow) (populationItem, string) {
		if bytes.Equal(r.SourceFingerprint, poisoned) {
			panic("poisoned row")
		}
		return previous(r)
	}
	t.Cleanup(func() { buildPopulationItem = previous })

	strata, skipped := classifyPopulation(rows)
	require.Len(t, skipped, 1)
	assert.Equal(t, evalSkipItemPanic, skipped[0].Reason)
	assert.Equal(t, poisoned, skipped[0].SourceFingerprint)
	n := 0
	for _, items := range strata {
		n += len(items)
	}
	assert.Equal(t, 2, n, "the rows around the poisoned one were lost")
}
