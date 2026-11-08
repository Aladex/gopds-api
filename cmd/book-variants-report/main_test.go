package main

import (
	"testing"
	"time"

	"gopds-api/internal/bookvariants"
)

func TestTallyReport(t *testing.T) {
	now := time.Now()
	in := func(id int64, title string, series map[int64]int64, names ...string) bookvariants.Input {
		if len(names) == 0 {
			names = []string{"Джон Толкин"}
		}
		return bookvariants.Input{
			BookID: id, Lang: "ru", Title: title, EntityIDs: []string{"e1"}, EntityNames: names,
			Series: series, RegisterDate: now,
		}
	}
	tl := newTally()
	books := []bookvariants.Input{
		in(1, "Сага", nil),
		in(2, "Сага [litres]", nil), //nolint:misspell // a catalog tag
		in(3, "Сага [СИ]", nil),
		in(4, "Дюна", map[int64]int64{7: 1}), in(5, "Дюна", map[int64]int64{7: 2}), in(6, "Дюна", nil),
		in(7, "Пьеса [проза]", nil),
		in(8, "unknown", nil),
		in(9, "Сказки", nil, "Unknown"),
	}
	for i := range books {
		tl.add(&books[i])
	}
	r := tl.report()
	if r.Books != 9 || r.Cards != 7 || r.OwnCards != 2 || r.VariantCards != 1 || r.BooksInVariants != 3 {
		t.Errorf("report %+v", r)
	}
	if r.SeriesSplitBases != 1 || r.SeriesSplitBooks != 3 || r.UnnumberedLoners != 1 {
		t.Errorf("series split %d/%d, loners %d; want 1/3, 1", r.SeriesSplitBases, r.SeriesSplitBooks, r.UnnumberedLoners)
	}
	if r.OwnCardReasons["junk title"] != 1 || r.OwnCardReasons["placeholder author"] != 1 {
		t.Errorf("own card reasons %v", r.OwnCardReasons)
	}
	if r.KeptTrailingBrack != 1 || r.Sizes["1"] != 6 || r.Sizes["3"] != 1 || r.MaxSize != 3 {
		t.Errorf("kept %d sizes %v max %d", r.KeptTrailingBrack, r.Sizes, r.MaxSize)
	}
}
