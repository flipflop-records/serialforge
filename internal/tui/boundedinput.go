package tui

import (
	"strconv"
	"unicode"
)

// This file is the TUI's one shared policy for keeping a text-entry buffer
// from ever representing a value bigger than a hard bound the packet/
// schema/CRC model already knows about, AND from ever containing a
// character that could never be part of a valid value in the first place
// (a decimal buffer can never usefully contain "U"; a hex buffer can never
// usefully contain "Z") — both enforced while typing, not just reported as
// an error after Enter. See ARCHITECTURE.md "Bounded input" for the
// product-level invariant this implements.
//
// A rejected keystroke never mutates the buffer at all — the visible text
// always matches exactly what the user actually typed, never an
// after-the-fact clamp (silently rewriting "12" down to "11" would be less
// predictable than simply not inserting the "2"), and never a silently
// dropped character re-typed as if it had been accepted. Every helper below
// processes incoming runes one at a time even when several arrive in a
// single tea.KeyMsg — bracketed paste, which bubbletea enables by default,
// delivers a whole paste as one KeyMsg with every pasted rune in Runes — so
// a paste is filtered exactly like interactive typing, rune by rune,
// silently dropping whichever pasted runes aren't valid for the field
// rather than rejecting the whole paste (matches how "too many digits" was
// already handled before this file's character-class fix: partial
// acceptance, not all-or-nothing).
//
// Every bounded/character-filtered editor in the app funnels through one of
// these functions rather than re-deriving its own acceptance rule:
//   - appendDecimalDigits: a decimal field with no natural upper bound —
//     Designer's packet-total-size field, and (via textForm's decimalOnly,
//     see savedpackets.go) Config's custom-baud field.
//   - appendDigitsWithinMax: same character class as appendDecimalDigits,
//     plus a value ceiling — Designer's field-size editor (max = remaining
//     packet capacity) and its custom-CRC Width field (max =
//     checksum.MaxWidth).
//   - appendHexWithinDigitLimit: TX Builder's per-field hex value editor
//     (maxDigits = 2*field.Size) and manual CRC override (maxDigits =
//     2*schema.CRCSize()), and Designer's custom-CRC
//     Polynomial/Init/XOR-Out fields (maxDigits = 2*ceil(width bits / 8)).
//
// Submit-time model validation (packet.Schema.Validate, checksum.Params.
// Validate, and each form's own submit function) is unchanged by any of
// this — these helpers only ever prevent an impossible intermediate
// keystroke; they are not a replacement for validating the final value.

// isDecimalDigit reports whether r is a plain ASCII decimal digit — the one
// character class every decimal editor in this app accepts. Deliberately
// narrower than unicode.IsDigit (which also accepts non-ASCII digit runes
// no numeric parser here would ever handle) and than "any letter is
// invalid" being conflated with "any non-digit is invalid" — this is
// exactly the class strconv.Atoi itself expects for a base-10 integer.
func isDecimalDigit(r rune) bool { return r >= '0' && r <= '9' }

// appendDecimalDigits appends only decimal-digit runes from runes to buf,
// silently dropping every other rune (letters like the reported "U"/"I"/
// "Z", punctuation, whitespace) — the plain character-class filter for a
// decimal field with no value ceiling to also enforce. See
// appendDigitsWithinMax for the version that adds one.
func appendDecimalDigits(buf string, runes []rune) string {
	for _, r := range runes {
		if isDecimalDigit(r) {
			buf += string(r)
		}
	}
	return buf
}

// appendDigitsWithinMax appends only decimal-digit runes from runes to buf
// (see appendDecimalDigits — every non-digit rune, e.g. a stray letter, is
// dropped, never appended), and additionally never lets buf come to parse
// (as a base-10 integer) to a value greater than max. A digit string too
// long for strconv.Atoi to parse (well beyond any realistic packet/CRC
// size) still passes through unconstrained by the max check specifically —
// only a rune that both parses AND exceeds max is rejected — matching this
// function's pre-existing overflow behavior; character-class filtering is
// the only new rejection this adds.
func appendDigitsWithinMax(buf string, runes []rune, max int) string {
	for _, r := range runes {
		if !isDecimalDigit(r) {
			continue
		}
		candidate := buf + string(r)
		if n, err := strconv.Atoi(candidate); err == nil && n > max {
			continue
		}
		buf = candidate
	}
	return buf
}

// appendHexWithinDigitLimit appends as many of runes to buf (uppercased,
// matching every existing hex editor's own convention) as possible without
// ever letting buf's semantic hex-digit count exceed maxDigits. Only
// 0-9/A-F count as a "digit" toward that limit. Two non-digit characters
// are still accepted unconstrained, matching cleanHexTUI's own existing,
// deliberate tolerance (txrx.go: strips " " and "0X" before parsing) so
// this fix never breaks input that already worked: a literal space (the
// "12 34"-style byte separator cleanHexTUI already strips) and 'x'/'X' (the
// "0x1234"-style prefix cleanHexTUI already strips — checked case-
// insensitively here since cleanHexTUI itself uppercases before stripping).
// Every OTHER non-hex-digit rune — the reported "U"/"I"/"Z"/"G", or any
// other letter/punctuation — is now dropped outright rather than appended:
// previously this function counted only hex digits toward the limit but
// appended literally any other rune unconstrained, which is the actual bug
// this task fixes. Submit-time parsing (cleanHexTUI + the exact-length
// check in txrx.go's submitEdit) remains the final authority on the fully
// assembled value; this only prevents an impossible intermediate keystroke.
func appendHexWithinDigitLimit(buf string, runes []rune, maxDigits int) string {
	digits := countHexDigits(buf)
	for _, r := range runes {
		r = unicode.ToUpper(r)
		switch {
		case isHexDigit(r):
			if digits >= maxDigits {
				continue
			}
			digits++
		case r == ' ' || r == 'X':
			// cleanHexTUI-tolerated formatting characters — see doc comment.
		default:
			continue
		}
		buf += string(r)
	}
	return buf
}

// isHexDigit is case-insensitive even though every current caller already
// uppercases before checking (appendHexWithinDigitLimit) or only ever
// stores already-uppercased buffers (countHexDigits' real call sites) — a
// defensive, cost-free choice so a future caller passing lowercase hex
// can't silently undercount.
func isHexDigit(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'A' && r <= 'F') || (r >= 'a' && r <= 'f')
}

func countHexDigits(s string) int {
	n := 0
	for _, r := range s {
		if isHexDigit(r) {
			n++
		}
	}
	return n
}
