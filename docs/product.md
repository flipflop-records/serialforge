# Product specification

This document describes what SerialForge is for and the requirements it is designed against. For
current implementation status (what's built, tested, or still planned), see
[`ARCHITECTURE.md`](../ARCHITECTURE.md); for usage, see [`README.md`](../README.md).

## Product identity

SerialForge is a packet-aware serial engineering environment for FPGA, MCU, and embedded systems
work: custom binary protocols, hardware bring-up, debugging, automated testing, and protocol
experimentation. It is deliberately not "just another serial monitor." It combines:

1. a polished interactive serial monitor,
2. a visual packet/protocol designer,
3. a packet builder and parser,
4. flexible CRC/checksum support,
5. batch/test execution,
6. machine-friendly automation.

The core abstraction is not merely `serial port → bytes`. It is a full pipeline:

```
physical serial transport
        ↓
     session
        ↓
   byte stream
        ↓
 framing / packets
        ↓
 protocol schema
        ↓
decoded packet fields
```

A user describes what their binary packet looks like once, and that same description drives TX
packet construction, RX packet decoding, live visualization, CRC calculation and validation, batch
testing, logging, automation, and saved protocol profiles. There is exactly one reusable
packet-schema model — never separate, incompatible packet definitions for different parts of the
program.

The product's guiding philosophy: SerialForge should feel like "a protocol-aware serial
workstation for FPGA and embedded engineers," not "a terminal window that happens to support
hexadecimal output." Its distinguishing strength is the transition between raw bytes and
structured protocol without hiding either one — raw stream, packet, fields, and interpreted
values are all visible, and the engineer can always see exactly what bytes are transmitted or
received. The tool should never introduce "smart" behavior that prevents the user from seeing or
controlling the actual bytes.

## Target platforms

Single executable, no runtime dependency (no Node.js, Python, JVM, browser). Genuinely
cross-platform:

- macOS (Intel and Apple Silicon)
- Linux (x86-64 and ARM64)
- Windows (x86-64)

The TUI is the primary interactive interface, with a full headless CLI for scripting and CI. Style
goal for the TUI: a polished, restrained terminal UI — clean borders, strong visual hierarchy,
tasteful colors, keyboard-first interaction, clear tabs/screens, useful status information,
minimal clutter, coherent spacing, good empty/error/loading states — one consistent visual
language across the entire application. No screen should look like it belongs to a different
program.

## Core architecture requirements

- Transport and protocol logic must stay separated: `serial transport → session → packet engine`,
  with the TUI, CLI, and any automation API all built as consumers on top of that stack.
- TUI code must not directly manipulate OS serial handles.
- CRC implementation must not live inside a TUI widget.
- Batch execution must not duplicate the packet serializer.
- RX parsing and TX construction must use the same packet schema.
- Packet visualization must be a representation of the underlying schema, never a second source of
  truth.

## Serial engine

A proper cross-platform serial abstraction, at minimum:

- port enumeration, with metadata where available (VID, PID, USB serial number, product,
  manufacturer, friendly name)
- open, close, reconnect
- configurable baud, data bits, stop bits, parity, flow control
- read/write timeouts where appropriate, asynchronous read/write
- graceful handling of cable removal
- meaningful errors

Common baud presets (9600, 19200, 38400, 57600, 115200, 230400, 460800, 921600) should be readily
available, but arbitrary values must remain supported. Raw bytes must be preserved internally —
the serial engine must never assume UTF-8 or any other text encoding.

## Device profiles

Persistent device aliases, e.g. an alias `fpga` bound to a VID/PID and default baud, so a user can
run `monitor fpga` instead of remembering an OS-specific path. Matching may use VID/PID, USB
serial number, manufacturer, product, or a literal path. If the OS-assigned path changes (a
different `/dev/cu.*` or `COM` port) while the device's identifying metadata stays the same, the
profile must still resolve correctly. If multiple connected devices match ambiguously, the tool
must never guess — that's a reportable error, not a silent pick.

Manual/virtual endpoints (a `socat`-created PTY pair, an adapter the platform doesn't recognize)
must be fully usable via an explicit path, without requiring VID/PID identity, and without the
tool ever broadening its automatic hardware discovery to cover every OS-level pseudo-terminal —
that would make ordinary terminal sessions look like serial devices. See ARCHITECTURE.md's
"Manual serial paths" and "Virtual / manual endpoint discovery" for how this is actually
implemented.

## Packet / protocol designer

This is a core feature, not an afterthought. The user must be able to visually define a packet's
structure: a total size, and an ordered list of named, sized fields (optionally with a CRC at the
end). The application must visualize this structure immediately and update the visualization live
as the schema is edited. The editor must prevent impossible layouts — the sum of all fields
(including any CRC) must exactly equal the configured total packet size before a schema is
considered valid. Useful editing operations: add, delete, rename, resize, reorder, duplicate.

## Packet visualization

A reusable, register-style packet diagram — not a progress bar. Think of the bit-field/register
diagrams used in FPGA/CPU/peripheral datasheets: a row of labeled cells sized proportionally to
each field's byte width, showing field name, byte size, byte offset/range, and (where a packet
instance exists) the current value. For packets too wide to render in one row at the current
terminal width, degrade gracefully — multiple rows, compact representations — rather than making
the UI unusable or unreadable. This diagram is a single reusable component used everywhere a
packet's shape needs to be shown: the protocol designer, the TX builder, the RX decoder, batch
execution, and a packet inspector. There must never be a second, competing implementation of this
visualization.

## Field properties

Fields start byte-sized, but the internal model must have a clean evolution path toward more
sophisticated definitions (sub-byte/bit-level fields, for example) without a painful rewrite.
Field properties: name, description, length, offset (always derived, never stored redundantly),
endianness, and a display format — hex, unsigned integer, signed integer, ASCII, raw bytes, or
enum.

## CRC / checksum engine

CRC support is a first-class feature, not an add-on.

- The schema editor must offer "enable checksum," after which CRC becomes a visible field at the
  end of the packet, occupying space *inside* the configured total packet size (a 14-byte packet
  with an 8-bit CRC enabled has 13 bytes available for other fields, not 14 fields plus 1 CRC
  byte). The visualization must update immediately when CRC is enabled, disabled, or resized.
- Built-in presets for widely used algorithms — CRC-8, CRC-8/MAXIM-DOW, CRC-8/SAE-J1850,
  CRC-16/ARC, CRC-16/MODBUS, CRC-16/CCITT-FALSE, CRC-16/XMODEM, CRC-32/ISO-HDLC, CRC-32C
  (Castagnoli), and other standard variants — verified against trustworthy reference parameters
  and validated with official/known check vectors, not implemented from the algorithm's name
  alone.
- A fully custom CRC definition must also be supported, exposing width, polynomial, initial value,
  RefIn, RefOut, and XorOut clearly enough that an embedded engineer can compare the values
  directly against a datasheet. Custom definitions must be heavily unit-tested.
- CRC width is represented explicitly in bits; the engine must support at least 8/16/32/64-bit
  algorithms and must not assume everything is CRC-8. Non-byte-aligned widths, if ever supported
  at the packet-storage level, must have an explicit, non-ambiguous packing rule rather than
  silently invented behavior.
- Default CRC coverage is "every packet byte before the CRC field." The model should allow this to
  become configurable (a byte range, or selected fields only) without a breaking redesign.

## Packet TX builder

Once a schema exists, values are entered field-by-field (not as a hand-assembled hex string). The
complete packet, and its CRC, are shown and recalculated live as fields change. CRC defaults to
AUTO; an advanced user must be able to override it manually for negative/fault-injection testing,
with the override clearly and unambiguously indicated as intentional, distinct from a normal
computed value.

## Packet RX decoder

The same schema is usable in reverse: incoming bytes matching the configured framing/length are
decoded into named fields, not just shown as a raw hex dump. A decoded packet should show its
received CRC, the recalculated CRC, and a clear PASS/FAIL — checksum failures must be very easy to
notice without compromising the overall visual polish. The original raw bytes must always remain
inspectable regardless of how the decoded view is presented.

## Packet inspector

A focused detail view for any RX or TX packet: timestamp, direction, total size, raw bytes, the
packet diagram, individual fields with offsets and interpreted values, CRC configuration, received
CRC, calculated CRC, and CRC status.

## Protocol / packet profiles

Users can save, edit, clone, rename, delete, import, and export named packet schemas ("protocol
profiles") for reuse across the designer, TX builder, RX decoding, and batch scenarios.

## Interactive serial monitor

A conventional raw serial monitor remains useful and is kept as one mode of the application (not
the whole product): ASCII, hex, and combined views, timestamps and timestamp deltas, RX/TX
distinction, pause/clear, and raw binary-safe capture. The product differentiates itself through
the structured packet tooling layered on top, not by replacing this mode.

## Receive framing

Multiple framing strategies — raw stream, line-based, fixed packet size, delimiter-based — with
fixed-size framing integrating naturally with packet schemas. The framing abstraction should leave
room for future strategies (COBS, SLIP, HDLC-like framing, sync/header detection, length fields,
custom parsers) without requiring a large plugin framework up front.

## Normal send modes

Besides structured packet TX, simple sending remains available: text (with configurable line
ending: none/LF/CR/CRLF) and hex (accepting common hex input styles). Send history and reusable
commands/macros are useful additions.

## Batch mode

A first-class feature, not an afterthought: scripted hardware tests expressed as a sequence of
steps (open/send/wait/decode/verify field/verify CRC/pass-fail), which is substantially more
useful for real test automation than matching raw strings. A batch run should produce a clear
pass/fail report per step, and the same register-style packet diagram (never a second
implementation) should be usable to show the current packet in a live batch view.

## Automation / API

Everything meaningful in the product must also be automatable: listing devices, selecting a
protocol, connecting, sending raw bytes or a structured packet, receiving and decoding packets,
retrieving CRC results, and running batch scenarios — all with machine-friendly (JSON) output
where appropriate, so the tool is easy to drive from Python or any other external tooling without
embedding Go. A local daemon/API (HTTP, JSON-RPC, or a Unix/Windows local socket) is a reasonable
future extension for lower-latency automation than shelling out to the CLI per call, but must
never expose a public network listener by default.

## Logging / capture

Application logs, raw serial capture, structured packet history, and batch results are distinct
concerns and should be separable. Packet history should preserve exact raw bytes, timestamp,
direction, the protocol/schema used, decoded values, and the full CRC result (calculated value,
received value, pass/fail) — not just a formatted string.

## Configuration

Proper platform configuration directories, overridable via `--config`. Persisted: device profiles,
serial defaults, protocol definitions, CRC presets/custom definitions, saved TX packets, saved
commands, UI preferences, and reconnect policy. Writes should be atomic where practical.

## TUI structure

A small number of top-level tabs (a likely shape: Monitor, Packets, Devices, Batch, Logs, Config),
with **Packets** providing contextual subviews (Protocol Designer, TX Builder, RX Inspector)
rather than an explosion of top-level tabs.

## First-run experience

Running the tool with no configuration should still be understandable: select a serial device,
choose baud/settings, connect, and use the raw Monitor immediately. From there, the UI should
naturally lead toward creating a protocol (name, total packet size, optional checksum) and seeing
the packet diagram appear immediately — creating a basic packet layout should take seconds, not
require hand-editing YAML.

## Performance

Serial monitoring may run for hours. The tool must not retain infinite rendered history, rebuild
huge strings every TUI frame, block serial reads on rendering, or let packet history consume
unbounded RAM. Bounded buffers and efficient models are required; streaming a raw capture to disk
is an acceptable way to keep an unbounded record without keeping it all in memory.

## Concurrency

Serial RX/TX, reconnect, decoding, packet history, the TUI, and batch execution must coexist
cleanly: proper context/cancellation use, clear ownership, bounded channels where appropriate, and
clean shutdown. The serial reader must never block because the TUI (or anything downstream) is
slow to keep up.

## Testability

A fake/in-memory serial transport must exist so hardware is not required for most automated
tests. Required coverage areas: packet layout (fields exactly fill/exceed the packet, remaining
bytes, reorder, CRC reservation), CRC correctness (presets, custom parameters, reflection,
different widths, incorrect-CRC detection) against canonical check vectors, the serializer and
decoder (both directions, byte-exact), a full round trip (fields → serialize → append CRC →
receive → decode → validate original fields and CRC), and batch execution (send, simulated
response, field assertion, CRC assertion, both pass and failure cases).

## Cross-platform discipline

Development may begin on one platform, but compilation must be continuously verified for
darwin/amd64, darwin/arm64, linux/amd64, linux/arm64, and windows/amd64. CGO should be avoided
where reasonably possible, and unavoidable platform-specific code should be isolated behind clear
boundaries. Common/shared code must never assume a Unix-style `/dev/...` path exists.

## Distribution

Target standalone binaries for macOS (Intel and Apple Silicon), Linux (x86-64 and ARM64), and
Windows (x86-64), with a CI/release pipeline (GitHub Actions + GoReleaser or an equivalent).

## UX standard

Functionality is not "done" just because it technically works. A protocol editor that only prints
plain field/size text pairs is not acceptable — the structured packet representation is one of the
defining visual elements of the product, and its appearance deserves real iteration. The packet
visualization should feel close to a live, interactive version of the register/bit-field diagrams
embedded engineers already work with in FPGA/MCU documentation, built with a proper TUI toolkit
rather than ad hoc string formatting.
