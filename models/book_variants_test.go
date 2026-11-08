package models

import "testing"

func TestVariantsKind(t *testing.T) {
	cases := []struct {
		files, documents int
		kind             string
		count            int
	}{
		{1, 1, BookVariantsSingle, 1},
		{0, 0, BookVariantsSingle, 1},
		{4, 1, BookVariantsRevisions, 4}, // one document in four revisions: «4 редакции»
		{3, 3, BookVariantsVariants, 3},
		{5, 2, BookVariantsVariants, 2}, // two documents, one of them in revisions: two variants
	}
	for _, c := range cases {
		kind, count := VariantsKind(c.files, c.documents)
		if kind != c.kind || count != c.count {
			t.Errorf("VariantsKind(%d, %d) = %s, %d; want %s, %d", c.files, c.documents, kind, count, c.kind, c.count)
		}
	}
}
