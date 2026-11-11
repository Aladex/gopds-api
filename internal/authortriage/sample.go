package authortriage

import (
	"cmp"
	"slices"
)

// AllocateSample splits size over the cells (class → items) in proportion,
// by largest remainder: a tie goes to the cell with fewer rows so far, then
// to the smaller key. Every non-empty cell gets at least one row when size
// allows it, taken from the largest allocation. No cell gets more than it
// holds, the sum is exactly min(size, Σ cells), and empty cells are absent.
func AllocateSample(cells map[string]int, size int) map[string]int {
	keys := make([]string, 0, len(cells))
	total := 0
	for k, n := range cells {
		if n > 0 {
			keys = append(keys, k)
			total += n
		}
	}
	slices.Sort(keys)
	want := min(size, total)
	out := map[string]int{}
	if want <= 0 {
		return out
	}

	rem := map[string]int{}
	given := 0
	for _, k := range keys {
		out[k] = want * cells[k] / total
		rem[k] = want * cells[k] % total
		given += out[k]
	}
	order := slices.Clone(keys)
	slices.SortStableFunc(order, func(x, y string) int {
		return cmp.Or(cmp.Compare(rem[y], rem[x]), cmp.Compare(out[x], out[y]))
	})
	for i := 0; given < want; i++ {
		out[order[i]]++
		given++
	}

	if want >= len(keys) {
		for _, k := range keys {
			if out[k] > 0 {
				continue
			}
			largest := ""
			for _, l := range keys {
				if largest == "" || out[l] > out[largest] {
					largest = l
				}
			}
			out[largest]--
			out[k]++
		}
	}
	for k, n := range out {
		if n == 0 {
			delete(out, k)
		}
	}
	return out
}
