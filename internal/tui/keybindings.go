package tui

import (
	"fmt"

	"github.com/vtemnyakov/serialforge/internal/savedpacket"
)

// This file is the TUI's one centralized keybinding policy for Saved Packet
// hotkeys (product requirement: "do not scatter reserved-key lists
// throughout TUI files — one centralized keybinding policy should be the
// source of truth").
//
// Design note — allowlist, not a denylist snapshot: an earlier version of
// this design reserved "every key currently used somewhere" and allowed a
// hotkey to be anything left over. That is fragile: a future change adding
// a new core/screen keybinding from that same "currently free" pool would
// create a silent collision with nothing forcing it to be checked against
// this file. Instead, hotkeyPalette is a small, deliberately-curated
// ALLOWLIST — the only keys a Saved Packet hotkey may ever be assigned
// from — chosen to be disjoint from every key already meaningful anywhere
// in this package's Navigation-mode dispatch (grepped from every `case
// msg.String()` across model.go/monitor.go/packets.go/designer.go/txrx.go/
// devices.go/virtualchooser.go/batchview.go/configview.go):
//
//	q ctrl+c tab shift+tab 1 2 3 4 5 6 [ ]
//	up down left right enter esc delete backspace
//	a c d f g h j k m n o p r s t u x G H L N < >
//
// hotkeyPalette is a PERMANENT, load-bearing invariant, not a snapshot: no
// future core or per-screen keybinding may ever be added from this set —
// add new app shortcuts from outside the palette instead, and grep this
// comment before doing so. TestPaletteKeysAreNeverConsumedByCoreDispatch
// (keybindings_test.go) enforces this mechanically: it drives every
// screen's Navigation-mode update function with every palette key and
// asserts none of them produce a state change, so a future accidental
// collision fails a test instead of shipping silently. This is what makes
// future application shortcuts automatically unavailable for Saved Packet
// assignment, rather than depending on a hand-maintained list staying in
// sync with the rest of the codebase.
var hotkeyPalette = []string{
	// Punctuation — deliberately the bulk of the palette; matches the
	// product spec's own example hotkeys (', ., ,, /).
	"'", ".", ",", ";", "/", "-", "=", "`", "\\",
	// Letters never bound to a core/screen action anywhere in this package.
	// "f" is deliberately NOT here — see reservedKeyLabels: Monitor's own
	// Traffic/Saved Packets pane-focus toggle. "g"/"G" are likewise NOT
	// here — Logs' own top/bottom jump.
	"b", "e", "i", "l", "v", "w", "y", "z",
	"B", "C", "D", "E", "F", "I", "J", "K", "M", "O", "P", "Q", "R", "S", "T", "U", "V", "W", "X", "Y", "Z",
	// Digits — 1-6 are the tab-switch shortcuts; the rest are free.
	"0", "7", "8", "9",
}

// hotkeyPaletteSet is hotkeyPalette as a set, built once.
var hotkeyPaletteSet = func() map[string]bool {
	m := make(map[string]bool, len(hotkeyPalette))
	for _, k := range hotkeyPalette {
		m[k] = true
	}
	return m
}()

// reservedKeyLabels documents *why* a key outside the palette is reserved —
// shown in the "Hotkey 'q' is reserved for Quit" style error the product
// spec asks for. This is documentation for the rejection message, not a
// second source of truth for what's reserved: rejection itself is decided
// solely by hotkeyPaletteSet (anything not IN the palette is rejected,
// labeled or not) — see ValidateHotkeyAssignment.
var reservedKeyLabels = map[string]string{
	"q": "Quit", "ctrl+c": "Quit",
	"tab": "Next tab", "shift+tab": "Previous tab",
	"1": "Monitor tab", "2": "Packets tab", "3": "Devices tab", "4": "Batch tab", "5": "Logs tab", "6": "Config tab",
	"[": "Previous Packets subview", "]": "Next Packets subview",
	"up": "navigate", "down": "navigate", "left": "navigate", "right": "navigate",
	"enter": "select/open/edit", "esc": "cancel", "delete": "delete", "backspace": "delete",
	"a": "Devices: add profile",
	"c": "context action (pause / CRC override / clear)",
	"d": "duplicate",
	"f": "Monitor: switch Traffic/Saved Packets focus",
	"g": "Logs: jump to top", "G": "Logs: jump to bottom",
	"h": "Saved Packets: assign hotkey",
	"j": "navigate down", "k": "navigate up",
	"m": "Devices: manual connect / Monitor: cycle display mode",
	"n": "new field / new protocol draft",
	"o": "open protocol",
	"p": "Monitor: pause/resume",
	"r": "rescan / refresh / rename",
	"s": "save",
	"t": "Config: toggle",
	"u": "custom CRC form / update saved packet",
	"x": "delete / send",
	"H": "reorder field left", "L": "reorder field right", "N": "new protocol",
	"<": "reorder", ">": "reorder",
}

// ValidHotkeyChar reports whether key (a tea.KeyMsg.String() value) is in
// the hotkey palette — the only keys a Saved Packet hotkey may ever be
// assigned from.
func ValidHotkeyChar(key string) bool { return hotkeyPaletteSet[key] }

// ReservedKeyLabel returns a human label for key if it's a documented core
// binding, for a specific rejection message ("reserved for Quit") rather
// than a generic one.
func ReservedKeyLabel(key string) (string, bool) {
	label, ok := reservedKeyLabels[key]
	return label, ok
}

// ValidateHotkeyAssignment is the one function every hotkey-assigning form
// calls before persisting a Saved Packet's hotkey: empty (unbind) is always
// fine; otherwise the key must be in the palette, and must not already be
// bound to a different Saved Packet.
func ValidateHotkeyAssignment(key string, saved *savedpacket.Store, excludeName string) error {
	if key == "" {
		return nil
	}
	if !ValidHotkeyChar(key) {
		if label, ok := ReservedKeyLabel(key); ok {
			return fmt.Errorf("key %q is reserved for %s", key, label)
		}
		return fmt.Errorf("key %q is not in the supported hotkey set (single printable key, letters/digits/punctuation not already reserved)", key)
	}
	if conflict, ok := saved.HotkeyConflict(key, excludeName); ok {
		return fmt.Errorf("hotkey %q is already used by %q", key, conflict)
	}
	return nil
}
