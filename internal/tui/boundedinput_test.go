package tui

import "testing"

// Direct unit coverage of the two shared bounded-input helpers, independent
// of any screen's own state — see boundedinput.go's doc comment for which
// screens/fields call each one and why.

func TestAppendDigitsWithinMax(t *testing.T) {
	cases := []struct {
		buf, in string
		max     int
		want    string
	}{
		{"", "1", 11, "1"},
		{"1", "2", 11, "1"},  // candidate 12 > 11, rejected
		{"", "11", 11, "11"}, // exactly the max
		{"", "12", 11, "1"},  // multi-rune event, only the valid prefix kept
		{"", "125", 11, "1"},
		{"", "abc", 11, "abc"}, // non-numeric passes through unconstrained
		{"1", "x", 11, "1x"},   // becomes non-numeric ("1x"), unconstrained
		{"", "0", 0, "0"},      // 0 is not > max(0)
		{"", "1", 0, ""},       // 1 > max(0), rejected
	}
	for _, c := range cases {
		if got := appendDigitsWithinMax(c.buf, []rune(c.in), c.max); got != c.want {
			t.Errorf("appendDigitsWithinMax(%q, %q, %d) = %q, want %q", c.buf, c.in, c.max, got, c.want)
		}
	}
}

func TestAppendHexWithinDigitLimit(t *testing.T) {
	cases := []struct {
		buf, in string
		max     int
		want    string
	}{
		{"", "AB", 4, "AB"},
		{"", "ab", 4, "AB"},         // lowercase input, uppercased like every existing hex editor
		{"AABB", "C", 4, "AABB"},    // already at the digit limit, rejected
		{"", "AABBCC", 4, "AABB"},   // multi-rune event, only the valid-length prefix kept
		{"AA", "BB C", 4, "AABB "},  // a typed space doesn't count toward the digit budget...
		{"AA", "BB CD", 4, "AABB "}, // ...but real digits after it are still rejected once the budget's full
		{"", "0x1F", 8, "0X1F"},     // "0x" isn't hex-digit, uppercased and passed through unconstrained
		{"", "", 4, ""},             // no input, no change
		{"AABB", "", 4, "AABB"},     // empty rune batch, no change
		{"", "AB", 0, ""},           // zero digit budget rejects everything
	}
	for _, c := range cases {
		if got := appendHexWithinDigitLimit(c.buf, []rune(c.in), c.max); got != c.want {
			t.Errorf("appendHexWithinDigitLimit(%q, %q, %d) = %q, want %q", c.buf, c.in, c.max, got, c.want)
		}
	}
}

func TestCountHexDigits(t *testing.T) {
	cases := []struct {
		s    string
		want int
	}{
		{"", 0},
		{"AABB", 4},
		{"AA BB", 4},
		{"0X1F", 3}, // 'X' is not a hex digit
		{"abcdef", 6},
	}
	for _, c := range cases {
		if got := countHexDigits(c.s); got != c.want {
			t.Errorf("countHexDigits(%q) = %d, want %d", c.s, got, c.want)
		}
	}
}
