package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"

	"github.com/vtemnyakov/serialforge/internal/batch"
)

// The Batch tab (product spec §22/§23). It runs scenarios against whatever
// session is currently connected (set up via the Devices tab) — it does
// not duplicate device resolution here; that already exists once, in
// cmd/serialforge's `batch run` and in Devices' connect flow.
type batchState struct {
	dir       string
	scenarios []string // basenames found under dir
	cursor    int
	running   bool
	report    *batch.Report
	live      []batch.StepResult
	message   string

	pathInput bool
	pathBuf   string
}

func newBatchState() batchState { return batchState{} }

// refreshScenarios scans <configDir>/batch/*.yaml — the natural place to
// keep reusable scenarios, alongside examples/batch in the repo for
// reference. Missing directory is not an error, just an empty list.
func (m *model) refreshBatchScenarios() {
	dir := filepath.Join(m.cfg.ConfigDir, "batch")
	m.batch.dir = dir
	entries, err := os.ReadDir(dir)
	if err != nil {
		m.batch.scenarios = nil
		return
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && (strings.HasSuffix(e.Name(), ".yaml") || strings.HasSuffix(e.Name(), ".yml")) {
			names = append(names, e.Name())
		}
	}
	m.batch.scenarios = names
}

func (m *model) updateBatch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	b := &m.batch
	if b.pathInput {
		switch msg.Type {
		case tea.KeyEsc:
			b.pathInput = false
		case tea.KeyEnter:
			b.pathInput = false
			return m, m.startBatch(strings.TrimSpace(b.pathBuf))
		case tea.KeyBackspace:
			if len(b.pathBuf) > 0 {
				b.pathBuf = b.pathBuf[:len(b.pathBuf)-1]
			}
		case tea.KeyRunes:
			b.pathBuf += string(msg.Runes)
		}
		return m, nil
	}

	switch msg.String() {
	case "r":
		m.refreshBatchScenarios()
	case "p":
		b.pathInput = true
		b.pathBuf = ""
	case "up", "k":
		if b.cursor > 0 {
			b.cursor--
		}
	case "down", "j":
		if b.cursor < len(b.scenarios)-1 {
			b.cursor++
		}
	case "enter":
		if b.running {
			return m, nil
		}
		if b.cursor < len(b.scenarios) {
			return m, m.startBatch(filepath.Join(b.dir, b.scenarios[b.cursor]))
		}
	}
	return m, nil
}

// startBatch loads the scenario, requires an already-connected session
// (reframing it to the scenario's protocol if one is set), and runs it in
// a goroutine — batch.Run blocks on real I/O timeouts, which must not
// block Bubble Tea's Update loop.
func (m *model) startBatch(path string) tea.Cmd {
	b := &m.batch
	b.message = ""
	b.report = nil
	b.live = nil

	data, err := os.ReadFile(path)
	if err != nil {
		b.message = err.Error()
		return nil
	}
	var scenario batch.Scenario
	if err := yaml.Unmarshal(data, &scenario); err != nil {
		b.message = "parse: " + err.Error()
		return nil
	}

	var schema = m.activeSchema
	if scenario.Protocol != "" {
		if sc, ok := m.cfg.Protocols.Get(scenario.Protocol); ok {
			schema = &sc
		} else {
			b.message = fmt.Sprintf("no protocol profile named %q", scenario.Protocol)
			return nil
		}
	}
	if m.sess == nil {
		b.message = "not connected — connect via Devices first"
		return nil
	}
	// Routes through the one centralized protocol-activation path (see
	// model.activateProtocol) rather than calling connect() directly:
	// gets the same-protocol no-op check, correct Logs wording ("session
	// reframed", not a fabricated "Connected ..."), and — the actual bug
	// this replaced — a properly propagated reconnect tea.Cmd instead of
	// one silently discarded by the bare `m.connect(...)` statement this
	// used to be.
	var reframeCmd tea.Cmd
	if schema != nil {
		reframeCmd = m.activateProtocol(schema)
	}

	sess := m.sess
	program := m.program
	b.running = true

	go func() {
		report := batch.Run(context.Background(), sess, schema, scenario, func(r batch.StepResult) {
			if program != nil {
				program.Send(batchStepMsg(r))
			}
		})
		if program != nil {
			program.Send(batchDoneMsg(report))
		}
	}()
	return reframeCmd
}

func (m *model) viewBatch() string {
	b := &m.batch
	if b.pathInput {
		return accentBox.Render(sectionStyle.Render("Scenario path") + "\n\n  " +
			keyStyle.Render(b.pathBuf) + "█\n\n" + renderHints(hint("enter", "run"), hint("esc", "cancel")))
	}

	var out strings.Builder
	out.WriteString(sectionStyle.Render("Scenarios") + dimStyle.Render("  ("+b.dir+")") + "\n")
	if len(b.scenarios) == 0 {
		out.WriteString(dimStyle.Render("  (none found — 'r' rescan, 'p' run by path, see examples/batch/)") + "\n")
	}
	for i, name := range b.scenarios {
		marker := "  "
		if i == b.cursor {
			marker = keyStyle.Render("▸ ")
		}
		out.WriteString(marker + name + "\n")
	}
	out.WriteString("\n" + renderHints(hint("enter", "run"), hint("p", "run by path"), hint("r", "rescan")) + "\n\n")

	if b.running || len(b.live) > 0 {
		out.WriteString(sectionStyle.Render("Progress") + "\n")
		for _, r := range b.live {
			mark := okStyle.Render(glyphOK)
			if r.Status == batch.StatusFail {
				mark = badStyle.Render(glyphBad)
			}
			line := fmt.Sprintf("%s %-40s %6s", mark, r.Label, r.Duration.Round(1e6))
			if r.Message != "" {
				line += "  " + dimStyle.Render(r.Message)
			}
			out.WriteString(line + "\n")
		}
		if b.running {
			out.WriteString(dimStyle.Render(glyphDot+" running…") + "\n")
		}
	}
	if b.report != nil {
		status := okStyle.Render("PASS")
		if !b.report.Pass {
			status = badStyle.Render("FAIL")
		}
		out.WriteString(fmt.Sprintf("\n%s   %d/%d steps   %s\n", status, len(b.report.Steps), len(b.report.Scenario.Steps), b.report.Elapsed.Round(1e6)))
	}
	if b.message != "" {
		out.WriteString("\n" + badStyle.Render(b.message))
	}
	return out.String()
}
