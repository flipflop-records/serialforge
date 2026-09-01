package main

import (
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"

	"github.com/vtemnyakov/serialforge/internal/batch"
	"github.com/vtemnyakov/serialforge/internal/config"
	"github.com/vtemnyakov/serialforge/internal/framing"
	"github.com/vtemnyakov/serialforge/internal/packet"
	"github.com/vtemnyakov/serialforge/internal/protocol"
)

var batchRunDefs = []flagDef{
	{names: []string{"--device", "--port", "--path"}, takesValue: true},
	{names: []string{"--protocol"}, takesValue: true},
	{names: []string{"--baud"}, takesValue: true},
}

const batchHelp = `usage: serialforge batch run <scenario.yaml> [--protocol NAME] [--device ALIAS|PATH] [--baud N]

Run a batch test scenario (send/expect/assert steps) against a live
connection and print PASS/FAIL per step; exits non-zero on failure, so it
composes with CI. --device accepts a saved alias, a literal OS path, or a
manual/virtual path (a socat PTY link) identically; --port/--path are
accepted as aliases for --device.

Flags:
  --protocol <name>        saved protocol profile; falls back to the
                            scenario file's 'protocol:' field
  --device, --port, --path <value>
                            device to connect to; falls back to the
                            scenario file's 'device:' field. Aliases for
                            the same value; conflicting values is an error.
  --baud <n>                overrides the connection's baud rate (same
                            precedence as 'monitor' — see its --help).
  --json                    print the final report as JSON instead of a
                            live per-step summary (global flag)
  --help, -h                 show this help

Examples:
  serialforge batch run examples/batch/uart-demo-smoke.yaml --protocol uart-demo --device dev-board
  serialforge batch run scenario.yaml --port /tmp/serialforge-a
`

func cmdBatch(g globalFlags, args []string) error {
	if wantsHelp(args) {
		fmt.Print(batchHelp)
		return nil
	}
	if len(args) == 0 || args[0] != "run" {
		return fmt.Errorf("usage: serialforge batch run <scenario.yaml> [--protocol NAME] [--device ALIAS] [--baud N]\n\nrun `serialforge batch --help` for details")
	}
	args = args[1:]

	parsed, err := parseArgs(args, batchRunDefs)
	if err != nil {
		return fmt.Errorf("%w\n\n%s", err, batchHelp)
	}
	switch len(parsed.positionals) {
	case 0:
		return fmt.Errorf("no scenario file given — usage: serialforge batch run <scenario.yaml> [flags]")
	case 1:
		// exactly right
	default:
		return fmt.Errorf("too many positional arguments: %v (expected exactly one scenario file)", parsed.positionals)
	}
	scenarioPath := parsed.positionals[0]

	data, err := os.ReadFile(scenarioPath)
	if err != nil {
		return err
	}
	var scenario batch.Scenario
	if err := yaml.Unmarshal(data, &scenario); err != nil {
		return fmt.Errorf("parse %s: %w", scenarioPath, err)
	}

	protocolName, _, err := parsed.single("--protocol", "--protocol")
	if err != nil {
		return err
	}
	if protocolName == "" {
		protocolName = scenario.Protocol
	}
	deviceArg, _, err := parsed.single("--device", "--device/--port/--path")
	if err != nil {
		return err
	}
	if deviceArg == "" {
		deviceArg = scenario.Device
	}
	if deviceArg == "" {
		return fmt.Errorf("no device: pass --device (or --port/--path) or set `device:` in the scenario")
	}

	var schema *packet.Schema
	if protocolName != "" {
		dir, err := config.Dir(g.configPath)
		if err != nil {
			return err
		}
		store, err := protocol.Load(dir)
		if err != nil {
			return err
		}
		sc, ok := store.Get(protocolName)
		if !ok {
			return fmt.Errorf("no protocol profile named %q", protocolName)
		}
		if err := sc.Validate(); err != nil {
			return fmt.Errorf("protocol %q is not valid: %w", protocolName, err)
		}
		schema = &sc
	}

	var overrideBaud *int
	if baudStr, ok, err := parsed.single("--baud", "--baud"); err != nil {
		return err
	} else if ok {
		b, err := strconv.Atoi(baudStr)
		if err != nil {
			return fmt.Errorf("--baud: %w", err)
		}
		overrideBaud = &b
	}
	path, cfg, err := resolveDevice(g, deviceArg, overrideBaud)
	if err != nil {
		return err
	}

	framer, err := mustFramer(schema)
	if err != nil {
		return err
	}

	sess, err := openSession(path, cfg, framer)
	if err != nil {
		return err
	}
	defer sess.Close()

	if !g.json {
		fmt.Printf("Connected %s @ %d %s\n", path, cfg.Baud, cfg.FrameString())
	}

	ctx, cancel := contextWithSignal()
	defer cancel()
	sess.Start(ctx)

	report := batch.Run(ctx, sess, schema, scenario, func(r batch.StepResult) {
		if g.json {
			return // JSON mode prints the final report only, for clean machine parsing
		}
		mark := "✓"
		if r.Status == batch.StatusFail {
			mark = "✗"
		}
		fmt.Printf("%s %-40s %6s  %s\n", mark, r.Label, r.Duration.Round(1e6), r.Message)
	})

	if g.json {
		if err := printJSON(report); err != nil {
			return err
		}
	} else {
		status := "PASS"
		if !report.Pass {
			status = "FAIL"
		}
		fmt.Printf("\n%s   %d/%d steps   %s\n", status, len(report.Steps), len(scenario.Steps), report.Elapsed.Round(1e6))
	}

	if !report.Pass {
		return fmt.Errorf("batch scenario failed")
	}
	return nil
}

// mustFramer picks fixed-size framing matching the resolved schema (so
// expect_packet reads exactly one packet's worth of bytes at a time), or
// raw framing when the scenario has no protocol (e.g. a plain send/expect
// hex-matching test).
func mustFramer(schema *packet.Schema) (framing.Framer, error) {
	if schema == nil {
		return framing.New(framing.KindRaw, framing.Options{})
	}
	return framing.New(framing.KindFixed, framing.Options{Size: schema.TotalSize})
}
