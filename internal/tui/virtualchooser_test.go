package tui

import "testing"

func TestTruncatePathKeepingTail(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"/tmp/serialforge-a", 40, "/tmp/serialforge-a"}, // well under max: unchanged
		{"short", 5, "short"},                            // exactly at max: unchanged
		{"/var/folders/fd/deep/nested/path/serialforge-a", 20, "…/path/serialforge-a"},
	}
	for _, c := range cases {
		got := truncatePathKeepingTail(c.in, c.max)
		if got != c.want {
			t.Errorf("truncatePathKeepingTail(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
		if len([]rune(got)) > c.max {
			t.Errorf("truncatePathKeepingTail(%q, %d) = %q (%d runes), exceeds max", c.in, c.max, got, len([]rune(got)))
		}
	}
}
