// Command serialforge is the packet-aware serial engineering tool's single
// entrypoint: a headless CLI/automation surface (this file's dispatch) and
// the interactive TUI (internal/tui — bare `serialforge` or `serialforge tui`).
//
// See ARCHITECTURE.md for architecture and internal/*'s doc comments for
// what each package actually owns; this file is deliberately thin — it
// parses arguments and calls into internal/* the same way the TUI does,
// never duplicating logic that belongs there.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// Version is the tool's version string. Bump it as part of any commit that
// changes user-visible behavior — there is no build-time ldflags injection
// yet (see ARCHITECTURE.md "Remaining work"), so this is the only source of
// truth for `serialforge version`.
const Version = "0.1.0-dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// globalFlags are recognized anywhere in the argument list (before or
// interleaved with the subcommand and its own flags).
type globalFlags struct {
	configPath string
	json       bool
}

func parseGlobalFlags(args []string) (globalFlags, []string, error) {
	var g globalFlags
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--config":
			if i+1 >= len(args) {
				return g, nil, errors.New("--config needs a path")
			}
			i++
			g.configPath = args[i]
		case strings.HasPrefix(a, "--config="):
			g.configPath = strings.TrimPrefix(a, "--config=")
		case a == "--json":
			g.json = true
		default:
			rest = append(rest, a)
		}
	}
	return g, rest, nil
}

func run(args []string) error {
	g, args, err := parseGlobalFlags(args)
	if err != nil {
		return err
	}

	cmd := "tui" // bare `serialforge` launches the TUI
	if len(args) > 0 {
		cmd = args[0]
		args = args[1:]
	}

	switch cmd {
	case "version":
		fmt.Println("serialforge " + Version)
		return nil

	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil

	case "config":
		return cmdConfig(g, args)

	case "ports":
		return cmdPorts(g, args)

	case "device":
		return cmdDevice(g, args)

	case "protocol":
		return cmdProtocol(g, args)

	case "packet":
		return cmdPacket(g, args)

	case "batch":
		return cmdBatch(g, args)

	case "monitor":
		return cmdMonitor(g, args)

	case "send":
		return cmdSend(g, args)

	case "tui":
		return cmdTUI(g, args)

	default:
		return fmt.Errorf("unknown command %q — run `serialforge help`", cmd)
	}
}

const usage = `serialforge — packet-aware serial engineering environment

Usage (canonical form — named flags, any order):
  serialforge                                        open the interactive TUI
  serialforge tui                                     same as above, explicitly
  serialforge version                                 print the version
  serialforge config path                             print the resolved config directory
  serialforge ports [--detailed]                       list serial ports (--json for machine output)
  serialforge device list|show|add|delete|rename|clone <args>
  serialforge protocol list|show|import|export|delete <args>
  serialforge packet build --protocol NAME --field NAME=HEX ...
  serialforge packet decode --protocol NAME --hex "AA 55 ..."
  serialforge batch run <scenario.yaml> --protocol NAME --device ALIAS
  serialforge monitor --port <path> [--baud N] [--hex|--ascii|--both]
  serialforge send --port <path> --hex|--text <payload> [--baud N]

--port (or --path, an alias) is a saved alias (see 'device add') or a
literal OS serial path — a real port (/dev/ttyUSB0, COM3) or a manual/
virtual one (a socat PTY link, a device the platform's enumerator doesn't
recognize). No --baud is required: the effective baud/data-bits/parity/
stop-bits follow explicit override > saved device profile > app config
default > built-in default (115200 8N1 none) — see 'serialforge monitor
--help' for the full precedence rule. Flags may be given in any order;
run 'serialforge <command> --help' for each command's exact flags,
defaults, and examples (monitor/send/packet/batch/device support --help).

Positional shorthand (secondary, for convenience — see --help per command
for exactly how it combines with flags):
  serialforge monitor /tmp/serialforge-a --hex
  serialforge send /tmp/serialforge-a "AA 55" --hex

Global flags:
  --config <path>   use this config directory instead of the platform default
  --json            machine-readable output where supported

See README.md and ARCHITECTURE.md for the full design.
`
