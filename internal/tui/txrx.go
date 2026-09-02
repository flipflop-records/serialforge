package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vtemnyakov/serialforge/internal/checksum"
	"github.com/vtemnyakov/serialforge/internal/packet"
	"github.com/vtemnyakov/serialforge/internal/savedpacket"
)

// --- TX Builder (product spec §15) ------------------------------------------

type txMode int

const (
	txBrowse txMode = iota
	txPicker
	txEditField
	txEditCRC
)

type txState struct {
	schema       *packet.Schema
	fieldCursor  int
	values       map[string]string // field name -> hex string as typed
	crcOverride  string            // hex string; empty = AUTO
	mode         txMode
	pickerCursor int
	editBuf      string
	message      string

	// savedName/dirty track this TX session's relationship to a Saved
	// Packet (product spec: "load Saved Packet -> user edits -> current TX
	// packet is dirty -> original Saved Packet remains unchanged -> user
	// may choose Update Saved Packet"). savedName == "" means this session
	// isn't tied to any Saved Packet — editing here never mutates
	// persistence on its own; only 's' (save/save-as) or 'u' (update) do.
	savedName string
	dirty     bool
	saveForm  *textForm // non-nil while the Save-packet form is open
}

func newTXState() txState {
	return txState{values: map[string]string{}}
}

func (t *txState) handleKeyIfEditing(m *model, msg tea.KeyMsg) (tea.Cmd, bool) {
	if m.tab != tabPackets || m.packetsView != packetsTX {
		return nil, false
	}
	if t.saveForm != nil {
		return nil, t.handleSaveForm(m, msg)
	}
	if t.mode == txBrowse {
		return nil, false
	}
	switch t.mode {
	case txPicker:
		return nil, t.handlePicker(m, msg)
	case txEditField, txEditCRC:
		return nil, t.handleEdit(msg)
	}
	return nil, false
}

// --- Save packet / Update saved packet (product spec §4/§6) ----------------

// handleSaveForm drives the "Save packet" name+hotkey form opened by 's'.
// Submitting persists the CURRENT TX Builder state (schema reference,
// field values, CRC mode) as a new or replaced Saved Packet — see
// submitSaveForm. This never fires implicitly from editing; only an
// explicit 's' (save/save-as) or 'u' (update) ever writes to
// SavedPackets — loading/editing a Saved Packet in TX Builder must never
// auto-mutate persistence (product spec §6).
func (t *txState) handleSaveForm(m *model, msg tea.KeyMsg) bool {
	submit, cancel := t.saveForm.handleKey(msg)
	if cancel {
		t.saveForm = nil
		return true
	}
	if submit {
		t.submitSaveForm(m)
	}
	return true
}

func (t *txState) submitSaveForm(m *model) {
	name := strings.TrimSpace(t.saveForm.values[0])
	hotkey := strings.TrimSpace(t.saveForm.values[1])
	if name == "" {
		t.saveForm.message = "enter a name"
		return
	}
	if t.schema == nil {
		t.saveForm.message = "open a protocol first"
		return
	}
	if err := ValidateHotkeyAssignment(hotkey, m.cfg.SavedPackets, name); err != nil {
		t.saveForm.message = err.Error()
		return
	}
	sp := savedpacket.SavedPacket{
		Name: name, Protocol: t.schema.Name, Values: copyStringMap(t.values),
		CRCMode: savedpacket.CRCModeAuto, Hotkey: hotkey,
	}
	if t.crcOverride != "" {
		sp.CRCMode = savedpacket.CRCModeOverride
		sp.CRCOverride = t.crcOverride
	}
	if err := m.cfg.SavedPackets.Put(sp); err != nil {
		t.saveForm.message = err.Error()
		return
	}
	if err := m.cfg.SavedPackets.Save(); err != nil {
		t.saveForm.message = err.Error()
		return
	}
	t.savedName = name
	t.dirty = false
	t.saveForm = nil
	m.status = "saved packet " + name
}

// updateSavedPacket is 'u' — refresh the Saved Packet this TX session was
// loaded from with the current field values/CRC mode, keeping its existing
// name and hotkey. Only meaningful once a Saved Packet has actually been
// loaded (t.savedName != "") — building from scratch always goes through
// 's' (Save), which is how a first name/hotkey gets chosen.
func (m *model) updateSavedPacket() {
	t := &m.tx
	if t.savedName == "" {
		t.message = "not loaded from a saved packet — press 's' to save as new"
		return
	}
	if t.schema == nil {
		t.message = "open a protocol first"
		return
	}
	existing, ok := m.cfg.SavedPackets.Get(t.savedName)
	if !ok {
		t.message = "saved packet " + t.savedName + " no longer exists — press 's' to save as new"
		return
	}
	sp := savedpacket.SavedPacket{
		Name: existing.Name, Protocol: t.schema.Name, Values: copyStringMap(t.values),
		CRCMode: savedpacket.CRCModeAuto, Hotkey: existing.Hotkey,
	}
	if t.crcOverride != "" {
		sp.CRCMode = savedpacket.CRCModeOverride
		sp.CRCOverride = t.crcOverride
	}
	if err := m.cfg.SavedPackets.Put(sp); err != nil {
		t.message = err.Error()
		return
	}
	if err := m.cfg.SavedPackets.Save(); err != nil {
		t.message = err.Error()
		return
	}
	t.dirty = false
	m.status = "updated saved packet " + sp.Name
}

func copyStringMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (t *txState) handlePicker(m *model, msg tea.KeyMsg) bool {
	names := m.cfg.Protocols.Names()
	switch msg.String() {
	case "esc":
		t.mode = txBrowse
	case "up", "k":
		if t.pickerCursor > 0 {
			t.pickerCursor--
		}
	case "down", "j":
		if t.pickerCursor < len(names)-1 {
			t.pickerCursor++
		}
	case "enter":
		if t.pickerCursor < len(names) {
			sc, _ := m.cfg.Protocols.Get(names[t.pickerCursor])
			t.schema = &sc
			t.values = map[string]string{}
			t.fieldCursor = 0
			t.mode = txBrowse
			// A freshly chosen protocol is a fresh packet, not an edit of
			// whatever Saved Packet (if any) was previously loaded here.
			t.savedName = ""
			t.dirty = false
			if m.sess != nil {
				m.connect(m.connectedPath, m.connectedCfg, t.schema)
			}
			m.activeSchema = t.schema
		}
	}
	return true
}

func (t *txState) handleEdit(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyEsc:
		t.mode = txBrowse
		return true
	case tea.KeyEnter:
		return t.submitEdit()
	case tea.KeyBackspace:
		if len(t.editBuf) > 0 {
			t.editBuf = t.editBuf[:len(t.editBuf)-1]
		}
	case tea.KeyRunes:
		t.editBuf += strings.ToUpper(string(msg.Runes))
	}
	return true
}

func (t *txState) submitEdit() bool {
	if t.mode == txEditCRC {
		newOverride := strings.TrimSpace(t.editBuf)
		if t.savedName != "" && newOverride != t.crcOverride {
			t.dirty = true // §6: editing after a load makes the TX session dirty; the Saved Packet itself is untouched until 'u'
		}
		t.crcOverride = newOverride
		t.mode = txBrowse
		return true
	}
	if t.schema == nil || t.fieldCursor >= len(t.schema.Fields) {
		t.mode = txBrowse
		return true
	}
	f := t.schema.Fields[t.fieldCursor]
	clean := cleanHexTUI(t.editBuf)
	if len(clean)/2 != f.Size {
		t.message = fmt.Sprintf("%s needs exactly %d bytes (%d hex digits)", f.Name, f.Size, f.Size*2)
		return true
	}
	if t.savedName != "" && t.values[f.Name] != clean {
		t.dirty = true
	}
	t.values[f.Name] = clean
	t.mode = txBrowse
	t.message = ""
	return true
}

func cleanHexTUI(s string) string {
	return strings.NewReplacer(" ", "", "0X", "", "0x", "").Replace(strings.ToUpper(s))
}

func (m *model) updateTX(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	t := &m.tx
	switch msg.String() {
	case "o":
		t.mode = txPicker
		t.pickerCursor = 0
	case "up", "k":
		if t.schema != nil && t.fieldCursor > 0 {
			t.fieldCursor--
		}
	case "down", "j":
		if t.schema != nil && t.fieldCursor < len(t.schema.Fields)-1 {
			t.fieldCursor++
		}
	case "enter":
		if t.schema != nil && t.fieldCursor < len(t.schema.Fields) {
			t.mode = txEditField
			t.editBuf = t.values[t.schema.Fields[t.fieldCursor].Name]
		}
	case "c":
		t.mode = txEditCRC
		t.editBuf = t.crcOverride
	case "x":
		return m, m.sendTXPacket()
	case "s":
		if t.schema == nil {
			t.message = "open a protocol first ('o')"
			return m, nil
		}
		name, hotkey := t.schema.Name, ""
		if t.savedName != "" {
			name = t.savedName
			if sp, ok := m.cfg.SavedPackets.Get(t.savedName); ok {
				hotkey = sp.Hotkey
			}
		}
		t.saveForm = newTextForm([]string{"Name", "Hotkey"}, name, hotkey)
	case "u":
		m.updateSavedPacket()
	}
	return m, nil
}

func (m *model) sendTXPacket() tea.Cmd {
	t := &m.tx
	if t.schema == nil {
		t.message = "open a protocol first ('o')"
		return nil
	}
	values := packet.Values{}
	for _, f := range t.schema.Fields {
		raw, err := decodeHexTUI(t.values[f.Name])
		if err != nil || len(raw) != f.Size {
			t.message = fmt.Sprintf("field %q is not set (or wrong length)", f.Name)
			return nil
		}
		values[f.Name] = raw
	}
	var crcOverride *uint64
	if t.crcOverride != "" {
		raw, err := decodeHexTUI(t.crcOverride)
		if err != nil {
			t.message = "bad CRC override hex"
			return nil
		}
		v := uint64(0)
		for _, b := range raw {
			v = v<<8 | uint64(b)
		}
		crcOverride = &v
	}
	pkt, err := packet.Build(*t.schema, values, crcOverride)
	if err != nil {
		t.message = err.Error()
		return nil
	}
	if m.sess == nil {
		t.message = "not connected — packet built but not sent (see Devices)"
		return nil
	}
	if _, err := m.sess.Send(pkt.Raw); err != nil {
		t.message = "send: " + err.Error()
		return nil
	}
	t.message = ""
	m.status = "sent " + strconv.Itoa(len(pkt.Raw)) + " bytes"
	return nil
}

func decodeHexTUI(s string) ([]byte, error) {
	s = cleanHexTUI(s)
	out := make([]byte, len(s)/2)
	for i := range out {
		var b int
		if _, err := fmt.Sscanf(s[i*2:i*2+2], "%02X", &b); err != nil {
			return nil, err
		}
		out[i] = byte(b)
	}
	return out, nil
}

func (m *model) viewTX() string {
	t := &m.tx
	if t.saveForm != nil {
		title := "Save packet"
		if t.savedName != "" {
			title = "Save packet as"
		}
		return t.saveForm.view(title)
	}
	if t.mode == txPicker {
		return m.viewProtocolPicker("Choose protocol for TX", t.pickerCursor)
	}
	if t.schema == nil {
		return dimStyle.Render("No protocol selected — press 'o' to choose one.")
	}

	values := packet.Values{}
	complete := true
	for _, f := range t.schema.Fields {
		raw, err := decodeHexTUI(t.values[f.Name])
		if err != nil || len(raw) != f.Size {
			complete = false
			continue
		}
		values[f.Name] = raw
	}
	var pkt *packet.Packet
	if complete {
		var crcOverride *uint64
		if t.crcOverride != "" {
			if raw, err := decodeHexTUI(t.crcOverride); err == nil {
				v := uint64(0)
				for _, x := range raw {
					v = v<<8 | uint64(x)
				}
				crcOverride = &v
			}
		}
		if built, err := packet.Build(*t.schema, values, crcOverride); err == nil {
			pkt = built
		}
	}

	var b strings.Builder
	b.WriteString(sectionStyle.Render(t.schema.Name))
	if t.savedName != "" {
		b.WriteString("  " + dimStyle.Render("← "+t.savedName))
		if t.dirty {
			b.WriteString("  " + warnStyle.Render("modified"))
		}
	}
	b.WriteString("\n\n")
	for i, f := range t.schema.Fields {
		marker := "  "
		if i == t.fieldCursor {
			marker = keyStyle.Render("▸ ")
		}
		v := t.values[f.Name]
		if v == "" {
			v = dimStyle.Render("(not set)")
		} else {
			v = spacedHex(v)
		}
		b.WriteString(fmt.Sprintf("%s%-16s %s\n", marker, f.Name, v))
	}
	if t.schema.CRCSize() > 0 {
		b.WriteString(fmt.Sprintf("  %-16s %s\n", "CRC", txCRCLine(t.schema.Checksum, t.crcOverride, pkt)))
	}

	b.WriteString("\n")
	if pkt != nil {
		b.WriteString(RenderDiagram(*t.schema, DiagramOptions{Width: m.diagramWidth(), Selected: t.fieldCursor, Values: values, CRCResult: pkt.CRC, CRCDisplay: CRCDisplayAuto}))
		b.WriteString("\n" + dimStyle.Render("raw: ") + hexBytes(pkt.Raw))
	} else {
		b.WriteString(RenderDiagram(*t.schema, DiagramOptions{Width: m.diagramWidth(), Selected: t.fieldCursor}))
	}

	if t.message != "" {
		b.WriteString("\n" + badStyle.Render(t.message))
	}
	if t.mode == txEditField || t.mode == txEditCRC {
		label := "field"
		if t.mode == txEditCRC {
			label = "CRC override"
		}
		b.WriteString("\n\n" + accentBox.Render(fmt.Sprintf("%s: %s█\n%s", label, t.editBuf,
			dimStyle.Render("hex bytes · enter confirm · esc cancel"))))
	} else {
		hint := "enter edit field   c set/clear CRC override   x send   o change protocol   s save packet"
		if t.savedName != "" {
			hint += "   u update saved packet"
		}
		b.WriteString("\n\n" + dimStyle.Render(hint))
	}
	return b.String()
}

// txCRCLine renders the TX Builder's field-list CRC row: the configured
// algorithm's name (entirely checksum.Definition's own — see
// AlgorithmName/AlgorithmLabels in internal/checksum/registry.go, never
// re-derived here), whether the value is AUTO or a manual OVERRIDE, and —
// as the fact that matters most, since it's the actual byte about to go on
// the wire — the value itself, visually emphasized over the mode word.
// Deliberately never PASS/FAIL: an unoverridden TX packet's CRC agreeing
// with its own arithmetic is not the same claim as a device confirming
// what it received — see packet.CRCResult's doc comment.
func txCRCLine(def checksum.Definition, crcOverrideHex string, pkt *packet.Packet) string {
	manual := crcOverrideHex != ""
	mode := "AUTO"
	if manual {
		mode = "OVERRIDE"
	}
	header := dimStyle.Render(fmt.Sprintf("%s · %s", def.AlgorithmName(), mode))
	if pkt == nil || pkt.CRC == nil {
		return header + dimStyle.Render("  (fields incomplete)")
	}
	valueStyle := crcTextStyle
	if manual {
		valueStyle = warnStyle
	}
	line := header + dimStyle.Render(" → ") + valueStyle.Render(crcHexValue(pkt.CRC.Width, pkt.CRC.Received))
	if pkt.CRC.Overridden {
		line += dimStyle.Render(fmt.Sprintf("  (calculated %s)", crcHexValue(pkt.CRC.Width, pkt.CRC.Calculated)))
	}
	return line
}

func spacedHex(clean string) string {
	var parts []string
	for i := 0; i+1 < len(clean); i += 2 {
		parts = append(parts, clean[i:i+2])
	}
	return strings.Join(parts, " ")
}

func (m *model) viewProtocolPicker(title string, cursor int) string {
	names := m.cfg.Protocols.Names()
	var b strings.Builder
	b.WriteString(sectionStyle.Render(title) + "\n\n")
	if len(names) == 0 {
		b.WriteString(dimStyle.Render("  (no saved protocols — see Packets/Designer)\n"))
	}
	for i, n := range names {
		marker := "  "
		if i == cursor {
			marker = keyStyle.Render("▸ ")
		}
		b.WriteString(marker + n + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("enter select · esc cancel"))
	return accentBox.Render(b.String())
}

// --- RX Inspector (product spec §16/§17) ------------------------------------

type rxState struct {
	history      []*packet.Packet
	cursor       int
	pickerOpen   bool
	pickerCursor int
}

const maxRXHistory = 500

func newRXState() rxState { return rxState{} }

func (m *model) updateRX(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	r := &m.rx
	if r.pickerOpen {
		names := m.cfg.Protocols.Names()
		switch msg.String() {
		case "esc":
			r.pickerOpen = false
		case "up", "k":
			if r.pickerCursor > 0 {
				r.pickerCursor--
			}
		case "down", "j":
			if r.pickerCursor < len(names)-1 {
				r.pickerCursor++
			}
		case "enter":
			if r.pickerCursor < len(names) {
				sc, _ := m.cfg.Protocols.Get(names[r.pickerCursor])
				if m.sess != nil {
					m.connect(m.connectedPath, m.connectedCfg, &sc)
				} else {
					m.activeSchema = &sc
				}
				r.pickerOpen = false
			}
		}
		return m, nil
	}
	switch msg.String() {
	case "o":
		r.pickerOpen = true
		r.pickerCursor = 0
	case "up", "k":
		if r.cursor > 0 {
			r.cursor--
		}
	case "down", "j":
		if r.cursor < len(r.history)-1 {
			r.cursor++
		}
	case "c":
		r.history = nil
		r.cursor = 0
	}
	return m, nil
}

func (m *model) viewRX() string {
	r := &m.rx
	if r.pickerOpen {
		return m.viewProtocolPicker("Choose protocol for RX decoding", r.pickerCursor)
	}
	if m.activeSchema == nil {
		return dimStyle.Render("No protocol selected — press 'o' to choose one (also reframes the live connection to that packet size).")
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s   %d packets captured\n\n", sectionStyle.Render(m.activeSchema.Name), len(r.history)))
	if len(r.history) == 0 {
		b.WriteString(dimStyle.Render("Waiting for packets…") + "\n\n" + dimStyle.Render("o change protocol   c clear history"))
		return b.String()
	}
	if r.cursor >= len(r.history) {
		r.cursor = len(r.history) - 1
	}
	pkt := r.history[r.cursor]

	b.WriteString(fmt.Sprintf("Packet #%d/%d   %s\n\n", r.cursor+1, len(r.history), pkt.Timestamp.Format("15:04:05.000")))
	values := packet.Values{}
	for _, fv := range pkt.Fields {
		values[fv.Field.Name] = fv.Raw
	}
	b.WriteString(RenderDiagram(*m.activeSchema, DiagramOptions{Width: m.diagramWidth(), Selected: -1, Values: values, CRCResult: pkt.CRC, CRCDisplay: CRCDisplayCompare}))
	b.WriteString("\n" + dimStyle.Render("raw: ") + hexBytes(pkt.Raw))
	if pkt.CRC != nil {
		b.WriteString("\n" + rxCRCLine(*pkt.CRC))
	}
	b.WriteString("\n\n" + dimStyle.Render("↑/↓ browse history   o change protocol   c clear history"))
	return b.String()
}

// rxCRCLine is the RX Inspector's explicit CRC RX / CRC CALC / PASS-FAIL
// breakdown: both sides of the comparison the diagram's PASS/FAIL cell
// summarizes, spelled out so a mismatch shows exactly which byte the
// device sent versus what the schema's algorithm computes — this is the
// one place in the app PASS/FAIL belongs, since it's comparing bytes that
// actually arrived over the wire (see packet.CRCResult's doc comment).
func rxCRCLine(r packet.CRCResult) string {
	status := okStyle.Render("PASS")
	if !r.Valid {
		status = badStyle.Render("FAIL")
	}
	return fmt.Sprintf("%s %s   %s %s   %s",
		dimStyle.Render("CRC RX"), crcHexValue(r.Width, r.Received),
		dimStyle.Render("CALC"), crcHexValue(r.Width, r.Calculated),
		status)
}
