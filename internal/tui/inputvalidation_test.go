package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vtemnyakov/serialforge/internal/serial"
)

// This file is the regression suite for character-level input validation
// across every numeric text editor in the TUI — see boundedinput.go's own
// doc comment for the shared helpers (appendDecimalDigits/
// appendDigitsWithinMax/appendHexWithinDigitLimit) every one of these
// editors now funnels through. The reported bug: a decimal or hex field
// let arbitrary letters ("U"/"I"/"Z") into the buffer while typing —
// previously only the *count* of digits was bounded (appendDigitsWithinMax
// appended any non-digit rune unconditionally; appendHexWithinDigitLimit
// appended any non-hex rune unconditionally, only excluding it from the
// digit count). These tests drive the real editor Update/key paths, not
// just the helpers directly (already covered in boundedinput_test.go).

// --- Designer: field size (decimal, bounded) ---------------------------------

func TestDesignerFieldSizeRejectsLetters(t *testing.T) {
	// fieldSizeBudgetSchema()'s own max is 11 (see TestFieldSizeEditorAcceptsExactMax),
	// so this stays well within the bound — isolating the character-class
	// concern from the separately-tested max-bound one.
	m := newDesignerTestModel(t)
	d := &m.designer
	d.schema = fieldSizeBudgetSchema()
	d.openFieldForm(-1)
	d.fieldFocus = 1

	typeString(m, "1U0I")
	if d.fieldSize != "10" {
		t.Errorf("fieldSize = %q, want %q — letters must never reach a decimal buffer", d.fieldSize, "10")
	}
	for _, letter := range []string{"U", "I", "A", "Z"} {
		d.fieldSize = "5"
		typeString(m, letter)
		if d.fieldSize != "5" {
			t.Errorf("letter %q: fieldSize = %q, want unchanged %q", letter, d.fieldSize, "5")
		}
	}
}

// --- Designer: packet total size (decimal, unbounded) ------------------------

func TestDesignerTotalSizeAcceptsOnlyDigits(t *testing.T) {
	m := newDesignerTestModel(t)
	d := &m.designer
	d.mode = dmTotalSize
	d.totalSizeBuf = ""

	d.handleTotalSizeForm(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1U2I3Z")})
	if d.totalSizeBuf != "123" {
		t.Errorf("totalSizeBuf = %q, want %q", d.totalSizeBuf, "123")
	}
}

func TestDesignerTotalSizeRejectsEachReportedLetter(t *testing.T) {
	for _, letter := range []string{"U", "I", "G", "Z"} {
		m := newDesignerTestModel(t)
		d := &m.designer
		d.mode = dmTotalSize
		d.totalSizeBuf = "12"

		d.handleTotalSizeForm(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(letter)})
		if d.totalSizeBuf != "12" {
			t.Errorf("letter %q: totalSizeBuf = %q, want unchanged %q", letter, d.totalSizeBuf, "12")
		}
	}
}

// Submit-time validation still catches malformed state constructed
// directly (defense in depth, independent of whether the character filter
// could ever actually produce this state through normal typing).
func TestDesignerTotalSizeSubmitValidationCatchesMalformedState(t *testing.T) {
	m := newDesignerTestModel(t)
	d := &m.designer
	d.mode = dmTotalSize
	d.totalSizeBuf = "abc" // bypasses the character filter by direct assignment

	d.handleTotalSizeForm(tea.KeyMsg{Type: tea.KeyEnter})
	if d.mode != dmTotalSize {
		t.Fatalf("malformed totalSizeBuf should not be accepted, mode = %v", d.mode)
	}
	if !strings.Contains(d.message, "whole number") {
		t.Errorf("expected a validation message, got %q", d.message)
	}
}

// Backspace must remain unaffected by the character filter.
func TestDesignerTotalSizeBackspaceStillWorks(t *testing.T) {
	m := newDesignerTestModel(t)
	d := &m.designer
	d.mode = dmTotalSize
	d.totalSizeBuf = ""

	typeString(m, "123")
	pressKey(m, tea.KeyBackspace)
	if d.totalSizeBuf != "12" {
		t.Errorf("after typing 123 and one backspace, totalSizeBuf = %q, want %q", d.totalSizeBuf, "12")
	}
}

// --- Designer: custom CRC Width (decimal, bounded) ---------------------------

func TestDesignerCRCWidthRejectsLetters(t *testing.T) {
	m := newDesignerTestModel(t)
	d := &m.designer
	d.schema = baseDesignerSchema()
	openCustomCRCAtCursor(d, 0) // Width
	d.customWidth = ""

	d.handleCRCCustom(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("6U4I")})
	if d.customWidth != "64" {
		t.Errorf("customWidth = %q, want %q", d.customWidth, "64")
	}
}

// --- Designer: custom CRC Poly/Init/XOR-Out (hex, bounded) -------------------

func TestDesignerCRCPolyAcceptsHexRejectsOutsideLetters(t *testing.T) {
	m := newDesignerTestModel(t)
	d := &m.designer
	d.schema = baseDesignerSchema()
	openCustomCRCAtCursor(d, 1) // Poly
	d.customPoly = ""

	d.handleCRCCustom(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("AB")})
	if d.customPoly != "AB" {
		t.Fatalf("customPoly = %q, want %q (A-F must be accepted)", d.customPoly, "AB")
	}
	d.customCursor = 1
	d.customPoly = ""
	for _, letter := range []string{"G", "U", "I", "Z"} {
		d.handleCRCCustom(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(letter)})
	}
	if d.customPoly != "" {
		t.Errorf("customPoly after G/U/I/Z = %q, want empty (none of those are hex digits)", d.customPoly)
	}
}

func TestDesignerCRCInitAndXorOutRejectNonHexLetters(t *testing.T) {
	m := newDesignerTestModel(t)
	d := &m.designer
	d.schema = baseDesignerSchema()

	openCustomCRCAtCursor(d, 2) // Init
	d.customInit = ""
	d.handleCRCCustom(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("FFU")})
	if d.customInit != "FF" {
		t.Errorf("customInit = %q, want %q", d.customInit, "FF")
	}

	d.customCursor = 5 // XOR Out
	d.customXorOut = ""
	d.handleCRCCustom(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ZZ")})
	if d.customXorOut != "" {
		t.Errorf("customXorOut = %q, want empty (Z is not hex)", d.customXorOut)
	}
}

// --- TX Builder: field value editor (hex, bounded) ---------------------------

func TestTXFieldEditAcceptsHexRejectsUIGLetters(t *testing.T) {
	m := newTestModel(t)
	m.tab, m.packetsView = tabPackets, packetsTX
	sc := txTestSchema()
	m.tx.schema = &sc
	openTXFieldEdit(t, m, "header") // 2-byte field, 4 hex digits max

	typeString(m, "DEADBEEF")
	if m.tx.editBuf != "DEAD" {
		t.Fatalf("editBuf = %q, want %q (DEADBEEF's valid hex prefix within the 4-digit budget)", m.tx.editBuf, "DEAD")
	}

	m.tx.editBuf = ""
	typeString(m, "DEADI") // 'I' is not hex — must be dropped, not merely excluded from the digit count
	if m.tx.editBuf != "DEAD" {
		t.Errorf("editBuf = %q, want %q — 'I' must never appear in the buffer", m.tx.editBuf, "DEAD")
	}

	m.tx.editBuf = ""
	for _, letter := range []string{"U", "I", "G", "Z"} {
		typeString(m, letter)
	}
	if m.tx.editBuf != "" {
		t.Errorf("editBuf after U/I/G/Z = %q, want empty", m.tx.editBuf)
	}
}

func TestTXFieldEditAcceptsMixedCaseHex(t *testing.T) {
	m := newTestModel(t)
	m.tab, m.packetsView = tabPackets, packetsTX
	sc := txTestSchema()
	m.tx.schema = &sc
	openTXFieldEdit(t, m, "header")

	typeString(m, "aBcD")
	if m.tx.editBuf != "ABCD" {
		t.Errorf("editBuf = %q, want %q (mixed-case hex, uppercased)", m.tx.editBuf, "ABCD")
	}
}

// --- TX Builder: CRC override (hex, bounded) ----------------------------------

func TestTXCRCOverrideAcceptsHexRejectsUILetters(t *testing.T) {
	m := newTestModel(t)
	m.tab, m.packetsView = tabPackets, packetsTX
	sc := txTestSchema() // CRC-8/MAXIM-DOW: 1 byte, 2 hex digits max
	m.tx.schema = &sc

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if m.tx.mode != txEditCRC {
		t.Fatalf("'c' should open the CRC override editor, mode = %v", m.tx.mode)
	}

	typeString(m, "AB")
	if m.tx.editBuf != "AB" {
		t.Fatalf("editBuf = %q, want %q", m.tx.editBuf, "AB")
	}
	m.tx.editBuf = ""
	typeString(m, "U")
	if m.tx.editBuf != "" {
		t.Errorf("editBuf after U = %q, want empty", m.tx.editBuf)
	}
}

// --- Config: custom baud (decimal, unbounded) ---------------------------------

func TestConfigCustomBaudAcceptsOnlyDigits(t *testing.T) {
	m := enterSerialDefaults(t)
	m.sd.openPicker(sfBaud)
	m.sd.pickerCursor = len(serial.BaudPresets) // trailing "Custom…" row
	m.sd.confirmPicker()
	if m.sd.mode != sdBaudCustom || m.sd.baudInput == nil {
		t.Fatalf("selecting Custom… should open the baud text form, mode=%v", m.sd.mode)
	}
	m.sd.baudInput.values[0] = ""

	m.sd.handleBaudCustom(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("9U6I0G0Z0")})
	if m.sd.baudInput.values[0] != "96000" {
		t.Errorf("custom baud buffer = %q, want %q", m.sd.baudInput.values[0], "96000")
	}
}

func TestConfigCustomBaudBackspaceStillWorks(t *testing.T) {
	m := enterSerialDefaults(t)
	m.sd.openPicker(sfBaud)
	m.sd.pickerCursor = len(serial.BaudPresets)
	m.sd.confirmPicker()
	m.sd.baudInput.values[0] = ""

	m.sd.handleBaudCustom(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("115200")})
	m.sd.handleBaudCustom(tea.KeyMsg{Type: tea.KeyBackspace})
	if m.sd.baudInput.values[0] != "11520" {
		t.Errorf("after backspace, buffer = %q, want %q", m.sd.baudInput.values[0], "11520")
	}
}

// --- Devices: add-profile form's Baud field (decimal); other fields stay free text ---

func TestDevAddFormBaudRejectsLettersOtherFieldsStayFreeText(t *testing.T) {
	m := newTestModel(t)
	m.tab = tabDevices
	m.devAdd = newAddDeviceForm(nil)

	m.devAdd.cursor = addFieldAlias
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("USB-A")})
	if m.devAdd.values[addFieldAlias] != "USB-A" {
		t.Errorf("Alias = %q, want %q (free text, letters allowed)", m.devAdd.values[addFieldAlias], "USB-A")
	}

	m.devAdd.cursor = addFieldVID
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2341")})
	if m.devAdd.values[addFieldVID] != "2341" {
		t.Errorf("VID = %q, want %q (free-form comparison string, unaffected)", m.devAdd.values[addFieldVID], "2341")
	}

	m.devAdd.cursor = addFieldBaud
	m.devAdd.values[addFieldBaud] = ""
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1U1I5G2Z0Z0")})
	if m.devAdd.values[addFieldBaud] != "115200" {
		t.Errorf("Baud = %q, want %q", m.devAdd.values[addFieldBaud], "115200")
	}
}

// --- Devices: manual-connect form's baud field (decimal); path stays free text ---

func TestDevManualConnectBaudRejectsLettersPathStaysFreeText(t *testing.T) {
	m := newTestModel(t)
	m.tab = tabDevices
	m.devManual = newManualConnectForm()
	m.devManual.cursor = 0

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/dev/ttyUSB0")})
	if m.devManual.path != "/dev/ttyUSB0" {
		t.Errorf("path = %q, want %q (free text)", m.devManual.path, "/dev/ttyUSB0")
	}

	m.devManual.cursor = 1
	m.devManual.baud = ""
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("9U6I0G0")})
	if m.devManual.baud != "9600" {
		t.Errorf("baud = %q, want %q", m.devManual.baud, "9600")
	}
}

// --- paste behavior: a single multi-rune KeyMsg (bracketed paste) ------------

func TestPasteIntoHexFieldOnlyValidRunesSurvive(t *testing.T) {
	m := newTestModel(t)
	m.tab, m.packetsView = tabPackets, packetsTX
	sc := txTestSchema()
	m.tx.schema = &sc
	openTXFieldEdit(t, m, "header")

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("AAZZ55"), Paste: true})
	if m.tx.editBuf != "AA55" {
		t.Errorf("pasted \"AAZZ55\" into a hex field = %q, want %q", m.tx.editBuf, "AA55")
	}
}

func TestPasteIntoDecimalFieldOnlyValidRunesSurvive(t *testing.T) {
	m := newDesignerTestModel(t)
	d := &m.designer
	d.mode = dmTotalSize
	d.totalSizeBuf = ""

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("12U4"), Paste: true})
	if d.totalSizeBuf != "124" {
		t.Errorf("pasted \"12U4\" into a decimal field = %q, want %q", d.totalSizeBuf, "124")
	}
}
