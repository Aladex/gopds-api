package database

import (
	"slices"
	"testing"
	"time"
)

func TestVariantInputFromRows(t *testing.T) {
	added := time.Now().Add(-24 * time.Hour)
	row := variantBookRow{
		ID: 7, Lang: "ru", Title: "Сага [СИ]", RegisterDate: added, DocDate: added.Format("2006-01-02"),
		DocumentID: "DOC", DocVersion: "1.1", Cover: true, Publisher: true,
		EntityIDs:   []string{"b", "a", "b"},
		EntityNames: []string{"Имя"},
		Series:      []string{"11:0", "7:3", "garbage", "9:x"},
	}
	in := row.input()
	if !slices.Equal(in.EntityIDs, []string{"a", "b"}) {
		t.Errorf("entity IDs %v, want [a b]: distinct and sorted", in.EntityIDs)
	}
	if len(in.Series) != 1 || in.Series[7] != 3 {
		t.Errorf("series %v, want only 7:3: unnumbered and unreadable entries drop", in.Series)
	}
	if in.BookID != 7 || in.DocumentID != "DOC" || in.DocVersion != "1.1" || !in.Cover || !in.Publisher ||
		in.Annotation || in.Year || !in.RegisterDate.Equal(added) || in.Unlinked {
		t.Errorf("input %+v does not carry the row", in)
	}
	row.Unlinked = true
	if in := row.input(); !in.Unlinked {
		t.Error("an unlinked row reads as linked")
	}
}
