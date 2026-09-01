# SerialForge

SerialForge is a cross-platform protocol-aware serial workstation for FPGA, MCU, and embedded
development — a polished interactive serial monitor combined with a visual protocol designer,
packet builder/parser, flexible CRC engine, and a batch test runner, all driven by one reusable
packet-schema description. Single Go binary, no runtime dependency, for macOS/Linux/Windows.

See [`ARCHITECTURE.md`](ARCHITECTURE.md) for architecture and package layout,
[`docs/product.md`](docs/product.md) for the full product specification, and
[`CONTRIBUTING.md`](CONTRIBUTING.md) for building, testing, and development conventions.

## Status

Under active development. The packet engine (schema model, CRC engine, serializer/decoder) and
the CLI automation surface are implemented and tested against a fake serial transport; the TUI is
implemented and covered by automated (headless) tests but **not yet exercised against real
hardware or reviewed by a human in a real terminal session**. See `ARCHITECTURE.md`'s "Known
limitations" and "Remaining work" for the current, honest state.

## Install / build

Requires Go 1.26+.

```sh
go build -o serialforge ./cmd/serialforge
```

## Quick start

```sh
./serialforge                 # opens the interactive TUI
./serialforge ports           # list detected serial ports
./serialforge protocol import examples/protocols/uart-demo.yaml
./serialforge protocol list
```

### The TUI

`serialforge` (or `serialforge tui`) opens six tabs — `Monitor`, `Packets`, `Devices`, `Batch`,
`Logs`, `Config` — switch with `Tab`/`Shift+Tab` or the number keys `1`-`6`. `q` quits.

Start on **Devices** to connect to a port (either a detected one, or a saved alias — see below),
then use **Packets** to build a protocol:

- **Designer** (`[`/`]` to reach it within Packets): set a total packet size, add fields
  (`n`), edit one (`enter`), delete (`x`), duplicate (`d`), reorder (`<`/`>`), enable a checksum
  and pick a preset or define a custom CRC, save it as a named profile (`s`). The register-style
  diagram updates live as you edit.
- **TX Builder**: pick a saved protocol (`o`), fill in each field's value in hex, watch the CRC
  compute live, send it over the active connection (`x`). Override the CRC manually to test a
  device's error handling.
- **RX Inspector**: pick a protocol to decode incoming bytes against; browse the history of
  decoded packets, each shown with its raw bytes, fields, and CRC PASS/FAIL.

### Headless / automation

Everything the TUI does is also available as plain commands with `--json` output, for scripting
or driving from Python/CI. **Named flags are canonical** and work in any order — `--port` (or its
alias `--path`) always means the device, regardless of where it appears:

```sh
serialforge packet build --protocol uart-demo \
  --field header=AA55 --field command=02 --field address=00C017FF \
  --field payload=000000000001

serialforge packet decode --protocol uart-demo --hex "AA 55 02 00 C0 17 FF 00 00 00 00 00 01 47"

serialforge batch run examples/batch/uart-demo-smoke.yaml --protocol uart-demo --device dev-board

serialforge monitor --port /tmp/serialforge-a --hex
serialforge monitor --baud 921600 --port /tmp/serialforge-a --hex   # same flag, different order — identical result

serialforge send --port /tmp/serialforge-a --hex "AA 55 02 00 C0 17 FF 00 80"
serialforge send --hex "AA 55 02 00 C0 17 FF 00 80" --port /tmp/serialforge-a   # order-independent, same bytes
```

`--baud` is never required — see "Serial settings" below for the default. A single non-flag
argument is accepted as positional shorthand for `--port` (`serialforge monitor fpga --hex`,
`serialforge send fpga "hello"`), but it's secondary: giving both a positional device *and*
`--port`/`--path` at the same time is a conflict error, not a silent guess. Run `serialforge
<command> --help` for each command's exact flags, defaults, and examples — `monitor`, `send`,
`packet`, `batch`, and `device` all support it. Run `serialforge help` for the full command list.

### Serial settings

No flag is required to connect — `--baud`/data bits/parity/stop bits all resolve through one
precedence chain, used identically by the CLI, the TUI's manual connect, and saved device
profiles:

1. an explicit override (`--baud 921600` on the command, or the equivalent TUI field)
2. the saved device profile's own setting, if one was used
3. the application config default (`serial:` in `app.yaml`)
4. the built-in default — **115200 8N1, no flow control**

so `serialforge monitor --port /tmp/serialforge-a --hex` works with no `--baud` at all, and the
connection banner always shows what was actually used: `Connected /tmp/serialforge-a @ 115200 8N1`.

### Device profiles

Give a physical device a stable name instead of typing its OS path every time:

```sh
serialforge device add --alias fpga --vid 0403 --pid 6010 --baud 115200
serialforge monitor --port fpga
```

The profile is matched against connected ports by USB identity (VID/PID/serial number), so it
still resolves correctly if the OS assigns a different `/dev/cu.*` or `COM` path next time.

#### Manual / virtual serial paths (no hardware attached)

Automatic port discovery deliberately doesn't scan every OS pseudo-terminal (on macOS that would
include ordinary terminal sessions, not just serial devices) — but SerialForge still discovers
*virtual and manual* endpoints on their own track, separate from physical hardware:

```sh
# One-off, no saved profile:
serialforge monitor --port /tmp/serialforge-a --baud 115200

# Saved as a reusable alias — VID/PID are optional when you give a path:
serialforge device add --alias virtual --path /tmp/serialforge-a --baud 115200
serialforge monitor virtual
```

In the TUI, **Devices → `m`** opens the **Virtual / Manual endpoints** picker: any friendly
symlink SerialForge finds (e.g. a `socat`-created `/tmp/serialforge-*` link), your recently used
manual paths, and any saved path-only profiles all show up there, grouped by source, with the
effective serial settings shown inline — pick one with `↑`/`↓` + `Enter` and it connects
immediately, no path to remember or type. Typing a path is still there as **"Enter custom
path..."**, the last row in the picker, for anything the picker doesn't already know about.

To try this yourself with a virtual serial pair instead of real hardware:

```sh
brew install socat   # macOS; most Linux distros ship it in their package manager

socat -d -d pty,raw,echo=0,link=/tmp/serialforge-a pty,raw,echo=0,link=/tmp/serialforge-b
```

That creates two linked PTYs — `/tmp/serialforge-a` and `/tmp/serialforge-b` — anything written to
one appears on the other, standing in for a serial cable. Launch SerialForge's TUI, open
**Devices → `m`**, and `/tmp/serialforge-a` appears under "Friendly symlinks" ready to select. See
`scripts/pty-dev-test.sh` for a ready-to-run scripted example and `ARCHITECTURE.md`'s "Virtual /
manual endpoint discovery" section for how the picker's discovery works.

### Protocol profiles

A protocol profile is one packet schema — total size, fields (name/size/endianness/display
format), and an optional checksum — saved under a name and reused everywhere: the designer, the
TX builder, RX decoding, and batch scenarios. See `examples/protocols/uart-demo.yaml` for a worked
example and `internal/packet`'s doc comments for the exact model.

```sh
serialforge protocol import examples/protocols/uart-demo.yaml
serialforge protocol export uart-demo > my-copy.yaml
serialforge protocol clone uart-demo uart-demo-v2
```

### Batch scenarios

A scenario is a sequence of steps — send a packet, wait for a reply, assert a field or the CRC —
run against a live connection. See `examples/batch/uart-demo-smoke.yaml` and `internal/batch`'s
doc comments for the full step vocabulary. `serialforge batch run` exits non-zero on failure, so
it composes with CI.

## Configuration

Config lives under the platform's standard config directory — device profiles, protocol profiles,
and UI preferences, each in its own YAML file:

| Platform | Path |
|---|---|
| macOS | `~/Library/Application Support/SerialForge` |
| Linux | `~/.config/serialforge` |
| Windows | `%AppData%\SerialForge` |

Override the location with `--config <path>` or the `SERIALFORGE_CONFIG_DIR` environment
variable.

## Development

```sh
go build ./...          # compile everything
go test ./...            # unit tests — no hardware required (see internal/serial.FakePort)
go test ./... -race       # concurrency check
go vet ./...
gofmt -l .
```

See [`CONTRIBUTING.md`](CONTRIBUTING.md) for the full development workflow (including testing
against a real PTY pair) and [`ARCHITECTURE.md`](ARCHITECTURE.md) for package boundaries,
architectural invariants, and what's implemented versus planned.
