package tui

import (
	"strconv"
	"unicode"
)

// This file is the TUI's one shared policy for keeping a text-entry buffer
// from ever representing a value bigger than a hard bound the packet/
// schema/CRC model already knows about — enforced while typing, not just
// reported as an error after Enter. See ARCHITECTURE.md "Bounded input" for
// the product-level invariant this implements.
//
// A rejected keystroke never mutates the buffer at all — the visible text
// always matches exactly what the user actually typed, never an
// after-the-fact clamp (silently rewriting "12" down to "11" would be less
// predictable than simply not inserting the "2"). Both helpers below
// process incoming runes one at a time even when several arrive in a
// single tea.KeyMsg — bracketed paste, which bubbletea enables by default,
// delivers a whole paste as one KeyMsg with every pasted rune in Runes —
// so a paste can't bypass the same limit interactive typing obeys.
//
// Every bounded editor in the app funnels through one of these two
// functions rather than re-deriving its own acceptance rule:
//   - appendDigitsWithinMax: Designer's field-size editor (max = remaining
//     packet capacity) and its custom-CRC Width field (max = 64, the CRC
//     engine's hard bit-width ceiling).
//   - appendHexWithinDigitLimit: TX Builder's per-field hex value editor
//     (maxDigits = 2*field.Size) and manual CRC override (maxDigits =
//     2*schema.CRCSize()), and Designer's custom-CRC
//     Polynomial/Init/XOR-Out fields (maxDigits = 2*ceil(width bits / 8)).
//
// Submit-time model validation (packet.Schema.Validate, checksum.Params.
// Validate, and each form's own submit function) is unchanged by any of
// this — these helpers only ever prevent an impossible intermediate
// keystroke; they are not a replacement for validating the final value.

// appendDigitsWithinMax appends as many of runes to buf as possible without
// ever letting buf parse (as a base-10 integer) to a value greater than
// max. Non-digit runes — or a rune that doesn't extend a string
// strconv.Atoi can parse — are appended unconditionally: this only ever
// rejects a digit that would make the buffer a valid number bigger than
// max, never touches malformed input, which submit-time validation still
// catches unaffected.
func appendDigitsWithinMax(buf string, runes []rune, max int) string {
	for _, r := range runes {
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
// 0-9/A-F count as a "digit" toward that limit — a typed separator (a
// space, or any other character the existing hex editors already tolerate,
// e.g. a stray "x" from "0x") never counts and is always appended
// unconstrained, matching the task's "count semantic hex digits, not
// formatting characters" rule and this app's existing tolerance for
// malformed input (submit-time parsing, e.g. cleanHexTUI + the exact-
// length check in txrx.go's submitEdit, is what actually rejects it).
func appendHexWithinDigitLimit(buf string, runes []rune, maxDigits int) string {
	digits := countHexDigits(buf)
	for _, r := range runes {
		r = unicode.ToUpper(r)
		if isHexDigit(r) {
			if digits >= maxDigits {
				continue
			}
			digits++
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
