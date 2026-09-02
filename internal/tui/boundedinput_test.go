package tui

import "testing"

// Direct unit coverage of the shared bounded/character-filtered input
// helpers, independent of any screen's own state — see boundedinput.go's
// doc comment for which screens/fields call each one and why.

func TestAppendDecimalDigits(t *testing.T) {
	cases := []struct {
		buf, in string
		want    string
	}{
		{"", "1", "1"},
		{"", "9", "9"},
		{"12", "34", "1234"},
		{"", "U", ""},        // rejected: not a digit
		{"", "I", ""},        // rejected: not a digit
		{"", "A", ""},        // rejected: not a digit (decimal, not hex)
		{"12", "U", "12"},    // buffer unchanged by a rejected keystroke
		{"", "12U4", "124"},  // mixed paste: valid runes accepted, invalid ones dropped
		{"", "AAZZ55", "55"}, // mixed paste: only the decimal digits survive
		{"", "", ""},
		{"12", "", "12"},
	}
	for _, c := range cases {
		if got := appendDecimalDigits(c.buf, []rune(c.in)); got != c.want {
			t.Errorf("appendDecimalDigits(%q, %q) = %q, want %q", c.buf, c.in, got, c.want)
		}
	}
}

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
		{"", "0", 0, "0"}, // 0 is not > max(0)
		{"", "1", 0, ""},  // 1 > max(0), rejected

		// Character-class rejection — the actual bug this task fixes:
		// letters must never reach a decimal buffer, regardless of the
		// max bound.
		{"", "U", 11, ""},
		{"", "I", 11, ""},
		{"", "A", 11, ""},
		{"1", "U", 11, "1"},   // buffer unchanged by a rejected keystroke
		{"1", "x", 11, "1"},   // "1x" would have been a real, non-numeric buggy result before this fix
		{"", "abc", 11, ""},   // pure garbage paste: nothing survives
		{"", "12U4", 11, "1"}, // mixed paste: digits accepted up to the max, "U" dropped ("124" > 11)
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
		{"", "aBcD", 4, "ABCD"},     // mixed-case input
		{"AABB", "C", 4, "AABB"},    // already at the digit limit, rejected
		{"", "AABBCC", 4, "AABB"},   // multi-rune event, only the valid-length prefix kept
		{"AA", "BB C", 4, "AABB "},  // a typed space doesn't count toward the digit budget...
		{"AA", "BB CD", 4, "AABB "}, // ...but real digits after it are still rejected once the budget's full
		{"", "0x1F", 8, "0X1F"},     // "0x" prefix: 'x' isn't a hex digit but cleanHexTUI strips it at parse time, so it's still accepted here
		{"", "", 4, ""},             // no input, no change
		{"AABB", "", 4, "AABB"},     // empty rune batch, no change
		{"", "AB", 0, ""},           // zero digit budget rejects everything

		// Character-class rejection — the actual bug this task fixes:
		// letters outside A-F must never reach a hex buffer.
		{"", "G", 4, ""},
		{"", "I", 4, ""},
		{"", "U", 4, ""},
		{"", "Z", 4, ""},
		{"AB", "U", 4, "AB"},      // buffer unchanged by a rejected keystroke
		{"", "DEADI", 10, "DEAD"}, // mixed paste: valid hex accepted, "I" dropped
		{"", "12U4", 10, "124"},   // mixed paste: "U" dropped, remaining hex digits kept
		{"", "AAZZ55", 10, "AA55"},
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
