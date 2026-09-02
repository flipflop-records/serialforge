package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vtemnyakov/serialforge/internal/batch"
	"github.com/vtemnyakov/serialforge/internal/config"
	"github.com/vtemnyakov/serialforge/internal/debuglog"
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
	closeDebugLog := debuglog.Init() // opt-in via SERIALFORGE_DEBUG_LOG — see internal/debuglog
	defer closeDebugLog()
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

	// --- Logs tab ---
	appLog []AppLogEntry // application/session event journal — see applog.go
	logs   logsState

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
	m.logs = newLogsState()
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
		oldW, oldH := m.width, m.height
		oldSidebar := m.monitorSidebarVisible()
		m.width, m.height = msg.Width, msg.Height
		newSidebar := m.monitorSidebarVisible()
		if oldW != m.width || oldH != m.height {
			debuglog.Event("resize", "old_w", oldW, "old_h", oldH, "new_w", m.width, "new_h", m.height,
				"sidebar_before", oldSidebar, "sidebar_after", newSidebar,
				"ratio", m.effectiveMonitorSplitRatio(), "sidebar_w", m.monitorSidebarWidth())
		}
		return m, nil

	case sessionEventMsg:
		e := session.Event(msg)
		// TX events are recorded synchronously by sendTX the moment
		// Send() succeeds (see savedpackets.go/txrx.go) — immune to
		// whether this async pump is currently even running (that pump
		// dying after a reconnect is exactly what made Monitor stop
		// showing TX in the first place; see ARCHITECTURE.md "TX/RX
		// Monitor event recording"). Re-appending it here would double
		// it, so RX/Status are the only kinds this path still owns.
		if e.Kind != session.EventTX {
			m.appendEvent(e)
		}
		if e.Kind == session.EventStatus {
			m.logEvent(sessionStatusLogLevel(e.Status), "%s", sessionStatusLogMessage(e))
		}
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
	if e.Kind == session.EventRX {
		debuglog.Event("rx", "endpoint", m.connectedPath, "len", len(e.Data), "hex", e.Data, "protocol", schemaLogName(m.activeSchema))
	}
	if !m.paused {
		m.events = append(m.events, eventLogEntry{event: e, device: m.connectedPath})
		if len(m.events) > maxEventLog {
			m.events = m.events[len(m.events)-maxEventLog:]
		}
	}

	// The RX Inspector decodes every RX frame against the active schema as
	// it arrives, independent of pause (pausing only freezes the raw
	// Monitor/Logs view) — see product spec §16/§17. packet.Decode never
	// errors on a checksum mismatch (that's what pkt.CRC.Valid is for —
	// see decode.go): the err path here is schema/structural (should not
	// happen against a live, framer-sized frame), so a corrupted-CRC
	// packet reaches history and Inspector exactly like a valid one,
	// showing FAIL rather than being silently dropped — confirmed live,
	// see this session's final report.
	if e.Kind == session.EventRX && m.activeSchema != nil {
		pkt, err := packet.Decode(*m.activeSchema, e.Data)
		if err == nil {
			pkt.Timestamp = e.Timestamp
			m.rx.history = append(m.rx.history, pkt)
			if trimmed := len(m.rx.history) - maxRXHistory; trimmed > 0 {
				m.rx.history = m.rx.history[trimmed:]
				m.rx.cursor -= trimmed // keep pointing at the same logical packet, not silently shifted
			}
			// Only follow the newest packet automatically if the user
			// hasn't manually browsed away from it — see rxState's own
			// doc comment (updateRX) for why this distinction exists.
			if m.rx.followLatest {
				m.rx.cursor = len(m.rx.history) - 1
			} else if m.rx.cursor < 0 {
				m.rx.cursor = 0
			}
		}
	}
}

// handleKey is the TUI's one key-priority model, in this fixed, documented
// order (see ARCHITECTURE.md "Key routing priority"):
//  1. a genuinely open text-entry/modal editor owns the key completely
//     (the 8 handleKeyIfEditing intercepts below);
//  2. otherwise, HARD GLOBAL CONTROLS always win — quit (q/ctrl+c) and
//     top-level tab navigation (Tab/Shift+Tab/1-6) can never be shadowed
//     by any screen or pane's own local state, Monitor's included;
//  3. then global Saved Packet hotkeys;
//  4. then screen/pane-local navigation (per-tab dispatch, including
//     Monitor's own traffic/sidebar focus and resize keys).
//
// This order used to put Saved Packet hotkeys ahead of quit/tab (harmless,
// since hotkeys are drawn from a palette disjoint from every global key —
// see keybindings.go), but hard global controls are promoted to their own
// explicit first-among-navigation-mode step here so that invariant is
// visible in the code, not just true by construction of the palette.
func (m *model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Text-entry modes intercept keys first — each screen's own handler
	// decides whether it's currently capturing input.
	if cmd, handled := m.designer.handleKeyIfEditing(m, msg); handled {
		debuglog.Event("key", "key", key, "tab", tabNames[m.tab], "route", "designer_editing")
		return m, cmd
	}
	if cmd, handled := m.tx.handleKeyIfEditing(m, msg); handled {
		debuglog.Event("key", "key", key, "tab", tabNames[m.tab], "route", "tx_editing", "tx_mode", int(m.tx.mode), "save_form_open", m.tx.saveForm != nil)
		return m, cmd
	}
	if cmd, handled := m.saved.handleKeyIfEditing(m, msg); handled {
		debuglog.Event("key", "key", key, "tab", tabNames[m.tab], "route", "saved_editing", "saved_mode", int(m.saved.mode))
		return m, cmd
	}
	if cmd, handled := m.devAddHandleKeyIfEditing(msg); handled {
		debuglog.Event("key", "key", key, "tab", tabNames[m.tab], "route", "dev_add_editing")
		return m, cmd
	}
	if cmd, handled := m.devManualHandleKeyIfEditing(msg); handled {
		debuglog.Event("key", "key", key, "tab", tabNames[m.tab], "route", "dev_manual_editing")
		return m, cmd
	}
	if cmd, handled := m.devVirtualHandleKeyIfEditing(msg); handled {
		debuglog.Event("key", "key", key, "tab", tabNames[m.tab], "route", "dev_virtual_editing")
		return m, cmd
	}
	if cmd, handled := m.devSaveHandleKeyIfEditing(msg); handled {
		debuglog.Event("key", "key", key, "tab", tabNames[m.tab], "route", "dev_save_editing")
		return m, cmd
	}
	if cmd, handled := m.sd.handleKeyIfEditing(m, msg); handled {
		debuglog.Event("key", "key", key, "tab", tabNames[m.tab], "route", "serial_defaults_editing")
		return m, cmd
	}

	// Hard global controls — quit and top-level tab navigation — always
	// win once no editor above claimed the key. This is intentionally
	// ABOVE Saved Packet hotkeys and ABOVE every per-tab/pane dispatch
	// (Monitor's focus/resize handling included): no screen-local state
	// may ever shadow these. See this function's own doc comment and
	// ARCHITECTURE.md "Key routing priority".
	switch key {
	case "ctrl+c", "q":
		debuglog.Event("key", "key", key, "tab", tabNames[m.tab], "pane", monitorPaneName(m.monitorFocus), "route", "global_quit")
		m.quit = true
		if m.sess != nil {
			m.sess.Close()
		}
		return m, tea.Quit
	case "tab":
		debuglog.Event("key", "key", key, "tab", tabNames[m.tab], "route", "global_tab_next")
		m.tab = (m.tab + 1) % tabCount
		return m, nil
	case "shift+tab":
		debuglog.Event("key", "key", key, "tab", tabNames[m.tab], "route", "global_tab_prev")
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

	// Saved Packet hotkeys fire here: after every sub-form/picker above has
	// had first refusal (so a hotkey never fires while typing into a field,
	// a protocol name, a path, or any modal — product requirement) and
	// after hard global controls, but before per-screen navigation — see
	// keybindings.go for why this is safe to do globally (the hotkey
	// palette is disjoint from every key used above).
	if cmd, handled := m.trySavedPacketHotkey(msg); handled {
		debuglog.Event("key", "key", key, "tab", tabNames[m.tab], "route", "saved_hotkey")
		return m, cmd
	}

	debuglog.Event("key", "key", key, "tab", tabNames[m.tab], "pane", monitorPaneName(m.monitorFocus), "route", "screen_dispatch")
	switch m.tab {
	case tabMonitor:
		return m.updateMonitor(msg)
	case tabPackets:
		return m.updatePackets(msg)
	case tabDevices:
		return m.updateDevices(msg)
	case tabBatch:
		return m.updateBatch(msg)
	case tabLogs:
		return m.updateLogs(msg)
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

// serialOpenFunc is serial.Open, indirected so tests can substitute a fake
// port instead of touching real hardware. connect() — and, through it,
// activateProtocol's reconnect-to-reframe path — would otherwise be
// untestable without a real serial device. Production code always goes
// through this var unchanged; only tests ever reassign it. Mirrors the
// existing buildVirtualChooserFunc indirection in virtualchooser.go.
//
// connect() below reads this exactly once, into a local, at the top of the
// call — never from inside the session's Opener closure directly. A
// session's own background reconnect logic (internal/session.Session) can
// invoke Opener asynchronously, on its own goroutine, at any later time
// completely outside this call's control; if that closure captured
// serialOpenFunc itself (the package var), a test reassigning it afterward
// — e.g. a later test's own setup, or this test's t.Cleanup restoring the
// original — would race against that still-running goroutine reading it.
// Snapshotting once per connect() call gives every session's Opener its
// own fixed, private reference, immune to any later reassignment of the
// package var — a real, `go test -race`-caught bug during this change, not
// a hypothetical.
var serialOpenFunc = serial.Open

// connectReason distinguishes a genuinely new connection (a different
// endpoint the user explicitly chose — Devices/virtual chooser/saved
// profile) from an internal reframe (activateProtocol/startBatch
// reconnecting the SAME endpoint only to rebuild the framer for a new
// schema) — connect() itself always tears down and reopens the port
// either way (that's the one existing reframe mechanism, reused rather
// than duplicated — see activateProtocol's doc comment), but the two mean
// something different to the user reading Logs: connectReasonNew logs
// "Connected <path> ..."; connectReasonReframe logs nothing extra here
// (activateProtocol already journals the protocol transition itself, and
// "Connected ..." again would misleadingly read as if the physical device
// had been manually reconnected — task's own explicit callout).
type connectReason int

const (
	connectReasonNew connectReason = iota
	connectReasonReframe
)

// connect opens path/cfg, replacing any existing session, and frames RX
// using schema's TotalSize if given (fixed framing) or raw bytes otherwise.
func (m *model) connect(path string, cfg serial.Config, schema *packet.Schema, reason connectReason) tea.Cmd {
	debuglog.Event("connect", "path", path, "schema", schemaLogName(schema), "prev_connected", m.sess != nil)
	m.disconnect()
	open := serialOpenFunc // snapshot — see its own doc comment for why

	var framer framing.Framer
	var err error
	if schema != nil {
		framer, err = framing.New(framing.KindFixed, framing.Options{Size: schema.TotalSize})
	} else {
		framer, err = framing.New(framing.KindRaw, framing.Options{})
	}
	if err != nil {
		m.status = err.Error()
		m.logEvent(LogError, "Connect %s: %s", path, err.Error())
		return nil
	}

	port, err := open(path, cfg)
	if err != nil {
		m.status = "connect: " + err.Error()
		m.logEvent(LogError, "Connect %s failed: %s", path, err.Error())
		return nil
	}
	sess := session.New(session.Config{
		Port:      port,
		Framer:    framer,
		Reconnect: session.DefaultReconnectPolicy(),
		Opener:    func() (serial.Port, error) { return open(path, cfg) },
	})
	ctx, cancel := context.WithCancel(context.Background())
	sess.Start(ctx)

	m.sess = sess
	m.sessCancel = cancel
	m.connectedPath = path
	m.connectedCfg = cfg
	m.activeSchema = schema
	m.status = fmt.Sprintf("Connected %s @ %d %s", path, cfg.Baud, cfg.FrameString())
	if reason == connectReasonNew {
		m.logEvent(LogInfo, "Connected %s @ %d %s", path, cfg.Baud, cfg.FrameString())
	}
	debuglog.Event("connect", "path", path, "schema", schemaLogName(schema), "result", "ok")
	return m.listenSession()
}

func (m *model) disconnect() {
	if m.sess != nil {
		debuglog.Event("disconnect", "path", m.connectedPath)
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

// errNotConnected mirrors internal/session's own "no active session"
// error text, for sendTX's own no-session guard.
var errNotConnected = fmt.Errorf("not connected")

// sendTX is the ONE path every interactive TUI send action goes through —
// TX Builder, and (via sendSavedPacket in savedpackets.go) Saved Packet
// hotkey send, Saved Packets direct send, and the Monitor sidebar's own
// Enter-to-send. It writes to the active session and, on success, records
// a Monitor TX event synchronously — immediately, in this same call,
// rather than waiting for the session's own async Events() channel to be
// drained by whatever tea.Cmd chain happens to be running.
//
// That "rather than" is deliberate, not stylistic: internal/session.Send
// already emits an EventTX on success (session.go), and prior to this fix
// that WAS the TUI's only path to Monitor showing a TX row — entirely
// dependent on listenSession's tea.Cmd chain staying alive. A protocol
// switch's reconnect (model.connect) returns a fresh listenSession() cmd
// that a caller discarding it (as every call site used to) would silently
// stop that chain forever after the first reconnect — Monitor would then
// never show another TX *or* RX event, not just the one that triggered
// it. Recording TX synchronously here makes TX visibility immune to that
// class of bug entirely, independent of whether the async pump is
// currently healthy; Update()'s sessionEventMsg handler explicitly skips
// EventTX (see its own comment) so this can never double-count. RX/status
// events have no such synchronous alternative (they originate from the
// session's own background read loop, not a call the TUI makes), so they
// still rely on the pump — which this fix also makes reconnect far less
// often, and always propagates its Cmd (see activateProtocol) — see
// ARCHITECTURE.md "TX/RX Monitor event recording".
//
// source is a short label for the debug log ("hotkey", "direct_send",
// "tx_builder") — see the "tx"/"TX" log line shape documented alongside
// debuglog. name is the Saved Packet's display name for hotkey/direct-send
// callers, or "" for TX Builder (which has no saved name unless the
// current packet happens to be loaded from one — kept simple, "TX Builder"
// is label enough) — used only for sendTX's own Logs journal entry
// (txLogLabel), not for anything Monitor-visible.
func (m *model) sendTX(data []byte, source, name string) (int, error) {
	label := txLogLabel(source, name)
	if m.sess == nil {
		debuglog.Event("tx", "source", source, "endpoint", m.connectedPath, "len", len(data), "hex", data, "result", "error: not connected")
		m.logEvent(LogWarn, "%s: not connected", label)
		return 0, errNotConnected
	}
	n, err := m.sess.Send(data)
	if err != nil {
		debuglog.Event("tx", "source", source, "endpoint", m.connectedPath, "len", len(data), "hex", data, "result", "error: "+err.Error())
		m.logEvent(LogError, "%s: send failed: %s", label, err.Error())
		return n, err
	}
	sent := append([]byte(nil), data[:n]...)
	debuglog.Event("tx", "source", source, "endpoint", m.connectedPath, "len", n, "hex", sent, "result", "ok")
	m.appendEvent(session.Event{Kind: session.EventTX, Data: sent, Timestamp: time.Now()})
	m.logEvent(LogTX, "%s · %d B", label, n)
	return n, nil
}

// txLogLabel is sendTX's one Logs-entry label, shared by its success and
// failure messages so "Sent Get Status · 14 B" and "Sent Get Status: send
// failed: ..." visibly describe the same action.
func txLogLabel(source, name string) string {
	switch source {
	case "hotkey", "direct_send":
		if name != "" {
			return "Sent " + name
		}
		return "Sent packet"
	case "tx_builder":
		return "TX Builder"
	default:
		return "Sent"
	}
}

// activateProtocol makes sc the TUI's one active protocol context — the
// single path every protocol-context change funnels through: the real
// protocol picker (TX Builder's and RX Inspector's own "o" pickers),
// loading a Saved Packet into TX Builder, and invoking a Saved Packet via
// hotkey or direct send (see sendSavedPacket in savedpackets.go). "Active
// protocol" is more than the visible m.activeSchema pointer: a connected
// session's RX framing (fixed vs. raw, sized from the schema — see
// connect's own doc comment) is fixed at connect time, so keeping
// activeSchema in sync without also reframing the live session would leave
// Monitor/the sidebar agreeing about a protocol the session itself isn't
// actually decoding against.
//
// When connected AND sc actually differs from the currently active schema
// in a way that changes framing (see sameFraming), this reconnects with a
// framer sized for sc and returns connect()'s own tea.Cmd — the caller
// MUST return this Cmd up through model.Update() (never discard it): it
// re-arms the session event pump (listenSession) for the new session, and
// dropping it is what silently stopped Monitor from ever showing another
// TX/RX event after a reconnect (see this session's regression report).
//
// When sc doesn't change framing — most real usage: repeatedly sending
// Saved Packets that already reference the currently active protocol —
// this is a deliberate no-op: no disconnect/reopen/new session, just an
// activeSchema pointer refresh (still needed: sc may carry updated
// Fields/Checksum even with the same Name+TotalSize, e.g. a field relabel
// in Designer). A previous version of this function reconnected
// unconditionally on every call, which is what actually broke Monitor's
// event pump in practice: it fired that discarded-Cmd bug on literally
// every hotkey send. Returns nil (no Cmd needed) for both the no-op and
// not-connected cases.
func (m *model) activateProtocol(sc *packet.Schema) tea.Cmd {
	from, to := schemaLogName(m.activeSchema), schemaLogName(sc)
	if m.sess == nil {
		debuglog.Event("protocol", "from", from, "to", to, "connected", false, "action", "pointer_only")
		if from != to {
			m.logEvent(LogInfo, "%s", protocolLogMessage(from, to, false))
			m.resetRXHistory()
		}
		m.activeSchema = sc
		return nil
	}
	if sameFraming(m.activeSchema, sc) {
		debuglog.Event("protocol", "from", from, "to", to, "connected", true, "action", "noop_same_protocol")
		m.activeSchema = sc
		return nil
	}
	debuglog.Event("protocol", "from", from, "to", to, "connected", true, "action", "reconnect")
	m.logEvent(LogInfo, "%s", protocolLogMessage(from, to, true))
	m.resetRXHistory()
	return m.connect(m.connectedPath, m.connectedCfg, sc, connectReasonReframe)
}

// resetRXHistory clears the RX Inspector's history — called by
// activateProtocol whenever the active protocol genuinely changes (never
// on the same-protocol no-op branch). Every entry in m.rx.history was
// decoded (packet.Decode) against whatever schema was active *at the time
// it arrived* — appendEvent never re-decodes retroactively. Leaving old
// entries in place across a protocol switch, as a previous version of this
// function did, meant viewRX would go on rendering them through
// RenderDiagram(*m.activeSchema, ...) using the *new* schema's field
// layout: a real, confirmed bug (see this session's final report) where a
// stale 4-byte packet decoded under one protocol was shown labeled with a
// different, unrelated protocol's 14-byte field layout — empty field
// cells (its values were keyed by the old schema's field names, which
// don't exist in the new one), the *old* packet's raw bytes displayed
// under the *new* protocol's name, and a CRC PASS/FAIL computed against
// the old schema's checksum rules presented as if it meant something for
// the new one. Starting the Inspector fresh on a real protocol switch
// mirrors the same "no active-context is worth showing what belongs to a
// different context" principle already applied to the Monitor Saved
// Packets sidebar's own protocol-scoped filtering.
func (m *model) resetRXHistory() {
	m.rx.history = nil
	m.rx.cursor = 0
	m.rx.followLatest = true
}

// protocolLogMessage renders activateProtocol's Logs entry. Matches the
// task's own two example phrasings (first activation vs. a real switch);
// when from==to but a reframe still happened (the protocol itself was
// edited — same Name, different TotalSize — see sameFraming), neither
// phrasing would read sensibly ("switched: X -> X"), so that gets its own
// wording. reframed appends a short, honest note distinguishing this from
// a real physical reconnect — task's own explicit callout: don't pretend
// a session reframe is a manual device reconnect.
func protocolLogMessage(from, to string, reframed bool) string {
	var msg string
	switch {
	case from == "none":
		msg = "Protocol activated: " + to
	case from == to:
		msg = "Protocol " + to + " updated"
	default:
		msg = "Protocol switched: " + from + " → " + to
	}
	if reframed {
		msg += " (session reframed)"
	}
	return msg
}

// sameFraming reports whether a and b would produce the same
// framing.Framer (see connect: fixed-size from TotalSize, or raw when
// nil) — the only two fields connect's framer construction actually
// reads. Two nils are "same" (no framing change, still raw); one nil and
// one not is always different.
func sameFraming(a, b *packet.Schema) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Name == b.Name && a.TotalSize == b.TotalSize
}

func schemaLogName(sc *packet.Schema) string {
	if sc == nil {
		return "none"
	}
	return sc.Name
}
