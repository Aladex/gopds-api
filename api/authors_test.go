package api

import "testing"

// The search page draws its pager from this number, so an off-by-one is a page
// the reader can reach and find empty.
func TestPageCount(t *testing.T) {
	cases := []struct {
		name  string
		total int
		limit int
		want  int
	}{
		{"nothing found offers no pages", 0, 10, 0},
		{"a partial page is still a page", 1, 10, 1},
		{"a full page is one page", 10, 10, 1},
		{"one over spills into a second", 11, 10, 2},
		{"two full pages are two", 20, 10, 2},
		{"an unnamed limit falls back to the page size", 25, 0, 3},
		{"a negative limit falls back too", 25, -5, 3},
		{"a larger page holds more", 100, 100, 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pageCount(tc.total, tc.limit); got != tc.want {
				t.Errorf("pageCount(%d, %d) = %d, want %d",
					tc.total, tc.limit, got, tc.want)
			}
		})
	}
}
