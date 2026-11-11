package authortriage

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func sum(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func TestAllocateSampleIsProportionalAndExact(t *testing.T) {
	assert.Equal(t, map[string]int{"a": 45, "b": 4, "c": 1},
		AllocateSample(map[string]int{"a": 900, "b": 90, "c": 10}, 50))
	assert.Equal(t, map[string]int{"a": 100, "b": 100},
		AllocateSample(map[string]int{"a": 100, "b": 100}, 300), "never more than a cell holds")
	assert.Equal(t, map[string]int{"a": 225, "b": 75},
		AllocateSample(map[string]int{"a": 300, "b": 100}, 300))
	assert.Equal(t, map[string]int{"a": 45, "b": 15},
		AllocateSample(map[string]int{"a": 75, "b": 25}, 60))

	t.Run("every non-empty cell gets one when the size allows", func(t *testing.T) {
		got := AllocateSample(map[string]int{"a": 10000, "b": 1, "c": 1, "d": 1, "e": 0}, 5)
		assert.Equal(t, map[string]int{"a": 2, "b": 1, "c": 1, "d": 1}, got)
	})

	t.Run("the sum is the size or everything", func(t *testing.T) {
		for _, c := range []struct {
			cells map[string]int
			size  int
		}{
			{map[string]int{"a": 7, "b": 13, "c": 29, "d": 51}, 10},
			{map[string]int{"a": 7, "b": 13, "c": 29, "d": 51}, 99},
			{map[string]int{"a": 7, "b": 13, "c": 29, "d": 51}, 100},
			{map[string]int{"a": 7, "b": 13, "c": 29, "d": 51}, 1000},
			{map[string]int{"a": 1, "b": 1, "c": 1}, 2},
			{map[string]int{"a": 3, "b": 3, "c": 3}, 4},
		} {
			got := AllocateSample(c.cells, c.size)
			total := sum(c.cells)
			want := min(c.size, total)
			assert.Equal(t, want, sum(got), "%v / %d", c.cells, c.size)
			for k, v := range got {
				assert.LessOrEqual(t, v, c.cells[k])
				assert.Positive(t, v, "no empty entries")
			}
			// Deterministic.
			assert.Equal(t, got, AllocateSample(c.cells, c.size))
		}
	})

	t.Run("nothing to take", func(t *testing.T) {
		assert.Empty(t, AllocateSample(nil, 50))
		assert.Empty(t, AllocateSample(map[string]int{"a": 0}, 50))
		assert.Empty(t, AllocateSample(map[string]int{"a": 5}, 0))
	})
}
