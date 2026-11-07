package curator

import "testing"

func TestNormalizeDetector(t *testing.T) {
	cases := []struct{ index, count, want int }{
		{-1, 0, -1},
		{-1, 3, 0},
		{0, 3, 0},
		{2, 3, 2},
		{3, 3, 0},
		{7, 3, 1},
		{-5, 2, 0},
	}
	for _, c := range cases {
		if got := normalizeDetector(c.index, c.count); got != c.want {
			t.Errorf("normalizeDetector(%d, %d) = %d, want %d", c.index, c.count, got, c.want)
		}
	}
}
