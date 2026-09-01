package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vtemnyakov/serialforge/internal/device"
)

// The Virtual/Manual endpoint chooser (Devices tab, 'm'): typing a raw
// filesystem path must be the FALLBACK workflow, not the primary one. This
// screen surfaces discoverable candidates (live socat-style friendly
// symlinks, path-only saved profiles, recent history) with "Enter custom
// path..." always present as the explicit fallback action — see
// internal/device/virtual.go for how candidates are discovered/
// deduplicated, and ARCHITECTURE.md's "Virtual / manual endpoint discovery"
// section and its "hardware discovery and virtual/manual endpoint
// discovery are separate concerns" invariant for why this never touches
// normal hardware enumeration.

// virtualRow is one rendered line: a non-selectable section header, the
// trailing "Enter custom path..." action, or a Candidate.
type virtualRow struct {
	header  string
	isEntry bool
	cand    *device.Candidate
}

type virtualChooserState struct {
	rows       []virtualRow
	selectable []int // indices into rows that the cursor can land on
	pos        int   // index into selectable
}

// buildVirtualChooserFunc is a package var (not a plain call to
// newVirtualChooser) purely so tests can point candidate discovery at a
// temp directory instead of the real /tmp — see
// internal/tui/devices_test.go's overrideSymlinkDirsForTest. Production
// code always goes through this indirection.
var buildVirtualChooserFunc = newVirtualChooser

func newVirtualChooser(m *model) *virtualChooserState {
	return newVirtualChooserWithDirs(m, device.FriendlySymlinkDirs())
}

func newVirtualChooserWithDirs(m *model, symlinkDirs []string) *virtualChooserState {
	cands := device.BuildVirtualCandidates(m.devices, m.recent, symlinkDirs)
	v := &virtualChooserState{}
	v.rows, v.selectable = buildVirtualRows(cands)
	return v
}

// buildVirtualRows groups candidates under section headers, in the order
// a user should see them: live friendly symlinks first (the exact socat
// workflow), then path-only saved profiles, then recent history — each
// followed by a divider and the always-present custom-path fallback.
func buildVirtualRows(cands []device.Candidate) (rows []virtualRow, selectable []int) {
	var symlinks, saved, recents []device.Candidate
	for _, c := range cands {
		switch c.Source {
		case device.CandidateSymlink:
			symlinks = append(symlinks, c)
		case device.CandidateSavedProfile:
			saved = append(saved, c)
		case device.CandidateRecent:
			recents = append(recents, c)
		}
	}

	addGroup := func(label string, group []device.Candidate) {
		if len(group) == 0 {
			return
		}
		rows = append(rows, virtualRow{header: label})
		for i := range group {
			rows = append(rows, virtualRow{cand: &group[i]})
		}
	}
	addGroup("Friendly symlinks", symlinks)
	addGroup("Saved (path-only)", saved)
	addGroup("Recently used", recents)

	if len(rows) > 0 {
		rows = append(rows, virtualRow{header: "─"})
	}
	rows = append(rows, virtualRow{isEntry: true})

	for i, r := range rows {
		if r.cand != nil || r.isEntry {
			selectable = append(selectable, i)
		}
	}
	return rows, selectable
}

func (m *model) devVirtualHandleKeyIfEditing(msg tea.KeyMsg) (tea.Cmd, bool) {
	v := m.devVirtual
	if v == nil {
		return nil, false
	}
	switch msg.Type {
	case tea.KeyEsc:
		m.devVirtual = nil
		return nil, true
	case tea.KeyUp:
		if v.pos > 0 {
			v.pos--
		}
		return nil, true
	case tea.KeyDown:
		if v.pos < len(v.selectable)-1 {
			v.pos++
		}
		return nil, true
	case tea.KeyEnter:
		return m.submitVirtualChooser(), true
	}
	switch msg.String() {
	case "k":
		if v.pos > 0 {
			v.pos--
		}
	case "j":
		if v.pos < len(v.selectable)-1 {
			v.pos++
		}
	case "x":
		m.removeSelectedRecent()
	case "r":
		pos := v.pos
		*v = *buildVirtualChooserFunc(m)
		v.pos = clampInt(pos, 0, len(v.selectable)-1)
	}
	return nil, true // modal: swallow every other key while the chooser is open
}

func clampInt(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (m *model) submitVirtualChooser() tea.Cmd {
	v := m.devVirtual
	if v == nil || len(v.selectable) == 0 {
		return nil
	}
	row := v.rows[v.selectable[v.pos]]
	if row.isEntry {
		m.devVirtual = nil
		m.devManual = newManualConnectForm()
		return nil
	}
	if row.cand == nil {
		return nil
	}
	return m.connectVirtualCandidate(*row.cand)
}

// connectVirtualCandidate connects to c using the same serial-setting
// precedence as every other connect path (device.ResolveSerialConfig) —
// a saved-profile candidate resolves through device.Resolve exactly like
// the Saved Profiles list does; a symlink/recent candidate connects
// directly by path with no profile (app-config/built-in tiers only). Never
// auto-connects anything the user didn't explicitly select (product spec
// §9) — this is only ever called from an explicit Enter on a chooser row.
func (m *model) connectVirtualCandidate(c device.Candidate) tea.Cmd {
	m.devVirtual = nil
	if c.Source == device.CandidateSavedProfile {
		p, ok := m.devices.Get(c.ProfileAlias)
		if !ok {
			m.status = "profile no longer exists"
			return nil
		}
		info, err := device.Resolve(p, m.detected)
		if err != nil {
			m.status = err.Error()
			return nil
		}
		m.touchRecent(info.Path)
		return m.connect(info.Path, device.ResolveSerialConfig(m.app, &p, nil), m.activeSchema)
	}
	m.touchRecent(c.Path)
	return m.connect(c.Path, device.ResolveSerialConfig(m.app, nil, nil), m.activeSchema)
}

func (m *model) touchRecent(path string) {
	if m.recent == nil {
		return
	}
	m.recent.Touch(path)
	if err := m.recent.Save(); err != nil {
		m.status = "recent history: " + err.Error()
	}
	m.refreshVirtualCount()
}

// removeSelectedRecent is 'x' on a "Recently used" row — the chooser's
// explicit "forget this one" action (product spec §8: stale history must
// be removable, never silently dropped on its own). A no-op on any other
// row kind.
func (m *model) removeSelectedRecent() {
	v := m.devVirtual
	if v == nil || len(v.selectable) == 0 {
		return
	}
	row := v.rows[v.selectable[v.pos]]
	if row.cand == nil || row.cand.Source != device.CandidateRecent || m.recent == nil {
		return
	}
	m.recent.Remove(row.cand.Path)
	if err := m.recent.Save(); err != nil {
		m.status = "recent history: " + err.Error()
	}
	m.refreshVirtualCount()
	pos := v.pos
	*v = *buildVirtualChooserFunc(m)
	v.pos = clampInt(pos, 0, len(v.selectable)-1)
}

// profileForCandidate resolves a CandidateSavedProfile row back to its
// Profile, for showing its own settings in the chooser's effective-config
// preview — nil for every other candidate kind.
func (m *model) profileForCandidate(c device.Candidate) *device.Profile {
	if c.Source != device.CandidateSavedProfile {
		return nil
	}
	p, ok := m.devices.Get(c.ProfileAlias)
	if !ok {
		return nil
	}
	return &p
}

func (m *model) viewVirtualChooser() string {
	v := m.devVirtual
	var b strings.Builder
	b.WriteString(sectionStyle.Render("Virtual / Manual endpoints") + "\n\n")

	if len(v.rows) == 1 { // nothing but the custom-path row
		b.WriteString(dimStyle.Render("No virtual/manual endpoints found.") + "\n")
		b.WriteString(dimStyle.Render("Start a virtual PTY pair (see README.md \"Manual / virtual serial paths\")") + "\n")
		b.WriteString(dimStyle.Render("or enter a custom path below.") + "\n\n")
	}

	curRow := -1
	if len(v.selectable) > 0 {
		curRow = v.selectable[v.pos]
	}
	for i, row := range v.rows {
		switch {
		case row.header == "─":
			b.WriteString(dimStyle.Render(strings.Repeat("─", 44)) + "\n")
		case row.header != "":
			b.WriteString(sectionStyle.Render(row.header) + "\n")
		case row.isEntry:
			marker := "  "
			if i == curRow {
				marker = keyStyle.Render("▸ ")
			}
			b.WriteString(marker + keyStyle.Render("Enter custom path...") + "\n")
		case row.cand != nil:
			b.WriteString(m.renderVirtualCandidateRow(*row.cand, i == curRow) + "\n")
		}
	}

	b.WriteString("\n" + dimStyle.Render("↑/↓ navigate   enter select   x remove (recent)   r rescan   esc back"))
	return accentBox.Render(b.String())
}

// truncatePathKeepingTail shortens a long path from the front, preserving
// the end — a path's basename (e.g. "serialforge-a") is what a user
// actually recognizes it by, so dropping the front ("…/serialforge-a")
// keeps a long path useful where dropping the back
// ("/var/folders/.../serialforge-a"[:N]) would cut off exactly the
// identifying part. Rune-aware (a byte-index truncation on a path with
// multi-byte characters could corrupt a rune, the same class of bug
// diagram.go's centerText has to avoid).
func truncatePathKeepingTail(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return "…" + string(r[len(r)-(max-1):])
}

func (m *model) renderVirtualCandidateRow(c device.Candidate, selected bool) string {
	marker := "  "
	style := fieldTextStyle
	if selected {
		marker = keyStyle.Render("▸ ")
		style = keyStyle
	}
	line := marker + style.Render(truncatePathKeepingTail(c.Label, 40))

	var meta []string
	switch {
	case !c.Available:
		meta = append(meta, badStyle.Render("unavailable"))
	case c.Target != "":
		meta = append(meta, dimStyle.Render("→ "+c.Target))
	}
	if c.Available {
		cfg := device.ResolveSerialConfig(m.app, m.profileForCandidate(c), nil)
		meta = append(meta, dimStyle.Render(fmt.Sprintf("%d %s", cfg.Baud, cfg.FrameString())))
	}
	if m.connectedPath != "" && m.connectedPath == c.Path {
		meta = append(meta, okStyle.Render("connected"))
	}
	if len(meta) > 0 {
		line += "   " + strings.Join(meta, "   ")
	}
	return line
}

// --- Save as profile (product spec §7) --------------------------------------

// saveProfileForm is the one-field prompt shown after connecting to a
// manual/virtual endpoint, offering to persist it as a named alias.
type saveProfileForm struct {
	alias string
}

func (m *model) devSaveHandleKeyIfEditing(msg tea.KeyMsg) (tea.Cmd, bool) {
	f := m.devSave
	if f == nil {
		return nil, false
	}
	switch msg.Type {
	case tea.KeyEsc:
		m.devSave = nil
		return nil, true
	case tea.KeyBackspace:
		if len(f.alias) > 0 {
			f.alias = f.alias[:len(f.alias)-1]
		}
		return nil, true
	case tea.KeyEnter:
		return m.submitSaveProfile(), true
	default:
		if msg.Type == tea.KeyRunes {
			f.alias += string(msg.Runes)
		}
		return nil, true
	}
}

func (m *model) submitSaveProfile() tea.Cmd {
	f := m.devSave
	alias := strings.TrimSpace(f.alias)
	if alias == "" {
		m.status = "enter an alias"
		return nil
	}
	if m.connectedPath == "" {
		m.status = "not connected"
		m.devSave = nil
		return nil
	}
	p := device.Profile{
		Alias:       alias,
		Path:        m.connectedPath,
		Baud:        m.connectedCfg.Baud,
		DataBits:    m.connectedCfg.DataBits,
		Parity:      m.connectedCfg.Parity,
		StopBits:    m.connectedCfg.StopBits,
		FlowControl: m.connectedCfg.FlowControl,
	}
	if err := m.devices.Put(p); err != nil {
		m.status = err.Error()
		return nil
	}
	if err := m.devices.Save(); err != nil {
		m.status = "save: " + err.Error()
		return nil
	}
	m.status = "saved profile " + alias
	m.devSave = nil
	m.refreshVirtualCount()
	return nil
}

func (m *model) viewSaveProfileForm() string {
	f := m.devSave
	body := fmt.Sprintf("%s\n\n  Alias  %s\n\n%s",
		sectionStyle.Render("Save as profile"),
		keyStyle.Render(f.alias)+"█",
		dimStyle.Render(fmt.Sprintf("path: %s\nsettings: %d %s\n\nenter confirm   esc cancel",
			m.connectedPath, m.connectedCfg.Baud, m.connectedCfg.FrameString())))
	return accentBox.Render(body)
}
