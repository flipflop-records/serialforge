package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vtemnyakov/serialforge/internal/batch"
	"github.com/vtemnyakov/serialforge/internal/config"
	"github.com/vtemnyakov/serialforge/internal/device"
	"github.com/vtemnyakov/serialforge/internal/framing"
	"github.com/vtemnyakov/serialforge/internal/packet"
	"github.com/vtemnyakov/serialforge/internal/protocol"
	"github.com/vtemnyakov/serialforge/internal/savedpacket"
	"github.com/vtemnyakov/serialforge/internal/serial"
	"github.com/vtemnyakov/serialforge/internal/session"
)

// Tabs — the top-level navigation (product spec §28). Packets carries its
// own contextual subviews (Designer/TX Builder/RX Inspector) rather than
// becoming three more top-level tabs.
const (
	tabMonitor = iota
	tabPackets
	tabDevices
	tabBatch
	tabLogs
	tabConfig
	tabCount
)

var tabNames = []string{"Monitor", "Packets", "Devices", "Batch", "Logs", "Config"}

const (
	packetsDesigner = iota
	packetsTX
	packetsRX
	packetsSaved
	packetsViewCount
)

var packetsViewNames = []string{"Designer", "TX Builder", "RX Inspector", "Saved"}

// RunConfig is everything Run needs to start the TUI — the stores and app
// config cmd/serialforge has already loaded, so this package never touches
// config-directory resolution itself.
type RunConfig struct {
	ConfigDir    string
	App          config.App
	Devices      *device.Store
	Protocols    *protocol.Store
	SavedPackets *savedpacket.Store
	Recent       *device.RecentStore
	Version      string
}

// Run starts the Bubble Tea program and blocks until the user quits.
//
// Zero devices, zero saved profiles, and no selected device are all normal
// starting states — see newModel/model.viewDevices/model.viewMonitor: the
// TUI must open into a polished empty state in every one of those cases
// (product spec, "serial hardware must not be required simply to explore
// the application"). The one thing that legitimately prevents Run from
// opening anything is having no real terminal to draw into at all — see
// friendlyStartError.
func Run(cfg RunConfig) error {
	m := newModel(cfg)
	p := tea.NewProgram(m, tea.WithAltScreen())
	m.program = p // lets background work (batch runs) push messages in — see batchview.go
	_, err := p.Run()
	if err != nil {
		return friendlyStartError(err)
	}
	return nil
}

// friendlyStartError recognizes Bubble Tea's own "no terminal available"
// failure — os.Stdin isn't a TTY and /dev/tty (its fallback, for piped-input
// cases) can't be opened either, e.g. no controlling terminal at all — and
// turns Bubble Tea's low-level wrapped os.Open error into an explanation an
// engineer can act on, instead of a bare "device not configured"/"not a
// tty" that reads like a silent failure. This is unrelated to device/USB
// state: it fires before the TUI ever constructs a frame, regardless of how
// many serial ports or profiles exist — see Run's doc comment and
// ARCHITECTURE.md's "TUI startup" section for how this was diagnosed.
func friendlyStartError(err error) error {
	if strings.Contains(err.Error(), "could not open a new TTY") {
		return fmt.Errorf("serialforge: no interactive terminal is available to open the TUI (%w) — "+
			"run this in a real terminal window, or use a headless command instead, e.g. "+
			"`serialforge ports`, `serialforge monitor <device>`, `serialforge batch run ...`", err)
	}
	return err
}

// eventLogEntry is one line in the shared Monitor/Logs history — a session
// Event plus which device it came from, so Logs can show connection
// lifecycle across reconnects while Monitor shows the live byte stream.
type eventLogEntry struct {
	event  session.Event
	device string
}

const maxEventLog = 2000 // bounded per product spec §30 — never unbounded history

type model struct {
	cfg     RunConfig
	program *tea.Program
	width   int
	height  int
	tab     int
	quit    bool
	status  string // transient one-line status message shown in the footer area

	// --- connection, shared across Monitor/Packets(TX,RX)/Batch ---
	sess          *session.Session
	sessCancel    context.CancelFunc
	connectedPath string
	connectedCfg  serial.Config
	activeSchema  *packet.Schema // schema used to frame+decode RX; nil = raw byte stream
	events        []eventLogEntry
	paused        bool
	monitorMode   string // "hex" | "ascii" | "both"

	// --- Monitor tab: Saved Packets sidebar (wide terminals only) ---
	monitorFocus monitorPane
	monitorSaved monitorSidebarState

	// --- Devices tab ---
	devices      *device.Store
	recent       *device.RecentStore
	detected     []serial.PortInfo
	devCursor    int
	devDetectErr string
	devAdd       *addDeviceForm
	devManual    *manualConnectForm
	devVirtual   *virtualChooserState
	devSave      *saveProfileForm
	virtualCount int

	// --- Packets tab ---
	packetsView int
	designer    designerState
	tx          txState
	rx          rxState
	saved       savedState

	// --- Batch tab ---
	batch batchState

	// --- Config tab ---
	app        config.App
	cfgSection int
	sd         serialDefaultsState
}

func newModel(cfg RunConfig) *model {
	if cfg.SavedPackets == nil {
		// Defensive: every real construction path (cmd/serialforge's `tui`
		// command) always loads a real store, but a nil *savedpacket.Store
		// must never reach the point of being dereferenced — see
		// trySavedPacketHotkey/viewSaved, which run on every keypress/every
		// Packets/Saved render.
		cfg.SavedPackets = &savedpacket.Store{}
	}
	m := &model{
		cfg:         cfg,
		devices:     cfg.Devices,
		recent:      cfg.Recent,
		app:         cfg.App,
		monitorMode: cfg.App.UI.MonitorMode,
	}
	if m.monitorMode == "" {
		m.monitorMode = "both"
	}
	m.designer = newDesignerState()
	m.tx = newTXState()
	m.rx = newRXState()
	m.saved = newSavedState()
	m.batch = newBatchState()
	m.sd = newSerialDefaultsState(cfg.App)
	m.refreshDetected()
	m.refreshBatchScenarios()
	return m
}

func (m *model) Init() tea.Cmd { return nil }

// --- messages -------------------------------------------------------------

type sessionEventMsg session.Event
type batchStepMsg batch.StepResult
type batchDoneMsg batch.Report
type tickMsg time.Time

// monitorSplitSaveMsg is the debounced-save tick for Monitor's adjustable
// split ratio — see monitorsidebar.go's scheduleMonitorSplitSave.
type monitorSplitSaveMsg struct{ gen int }

func (m *model) listenSession() tea.Cmd {
	if m.sess == nil {
		return nil
	}
	events := m.sess.Events()
	return func() tea.Msg {
		e, ok := <-events
		if !ok {
			return nil
		}
		return sessionEventMsg(e)
	}
}

// --- update -----------------------------------------------------------------

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case sessionEventMsg:
		m.appendEvent(session.Event(msg))
		return m, m.listenSession()

	case batchStepMsg:
		m.batch.live = append(m.batch.live, batch.StepResult(msg))
		return m, nil

	case batchDoneMsg:
		r := batch.Report(msg)
		m.batch.report = &r
		m.batch.running = false
		return m, nil

	case monitorSplitSaveMsg:
		// Only the most recently scheduled tick actually saves — a stale
		// tick from a keypress that's since been superseded by another
		// resize (msg.gen no longer matches) is a no-op, which is what
		// collapses a held resize key into a single write. See
		// scheduleMonitorSplitSave's doc comment.
		if msg.gen == m.monitorSaved.saveGen {
			if err := config.SaveApp(m.cfg.ConfigDir, m.app); err != nil {
				m.status = "save: " + err.Error()
			}
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *model) appendEvent(e session.Event) {
	if !m.paused {
		m.events = append(m.events, eventLogEntry{event: e, device: m.connectedPath})
		if len(m.events) > maxEventLog {
			m.events = m.events[len(m.events)-maxEventLog:]
		}
	}

	// The RX Inspector decodes every RX frame against the active schema as
	// it arrives, independent of pause (pausing only freezes the raw
	// Monitor/Logs view) — see product spec §16/§17.
	if e.Kind == session.EventRX && m.activeSchema != nil {
		pkt, err := packet.Decode(*m.activeSchema, e.Data)
		if err == nil {
			pkt.Timestamp = e.Timestamp
			m.rx.history = append(m.rx.history, pkt)
			if len(m.rx.history) > maxRXHistory {
				m.rx.history = m.rx.history[len(m.rx.history)-maxRXHistory:]
			}
			m.rx.cursor = len(m.rx.history) - 1
		}
	}
}

func (m *model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Text-entry modes intercept keys first — each screen's own handler
	// decides whether it's currently capturing input.
	if cmd, handled := m.designer.handleKeyIfEditing(m, msg); handled {
		return m, cmd
	}
	if cmd, handled := m.tx.handleKeyIfEditing(m, msg); handled {
		return m, cmd
	}
	if cmd, handled := m.saved.handleKeyIfEditing(m, msg); handled {
		return m, cmd
	}
	if cmd, handled := m.devAddHandleKeyIfEditing(msg); handled {
		return m, cmd
	}
	if cmd, handled := m.devManualHandleKeyIfEditing(msg); handled {
		return m, cmd
	}
	if cmd, handled := m.devVirtualHandleKeyIfEditing(msg); handled {
		return m, cmd
	}
	if cmd, handled := m.devSaveHandleKeyIfEditing(msg); handled {
		return m, cmd
	}
	if cmd, handled := m.sd.handleKeyIfEditing(m, msg); handled {
		return m, cmd
	}

	// Saved Packet hotkeys fire here: after every sub-form/picker above has
	// had first refusal (so a hotkey never fires while typing into a field,
	// a protocol name, a path, or any modal — product requirement), but
	// before core navigation, on every tab — "Navigation mode" is simply
	// "no text-entry/modal intercept above claimed this key." See
	// keybindings.go for why this is safe to do globally.
	if cmd, handled := m.trySavedPacketHotkey(msg); handled {
		return m, cmd
	}

	switch msg.String() {
	case "ctrl+c", "q":
		m.quit = true
		if m.sess != nil {
			m.sess.Close()
		}
		return m, tea.Quit
	case "tab":
		// While the Monitor tab's Saved Packets sidebar is actually on
		// screen, Tab/Shift+Tab switch focus between the two Monitor
		// panes instead of cycling top-level tabs — narrower in scope
		// than stealing Tab everywhere: the moment the sidebar isn't
		// visible (a different tab, or a terminal too narrow for it),
		// Tab reverts to its normal global meaning immediately, and the
		// digit jump keys (1-6) always reach every tab regardless, so
		// this never actually strands the user. See monitorsidebar.go.
		if m.tab == tabMonitor && m.monitorSidebarVisible() {
			m.monitorFocus = 1 - m.monitorFocus
			return m, nil
		}
		m.tab = (m.tab + 1) % tabCount
		return m, nil
	case "shift+tab":
		if m.tab == tabMonitor && m.monitorSidebarVisible() {
			m.monitorFocus = 1 - m.monitorFocus
			return m, nil
		}
		m.tab = (m.tab - 1 + tabCount) % tabCount
		return m, nil
	case "1":
		m.tab = tabMonitor
		return m, nil
	case "2":
		m.tab = tabPackets
		return m, nil
	case "3":
		m.tab = tabDevices
		return m, nil
	case "4":
		m.tab = tabBatch
		return m, nil
	case "5":
		m.tab = tabLogs
		return m, nil
	case "6":
		m.tab = tabConfig
		return m, nil
	}

	switch m.tab {
	case tabMonitor:
		return m.updateMonitor(msg)
	case tabPackets:
		return m.updatePackets(msg)
	case tabDevices:
		return m.updateDevices(msg)
	case tabBatch:
		return m.updateBatch(msg)
	case tabConfig:
		return m.updateConfig(msg)
	}
	return m, nil
}

// --- view -------------------------------------------------------------------

func (m *model) View() string {
	if m.quit {
		return ""
	}
	var b strings.Builder
	b.WriteString(m.header())
	b.WriteString("\n\n")
	switch m.tab {
	case tabMonitor:
		b.WriteString(m.viewMonitor())
	case tabPackets:
		b.WriteString(m.viewPackets())
	case tabDevices:
		b.WriteString(m.viewDevices())
	case tabBatch:
		b.WriteString(m.viewBatch())
	case tabLogs:
		b.WriteString(m.viewLogs())
	case tabConfig:
		b.WriteString(m.viewConfig())
	}
	b.WriteString("\n\n")
	b.WriteString(m.footer())
	return b.String()
}

func (m *model) header() string {
	parts := []string{titleStyle.Render("SerialForge"), " "}
	for i, n := range tabNames {
		label := fmt.Sprintf("%d·%s", i+1, n)
		if i == m.tab {
			parts = append(parts, tabActive.Render(label))
		} else {
			parts = append(parts, tabInactive.Render(label))
		}
	}
	conn := dimStyle.Render("not connected")
	if m.connectedPath != "" {
		conn = okStyle.Render(fmt.Sprintf("● %s @%d %s", m.connectedPath, m.connectedCfg.Baud, m.connectedCfg.FrameString()))
	}
	parts = append(parts, "  "+conn)
	return strings.Join(parts, "")
}

func (m *model) footer() string {
	hints := renderHints(hint("tab", "next screen"), hint("1-6", "jump"), hint("q", "quit"))
	if m.status != "" {
		return warnStyle.Render(m.status) + "   " + hints
	}
	return hints
}

func (m *model) diagramWidth() int {
	w := m.width - 4
	if w < 20 {
		w = 20
	}
	return w
}

// refreshDetected re-scans serial ports. Called on startup and whenever the
// Devices screen requests a rescan.
func (m *model) refreshDetected() {
	ports, err := serial.ListDetailed()
	if err != nil {
		m.devDetectErr = err.Error()
		m.detected = nil
	} else {
		m.devDetectErr = ""
		m.detected = ports
	}
	m.refreshVirtualCount()
}

// refreshVirtualCount recomputes the Virtual/manual endpoints count shown
// on the Devices screen. Cached on the model (like m.detected) rather than
// scanned fresh on every View() call — View() must stay cheap and I/O-free
// on the TUI's render path; this only runs at startup, on 'r' rescan, and
// after anything that could change what the chooser would show (a profile
// or recent-history mutation).
func (m *model) refreshVirtualCount() {
	m.virtualCount = len(device.BuildVirtualCandidates(m.devices, m.recent, device.FriendlySymlinkDirs()))
}

// connect opens path/cfg, replacing any existing session, and frames RX
// using schema's TotalSize if given (fixed framing) or raw bytes otherwise.
func (m *model) connect(path string, cfg serial.Config, schema *packet.Schema) tea.Cmd {
	m.disconnect()

	var framer framing.Framer
	var err error
	if schema != nil {
		framer, err = framing.New(framing.KindFixed, framing.Options{Size: schema.TotalSize})
	} else {
		framer, err = framing.New(framing.KindRaw, framing.Options{})
	}
	if err != nil {
		m.status = err.Error()
		return nil
	}

	port, err := serial.Open(path, cfg)
	if err != nil {
		m.status = "connect: " + err.Error()
		return nil
	}
	sess := session.New(session.Config{
		Port:      port,
		Framer:    framer,
		Reconnect: session.DefaultReconnectPolicy(),
		Opener:    func() (serial.Port, error) { return serial.Open(path, cfg) },
	})
	ctx, cancel := context.WithCancel(context.Background())
	sess.Start(ctx)

	m.sess = sess
	m.sessCancel = cancel
	m.connectedPath = path
	m.connectedCfg = cfg
	m.activeSchema = schema
	m.status = fmt.Sprintf("Connected %s @ %d %s", path, cfg.Baud, cfg.FrameString())
	return m.listenSession()
}

func (m *model) disconnect() {
	if m.sess != nil {
		m.sess.Close()
	}
	if m.sessCancel != nil {
		m.sessCancel()
	}
	m.sess = nil
	m.sessCancel = nil
	m.connectedPath = ""
	m.activeSchema = nil
}
