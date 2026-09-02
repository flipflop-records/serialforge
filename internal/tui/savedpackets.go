package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vtemnyakov/serialforge/internal/packet"
	"github.com/vtemnyakov/serialforge/internal/savedpacket"
)

// The Saved Packets subview (product spec: reusable, hotkey-bindable
// packets built from a Protocol Profile + concrete field values + CRC
// mode). This file owns: the list/detail screen, create-adjacent actions
// (duplicate/rename/hotkey-assign/delete), direct send, and the global
// hotkey dispatch entry point (trySavedPacketHotkey, called from
// model.handleKey). Saving FROM TX Builder and loading INTO TX Builder are
// the primary create/edit workflow and live in txrx.go
// (submitSaveForm/updateSavedPacket) and loadSavedPacketIntoTX below —
// nothing here reimplements serialization: every send goes through
// savedpacket.SavedPacket.Build, the same function TX Builder and the CLI's
// `saved send` use.

type savedMode int

const (
	savedBrowse savedMode = iota
	savedFormRename
	savedFormDuplicate
	savedFormHotkey
	savedConfirmDelete
)

type savedState struct {
	cursor  int
	mode    savedMode
	form    *textForm
	message string
}

func newSavedState() savedState { return savedState{} }

// selected returns the saved packet currently under the cursor, if any.
func (s *savedState) selected(m *model) (savedpacket.SavedPacket, bool) {
	all := m.cfg.SavedPackets.All()
	if s.cursor < 0 || s.cursor >= len(all) {
		return savedpacket.SavedPacket{}, false
	}
	return all[s.cursor], true
}

// handleKeyIfEditing intercepts keys for the Saved Packets subview's forms
// — same funnel-first pattern as designer/tx/devices (see model.handleKey):
// this must claim the key before the global hotkey dispatch or core
// navigation ever sees it, so typing into (say) the rename field can never
// be mistaken for a hotkey press or a tab switch.
func (s *savedState) handleKeyIfEditing(m *model, msg tea.KeyMsg) (tea.Cmd, bool) {
	if m.tab != tabPackets || m.packetsView != packetsSaved || s.mode == savedBrowse {
		return nil, false
	}
	if s.mode == savedConfirmDelete {
		return nil, s.handleConfirmDelete(m, msg)
	}
	return nil, s.handleForm(m, msg)
}

func (s *savedState) handleForm(m *model, msg tea.KeyMsg) bool {
	submit, cancel := s.form.handleKey(msg)
	if cancel {
		s.mode = savedBrowse
		s.form = nil
		return true
	}
	if submit {
		s.submitForm(m)
	}
	return true
}

func (s *savedState) submitForm(m *model) {
	sp, ok := s.selected(m)
	if !ok {
		s.mode = savedBrowse
		s.form = nil
		return
	}
	switch s.mode {
	case savedFormRename:
		newName := strings.TrimSpace(s.form.values[0])
		if newName == "" {
			s.form.message = "enter a name"
			return
		}
		if err := m.cfg.SavedPackets.Rename(sp.Name, newName); err != nil {
			s.form.message = err.Error()
			return
		}
		if err := m.cfg.SavedPackets.Save(); err != nil {
			s.form.message = err.Error()
			return
		}
		m.status = "renamed to " + newName
	case savedFormDuplicate:
		newName := strings.TrimSpace(s.form.values[0])
		if newName == "" {
			s.form.message = "enter a name"
			return
		}
		if err := m.cfg.SavedPackets.Duplicate(sp.Name, newName); err != nil {
			s.form.message = err.Error()
			return
		}
		if err := m.cfg.SavedPackets.Save(); err != nil {
			s.form.message = err.Error()
			return
		}
		m.status = "duplicated as " + newName
	case savedFormHotkey:
		key := strings.TrimSpace(s.form.values[0])
		if err := ValidateHotkeyAssignment(key, m.cfg.SavedPackets, sp.Name); err != nil {
			s.form.message = err.Error()
			return
		}
		sp.Hotkey = key
		if err := m.cfg.SavedPackets.Put(sp); err != nil {
			s.form.message = err.Error()
			return
		}
		if err := m.cfg.SavedPackets.Save(); err != nil {
			s.form.message = err.Error()
			return
		}
		if key == "" {
			m.status = "cleared hotkey for " + sp.Name
		} else {
			m.status = key + " → " + sp.Name
		}
	}
	s.mode = savedBrowse
	s.form = nil
}

func (s *savedState) handleConfirmDelete(m *model, msg tea.KeyMsg) bool {
	switch msg.String() {
	case "y", "enter":
		if sp, ok := s.selected(m); ok {
			m.cfg.SavedPackets.Delete(sp.Name)
			if err := m.cfg.SavedPackets.Save(); err != nil {
				m.status = "delete: " + err.Error()
			} else {
				m.status = "deleted " + sp.Name
			}
			if n := len(m.cfg.SavedPackets.All()); s.cursor >= n && s.cursor > 0 {
				s.cursor = n - 1
			}
		}
		s.mode = savedBrowse
	case "esc", "n":
		s.mode = savedBrowse
	}
	return true
}

// --- browse mode: navigation + row actions ----------------------------------

func (m *model) updateSaved(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := &m.saved
	all := m.cfg.SavedPackets.All()
	switch msg.String() {
	case "up", "k":
		if s.cursor > 0 {
			s.cursor--
		}
	case "down", "j":
		if s.cursor < len(all)-1 {
			s.cursor++
		}
	case "enter":
		if sp, ok := s.selected(m); ok {
			m.loadSavedPacketIntoTX(sp)
		}
	case "x":
		if sp, ok := s.selected(m); ok {
			m.sendSavedPacket(sp, "")
		}
	case "delete", "backspace":
		if _, ok := s.selected(m); ok {
			s.mode = savedConfirmDelete
		}
	case "d":
		if sp, ok := s.selected(m); ok {
			s.mode = savedFormDuplicate
			s.form = newTextForm([]string{"New name"}, sp.Name+" copy")
		}
	case "r":
		if sp, ok := s.selected(m); ok {
			s.mode = savedFormRename
			s.form = newTextForm([]string{"New name"}, sp.Name)
		}
	case "h":
		if sp, ok := s.selected(m); ok {
			s.mode = savedFormHotkey
			s.form = newTextForm([]string{"Hotkey (empty clears)"}, sp.Hotkey)
		}
	}
	return m, nil
}

// loadSavedPacketIntoTX is Enter/"Load" — it loads sp's schema reference
// and values into TX Builder, switching to that subview, WITHOUT touching
// persistence (product spec §6: loading is not editing; only an explicit
// Save/Update in TX Builder writes back). A field-level mismatch
// (StatusIncompatible — a value no longer fitting, a field added/removed
// since save) still loads whatever's usable so the user can repair it in
// TX Builder; a missing or structurally invalid protocol does not, since
// that's a Designer-level problem TX Builder can't fix by editing field
// values.
func (m *model) loadSavedPacketIntoTX(sp savedpacket.SavedPacket) {
	res := sp.Resolve(m.cfg.Protocols)
	switch res.Status {
	case savedpacket.StatusProtocolMissing:
		m.status = sp.Name + " · protocol missing"
		return
	case savedpacket.StatusProtocolInvalid:
		m.status = sp.Name + " · protocol schema invalid — fix it in Designer first"
		return
	}

	sc := res.Schema
	t := &m.tx
	t.schema = &sc
	t.values = map[string]string{}
	for _, f := range sc.Fields {
		if v, ok := sp.Values[f.Name]; ok {
			t.values[f.Name] = cleanHexTUI(v)
		}
	}
	t.crcOverride = ""
	if sp.CRCMode == savedpacket.CRCModeOverride {
		t.crcOverride = cleanHexTUI(sp.CRCOverride)
	}
	t.fieldCursor = 0
	t.savedName = sp.Name
	t.dirty = false
	t.saveForm = nil
	t.message = ""
	if len(res.Problems) > 0 {
		t.message = fmt.Sprintf("loaded with %d problem(s): %s", len(res.Problems), res.Problems[0].String())
	}
	if m.sess != nil {
		m.connect(m.connectedPath, m.connectedCfg, t.schema)
	}
	m.activeSchema = t.schema
	m.packetsView = packetsTX
	m.status = "loaded " + sp.Name
}

// sendSavedPacket resolves+builds+sends sp through the ONE shared build
// path (savedpacket.SavedPacket.Build — the same call CLI `saved send`
// makes) and reports a one-line status (product spec §12: "the user should
// never wonder whether the key press actually did anything"). hotkey is
// the triggering key's string for a hotkey-fired send ("' → Get Status ·
// sent"), or "" for a direct send from the list/CLI-equivalent ("sent Get
// Status · 14 B").
func (m *model) sendSavedPacket(sp savedpacket.SavedPacket, hotkey string) {
	res := sp.Resolve(m.cfg.Protocols)
	if res.Status != savedpacket.StatusOK {
		m.status = sp.Name + " · " + statusShortMessage(res)
		return
	}
	pkt, err := sp.Build(m.cfg.Protocols)
	if err != nil {
		m.status = sp.Name + " · " + err.Error()
		return
	}
	if m.sess == nil {
		m.status = sp.Name + " · not connected"
		return
	}
	if _, err := m.sess.Send(pkt.Raw); err != nil {
		m.status = sp.Name + " · send failed: " + err.Error()
		return
	}
	if hotkey != "" {
		m.status = hotkey + " → " + sp.Name + " · sent"
	} else {
		m.status = "sent " + sp.Name + " · " + strconv.Itoa(len(pkt.Raw)) + " B"
	}
}

func statusShortMessage(res savedpacket.Resolution) string {
	switch res.Status {
	case savedpacket.StatusProtocolMissing:
		return "protocol missing"
	case savedpacket.StatusProtocolInvalid:
		return "protocol invalid"
	case savedpacket.StatusIncompatible:
		if len(res.Problems) > 0 {
			return "incompatible: " + res.Problems[0].String()
		}
		return "incompatible"
	default:
		return "not ready"
	}
}

// trySavedPacketHotkey is the global hotkey dispatch entry point, called
// from model.handleKey after every text-entry/modal intercept above it has
// had first refusal — see that call site's comment and keybindings.go for
// why single-key, palette-restricted hotkeys are safe to fire globally
// across every Navigation-mode screen. Requires a single plain printable
// rune (no modifier) — §11's v1 single-key scope.
func (m *model) trySavedPacketHotkey(msg tea.KeyMsg) (tea.Cmd, bool) {
	if msg.Type != tea.KeyRunes || len(msg.Runes) != 1 {
		return nil, false
	}
	key := msg.String()
	if !ValidHotkeyChar(key) {
		// Defense in depth: assignment already restricts storage to the
		// palette (ValidateHotkeyAssignment), so this only trips if
		// saved_packets.yaml was hand-edited outside the app.
		return nil, false
	}
	sp, ok := m.cfg.SavedPackets.FindByHotkey(key)
	if !ok {
		return nil, false
	}
	m.sendSavedPacket(sp, key)
	return nil, true
}

// --- rendering -----------------------------------------------------------------

func (m *model) viewSaved() string {
	s := &m.saved
	if s.mode == savedConfirmDelete {
		sp, _ := s.selected(m)
		return accentBox.Render(sectionStyle.Render("Delete "+sp.Name+"?") + "\n\n" +
			renderHints(hint("y/enter", "confirm"), hint("esc/n", "cancel")))
	}
	if s.form != nil {
		title := map[savedMode]string{
			savedFormRename:    "Rename saved packet",
			savedFormDuplicate: "Duplicate saved packet",
			savedFormHotkey:    "Assign hotkey",
		}[s.mode]
		return s.form.view(title)
	}

	all := m.cfg.SavedPackets.All()
	var b strings.Builder
	b.WriteString(sectionStyle.Render("Saved packets") + "\n\n")
	if len(all) == 0 {
		b.WriteString(dimStyle.Render("  (none yet — build a packet in TX Builder and press 's' to save it)\n"))
	}
	nameWidth := 18
	for _, sp := range all {
		if len(sp.Name) > nameWidth {
			nameWidth = len(sp.Name)
		}
	}
	if nameWidth > 28 {
		nameWidth = 28
	}
	for i, sp := range all {
		marker := "  "
		if i == s.cursor {
			marker = keyStyle.Render("▸ ")
		}
		hotkey := dimStyle.Render("·")
		if sp.Hotkey != "" {
			hotkey = keyStyle.Render(sp.Hotkey)
		}
		name := sp.Name
		if len(name) > nameWidth {
			name = name[:nameWidth-1] + "…"
		}
		res := sp.Resolve(m.cfg.Protocols)
		mark := ""
		if res.Status != savedpacket.StatusOK {
			mark = "  " + warnStyle.Render("!")
		}
		b.WriteString(fmt.Sprintf("%s%-*s %-6s %s%s\n", marker, nameWidth, name, hotkey, dimStyle.Render(sp.Protocol), mark))
	}
	b.WriteString("\n")

	if sp, ok := s.selected(m); ok {
		b.WriteString(m.viewSavedDetail(sp))
	}
	if s.message != "" {
		b.WriteString("\n" + badStyle.Render(s.message))
	}
	b.WriteString("\n\n" + renderHints(
		hint("enter", "load/edit"), hint("x", "send"), hint("d", "duplicate"),
		hint("r", "rename"), hint("h", "hotkey"), hint("delete", "remove")))
	return b.String()
}

// viewSavedDetail renders one saved packet's full state: identity, per-field
// values, the CRC line (reusing txCRCLine — no second CRC-presentation
// implementation), and the shared register-style diagram when the packet
// resolves cleanly; otherwise the specific reason it doesn't (product spec
// §16: "show useful states such as Protocol missing / Schema changed /
// Missing field / Value no longer fits").
func (m *model) viewSavedDetail(sp savedpacket.SavedPacket) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%-10s %s\n", "Name", sp.Name))
	b.WriteString(fmt.Sprintf("%-10s %s\n", "Protocol", sp.Protocol))
	hk := dimStyle.Render("(none)")
	if sp.Hotkey != "" {
		hk = keyStyle.Render(sp.Hotkey)
	}
	b.WriteString(fmt.Sprintf("%-10s %s\n\n", "Hotkey", hk))

	res := sp.Resolve(m.cfg.Protocols)
	switch res.Status {
	case savedpacket.StatusProtocolMissing:
		b.WriteString(badStyle.Render("Protocol missing: " + sp.Protocol))
		return b.String()
	case savedpacket.StatusProtocolInvalid:
		b.WriteString(badStyle.Render("Protocol schema invalid: " + res.Err().Error()))
		return b.String()
	case savedpacket.StatusIncompatible:
		b.WriteString(warnStyle.Render("Schema changed — incompatible with the current protocol:"))
		for _, p := range res.Problems {
			b.WriteString("\n  " + badStyle.Render(p.String()))
		}
		return b.String()
	}

	pkt, err := sp.Build(m.cfg.Protocols)
	if err != nil {
		b.WriteString(badStyle.Render(err.Error()))
		return b.String()
	}
	for _, f := range res.Schema.Fields {
		b.WriteString(fmt.Sprintf("  %-14s %s\n", f.Name, spacedHex(cleanHexTUI(sp.Values[f.Name]))))
	}
	if res.Schema.CRCSize() > 0 {
		crcOverrideHex := ""
		if sp.CRCMode == savedpacket.CRCModeOverride {
			crcOverrideHex = sp.CRCOverride
		}
		b.WriteString(fmt.Sprintf("  %-14s %s\n", "CRC", txCRCLine(res.Schema.Checksum, crcOverrideHex, pkt)))
	}

	b.WriteString("\n")
	values := packet.Values{}
	for _, fv := range pkt.Fields {
		values[fv.Field.Name] = fv.Raw
	}
	b.WriteString(RenderDiagram(res.Schema, DiagramOptions{Width: m.diagramWidth(), Selected: -1, Values: values, CRCResult: pkt.CRC, CRCDisplay: CRCDisplayAuto}))
	return b.String()
}

// --- textForm: a small shared "one field at a time" text-entry form -------
//
// The same Tab/Shift+Tab-to-move, Enter-on-last-field-to-submit idiom every
// other form in this TUI hand-rolls (addDeviceForm, manualConnectForm, the
// designer's custom-CRC form) — factored out here because this feature
// needs several near-identical small forms (TX Builder's Save-packet form,
// Saved Packets' rename/duplicate/hotkey forms) where re-deriving the same
// boilerplate four times would be pure duplication.
type textForm struct {
	labels  []string
	values  []string
	cursor  int
	message string
}

func newTextForm(labels []string, initial ...string) *textForm {
	f := &textForm{labels: labels, values: make([]string, len(labels))}
	copy(f.values, initial)
	return f
}

// handleKey applies one keypress. submit is true only on Enter at the last
// field (matching addDeviceForm's convention: Enter advances until the
// last field, then confirms); cancel is true on Esc.
func (f *textForm) handleKey(msg tea.KeyMsg) (submit, cancel bool) {
	switch msg.Type {
	case tea.KeyEsc:
		return false, true
	case tea.KeyTab, tea.KeyDown:
		f.cursor = (f.cursor + 1) % len(f.labels)
	case tea.KeyShiftTab, tea.KeyUp:
		f.cursor = (f.cursor - 1 + len(f.labels)) % len(f.labels)
	case tea.KeyBackspace:
		if v := f.values[f.cursor]; len(v) > 0 {
			f.values[f.cursor] = v[:len(v)-1]
		}
	case tea.KeyEnter:
		if f.cursor < len(f.labels)-1 {
			f.cursor++
			return false, false
		}
		return true, false
	case tea.KeyRunes:
		f.values[f.cursor] += string(msg.Runes)
	}
	return false, false
}

func (f *textForm) view(title string) string {
	var b strings.Builder
	b.WriteString(sectionStyle.Render(title) + "\n\n")
	for i, label := range f.labels {
		marker := "  "
		style := fieldTextStyle
		val := f.values[i]
		if i == f.cursor {
			marker = keyStyle.Render("▸ ")
			style = keyStyle
			val += "█"
		}
		b.WriteString(fmt.Sprintf("%s%-12s %s\n", marker, label, style.Render(val)))
	}
	if f.message != "" {
		b.WriteString("\n" + badStyle.Render(f.message))
	}
	b.WriteString("\n" + renderHints(hint("tab/↓", "next field"), hint("enter", "confirm (last field)"), hint("esc", "cancel")))
	return accentBox.Render(b.String())
}
