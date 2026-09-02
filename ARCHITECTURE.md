# Architecture

This document describes SerialForge's package layout, architectural invariants, and current
implementation status. See [`README.md`](README.md) for usage and [`docs/product.md`](docs/product.md)
for the product specification this architecture serves. [`CONTRIBUTING.md`](CONTRIBUTING.md) covers
building, testing, and development workflow.

SerialForge is a single-binary Go CLI/TUI serial engineering environment for FPGA/MCU/embedded
bring-up and protocol work: a polished interactive serial monitor plus a **packet-aware** layer
(visual protocol designer, packet builder/parser, CRC engine, batch test runner, automation
surface) built around one reusable packet-schema model. The important abstraction is not just
"serial port → bytes" but a full pipeline: physical serial transport → session → byte stream →
framing/packets → protocol schema → decoded packet fields. The schema is a first-class object
that drives TX construction, RX decoding, live visualization, CRC calculation/validation, batch
testing, and automation — one description, reused everywhere, never duplicated per subsystem.

The TUI follows a restrained, keyboard-first visual style: Bubble Tea + Lip Gloss, rounded-border
boxes, a small shared ANSI-256 color palette, one flat model per screen rather than a nested
per-screen framework, and a consistent visual language across every tab.

## Module layout
- Module root: this directory (`github.com/vtemnyakov/serialforge`, go 1.26). Single Go module,
  no nested sub-module.
- `cmd/serialforge/` — CLI entrypoint + command dispatch (`main.go` + `commands_*.go`, one file per
  command group). Manual flag/subcommand parsing, no CLI framework.
- `internal/checksum/` — parametric CRC engine (Rocksoft model: width/poly/init/refin/refout/
  xorout), a catalogue of 16 built-in presets, and `Definition`, the packet-facing type a protocol
  profile embeds (mode: none/preset/custom + coverage + wire endianness).
- `internal/packet/` — the packet-schema model: `Schema`, `Field`, layout/validation, `Serialize`/
  `Decode`, byte-order-aware value encode/decode helpers. Depends on `checksum` only.
- `internal/serial/` — cross-platform serial transport: `Port` interface, real implementation over
  `go.bug.st/serial`, and `FakePort`/`FakeDevice` (in-memory, `io.Pipe`-based) so nothing above
  this layer needs hardware to test. Port enumeration is a separate concern with a build-tag split
  for darwin — see "Serial engine" below.
- `internal/device/` — device profile model (alias → VID/PID/serial/manufacturer/product/path
  matching, most-specific-match-wins, never guesses among ties) and YAML profile storage.
- `internal/framing/` — receive framing strategies (raw/line/fixed-size/delimiter) as a small
  `Push`/`Next`/`Reset` `Framer` interface, independent of what's inside a frame.
- `internal/session/` — owns one open `serial.Port` + a `Framer`; runs the RX goroutine, emits
  RX/TX/status `Event`s on a bounded channel, reconnects via a caller-supplied `Opener` with
  exponential backoff. Nothing above `session` touches a raw OS handle (hard invariant, see
  below).
- `internal/protocol/` — protocol *profiles*: a named `packet.Schema`, YAML (de)serialization, and
  a profile store (create/get/put/delete/rename/clone/export/import). Deliberately thin — never
  reimplements `Schema.Validate`/`Serialize`/`Decode`.
- `internal/savedpacket/` — reusable, named, optionally hotkey-bound packets: a protocol reference
  (never an embedded schema copy) plus concrete field values and a CRC mode, YAML persistence
  (`saved_packets.yaml`), protocol-evolution validation (`Resolve`), and the one shared build path
  (`Build`) the TUI (TX Builder save/load/update, the Saved Packets screen, hotkeys) and
  `cmd/serialforge saved send` all call — never a second serializer. See "Saved packets" below.
- `internal/config/` — platform config directory resolution (`os.UserConfigDir()/SerialForge` on
  macOS/Windows, `os.UserConfigDir()/serialforge` on Linux, overridable via
  `--config`/`SERIALFORGE_CONFIG_DIR`), atomic file writes (temp + rename), and `app.yaml` (UI
  prefs + reconnect policy).
- `internal/batch/` — batch scenario model (`Scenario`/`Step`, a tagged-union YAML shape) +
  executor (`Run`), built directly on `session.Session` + `packet`/`checksum` — does not
  reimplement serialization/decoding/CRC. Steps: send, send_packet, sleep, expect, expect_hex,
  expect_packet, assert_field, assert_crc, clear_rx, log.
- `internal/tui/` — Bubble Tea application: one flat `model`, the shared style palette, the
  reusable register-style packet diagram widget, and six tabs' worth of screens.
- `internal/capture/` — **planned, not implemented.** On-disk raw capture / structured packet
  history persistence beyond the TUI's in-memory bounded buffers (`model.events`,
  `rxState.history`) doesn't exist yet — see "Logging and captures" / "Remaining work".
- `internal/api/` — **planned, not implemented.** No daemon; the automation surface today is the
  CLI's `--json` output (see "CLI" below) — see "Remaining work".

## Hard architectural invariants
- **TUI code never touches an OS serial handle.** It only holds a `session.Session` (or higher).
- **CRC logic lives only in `internal/checksum`.** No widget computes a checksum itself.
- **`internal/batch` calls `packet.Serialize`/`packet.Build`/`packet.Decode` — it does not
  reimplement them.** Same for `cmd/serialforge`'s `packet build`/`packet decode`/`batch run` commands
  and every TUI screen (Designer/TX/RX/Batch) — one `internal/packet` implementation, many callers.
- **RX and TX share one `packet.Schema`.** There is no separate "decoder schema" vs "builder schema".
- **The packet diagram is a pure function of `packet.Schema.Layout()`.** `internal/tui.RenderDiagram`
  is the only rendering of a packet's byte layout in the codebase; the designer, TX builder, RX
  inspector, and packet inspector all call it with the same `DiagramOptions{Values, CRCResult,
  CRCDisplay, Selected}` shape — never a second, hand-maintained visual model.
- **CRC is packet-final and carved out of `TotalSize`**, never added on top of it. A schema
  with N field bytes + an M-byte CRC in a `TotalSize` packet requires `N+M == TotalSize`.
- **CRC algorithm naming lives only in `checksum.Definition` (`AlgorithmName`/`AlgorithmLabels`).**
  No TUI screen invents its own abbreviation or re-derives a preset's name from `Definition.Preset`
  directly — the designer's field-list summary, the TX Builder's CRC line, and the diagram's CRC
  cell all name the algorithm through these two methods, so a narrow cell picks a shorter
  *catalogued alias* rather than truncating mid-word.
- **PASS/FAIL is an RX-only claim.** It means "a byte that arrived over the wire matches a
  recalculation" (`DiagramOptions{CRCDisplay: CRCDisplayCompare}`, the zero value, used by RX
  Inspector). TX Builder never shows PASS/FAIL — a freshly built packet's CRC agreeing with its own
  arithmetic is not a claim about anything a device confirmed; it uses `CRCDisplayAuto`, which shows
  the actual byte plus whether it was AUTO-computed or manually overridden instead.
- **Hardware discovery and virtual/manual endpoint discovery are separate concerns.**
  Virtual/PTY endpoints must not pollute physical serial-device enumeration.
- **Explicit arbitrary paths must always remain supported as a fallback.**

## Packet schema model (`internal/packet`)
- `Schema{Name, Description, TotalSize, Fields []Field, Checksum checksum.Definition}`. `TotalSize`
  is bytes; every `Field.Size` is bytes (sub-byte/bit-level fields are a documented future
  extension, not implemented — see Known limitations).
- `Field{Name, Description, Size, Endianness, Format, Enum}`. `Format` ∈
  {hex, uint, int, ascii, raw, enum}; raw bytes are always retained regardless of `Format` —
  format only changes *interpretation*, never storage.
- Offsets are always derived (`Schema.FieldOffset`, `Schema.Layout()`), never stored — reordering a
  field is a slice operation, not an offset-rewrite.
- `Schema.Validate()` enforces: `TotalSize > 0`; no duplicate/empty field names; no zero-size
  fields; enum fields declare enum values; CRC width byte-aligned; **`Allocated() == TotalSize`
  exactly** (`Allocated = FieldsSize + CRCSize`). This is what makes "impossible layouts"
  unrepresentable — see `packet_test.go` for the exact cases covered (fields exactly fill,
  exceed, remaining bytes, CRC reservation, reorder).
- `Schema.Layout() []Span` is the single reusable representation: ordered `{Kind, Name, Offset,
  Size, FieldIndex}` spans (field / crc / unallocated-while-editing). Every visual/serialization
  consumer reads this, never a bespoke recomputation.
- `Serialize(schema, Values, *crcOverride) ([]byte, *CRCResult, error)` — `Values` maps field name
  to already-endianness-encoded wire bytes (`EncodeUint`/`EncodeASCII` produce these); every field
  must be present at exactly its declared size — no implicit zero-fill. CRC is AUTO-computed unless
  `crcOverride` is given (fault injection). `CRCResult` then carries two distinct facts a TX display
  must not conflate: `Manual` ("did the sender type this value in" — true whenever `crcOverride` is
  non-nil, even if it happens to equal AUTO's value) and `Overridden` (`Manual && Received !=
  Calculated` — "did the override actually change the byte", the fault-injection case the CLI's
  `packet build` flags). `Build` wraps `Serialize` and immediately `Decode`s the result back into a
  `*Packet` — the shape the TX preview/inspector/batch want.
- `Decode(schema, raw) (*Packet, error)` — `raw` must be exactly `TotalSize` bytes; every field is
  decoded to `FieldValue{Raw, Uint/UintOK, Int/IntOK, ASCII}` (Uint/Int only populated for fields
  ≤ 8 bytes; `Raw` is always authoritative); CRC mismatch is reported via `Packet.CRC`, never
  hidden or turned into a decode error.
- CRC coverage: `checksum.Coverage{Mode: all_before_crc | range, Start, End}`. Only
  `all_before_crc` (every byte before the CRC field — the default and simplest normal behavior)
  is wired into `coverageBytes()` today; `range` is modeled (serializable, part of the stable
  schema) but selective/per-field coverage beyond a byte range is **planned**, not implemented,
  and has no editor UI yet.

## CRC engine (`internal/checksum`)
- `Params{Width, Poly, Init, RefIn, RefOut, XorOut}` — the Rocksoft/reveng-catalogue parameter
  model. `Poly`/`Init`/`XorOut` are always normal (non-reflected) form, matching how datasheets and
  the catalogue publish them, so a saved custom definition can be diffed against a datasheet table
  directly.
- **Engine requires `Width` in `checksum.MinWidth`..`checksum.MaxWidth` (8..64) bits** — exported
  constants, the one source of truth `Params.Validate` enforces and that `internal/tui`'s
  custom-CRC form derives its own input-time Width bound from (see "Bounded input"), rather than a
  duplicated literal. `Compute` XORs each (optionally reflected) input byte into the top
  byte-position of the width-bit register, then shifts 8×, conditionally XORing `Poly` — the
  standard byte-serial bit-by-bit CRC simulation. Widths below `MinWidth` are rejected by
  `Params.Validate` with an explicit error (not silently wrong) — see Known limitations.
- 16 built-in presets in `presets.go` (CRC-8, CRC-8/MAXIM-DOW, CRC-8/SAE-J1850, CRC-8/NRSC-5,
  CRC-16/ARC, CRC-16/MODBUS, CRC-16/CCITT-FALSE, CRC-16/XMODEM, CRC-16/KERMIT, CRC-16/DNP,
  CRC-32/ISO-HDLC, CRC-32C, CRC-32/BZIP2, CRC-32/MPEG-2, CRC-64/XZ, CRC-64/ISO), each carrying the
  catalogue's `Check` value for `"123456789"`. **Every preset is asserted against its Check value
  in `crc_test.go`, and the 32/64-bit presets are additionally cross-validated against Go's own
  `hash/crc32`/`hash/crc64`** (IEEE, Castagnoli, ECMA, ISO tables) so correctness doesn't rest on
  transcribed constants alone — this cross-check is what catches a subtly wrong parameter (a
  transcription error that still passes a superficial smoke test) before it ships.
- `checksum.Definition` is what `packet.Schema.Checksum` actually holds: `Mode` (none/preset/
  custom), `Preset` name or `Custom Params`, `Coverage`, and packing `Endianness` (defaults to
  big-endian; independent of `RefIn`/`RefOut`, which affect the algorithm's internal bit order, not
  how the resulting bytes are laid out on the wire). The designer's CRC picker (`Packets → Designer`,
  `enter` on the CRC row) exposes preset selection and a full custom-parameter form
  (Width/Poly/Init/RefIn/RefOut/XorOut) directly.
- `Definition.AlgorithmLabels() []string` / `AlgorithmName() string` are the single source of CRC
  display naming (see the "CRC algorithm naming" hard invariant above). `AlgorithmLabels` returns
  every candidate name, most descriptive first: a preset's canonical `Name`, then its catalogued
  `Aliases` in declaration order (e.g. `CRC-8/MAXIM-DOW` → `["CRC-8/MAXIM-DOW", "CRC-8/MAXIM",
  "DOW-CRC", "1-WIRE"]`) — real, catalogued names doubling as ready-made abbreviations, not an
  invented truncation. `ModeCustom` synthesizes a `["CUSTOM CRC-N", "CRC-N", "CRC"]` ladder since
  there's no catalogued name to fall back on. `AlgorithmName` is just the first candidate (or the
  bare `"CRC"` fallback). `internal/tui.crcCellLabel` is the one caller that picks a *narrower*
  candidate than the first — the longest one that fits a diagram cell's actual width, never a
  substring/ellipsis cut of the full name unless even the shortest candidate doesn't fit.
- CRC byte packing: `ByteWidth = ceil(Width/8)`; v0.1 requires `Width % 8 == 0` for anything that
  goes into a packet (`Schema.Validate` enforces this even though the bitwise engine itself would
  happily compute a non-byte-aligned CRC standalone) — packing behavior is never left ambiguous.
- Custom CRC parameters can be entered and used per-schema (via the designer's custom-CRC form),
  but there is no separate **named, reusable custom-CRC library** (save once, pick from a list
  across protocols) yet — that's **planned**, not built.

## Serial engine (`internal/serial`)
Library: `go.bug.st/serial` v1.8.0 (pure Go for open/close/read/write/`GetPortsList` on every
platform; `go.bug.st/serial/enumerator`, which adds VID/PID/manufacturer/product/serial-number
metadata, requires cgo **on darwin only** to call IOKit — confirmed by cross-compiling a probe
program: `CGO_ENABLED=0 GOOS=darwin ...` fails to even compile the `enumerator` package, while
`CGO_ENABLED=0` builds for `linux/{amd64,arm64}` and `windows/amd64` succeed with full enumerator
support, and native darwin builds (cgo on by default) succeed with full support).

Resolution, implemented: detailed enumeration is split by build tag —
`enumerate_enumerator.go` (`//go:build !(darwin && !cgo)`) uses the real `enumerator` package
(pure-Go on linux/windows, cgo+IOKit on native darwin); `enumerate_fallback.go`
(`//go:build darwin && !cgo`) degrades to `serial.GetPortsList()` — port paths only, metadata
fields left empty — so a forced `CGO_ENABLED=0` darwin cross-build still compiles and runs
instead of failing outright. `enumerator.GetDetailedPortsList(enumerator.All)` is called with the
"probe every device" filter so `Manufacturer`/`Product` actually populate (they're empty with no
filter — easy to miss in the library's own doc comment). Native macOS dev builds (cgo default-on)
get full VID/PID/serial-number/manufacturer/product enumeration.

`Port` is a minimal `io.ReadWriteCloser` + `SetReadTimeout(ms int64)`. `Open(path, Config)` maps
`Config` (baud/data bits/parity/stop bits/flow control/read timeout) onto `go.bug.st/serial.Mode`;
`FlowRTSCTS` is wired to `SetRTS(true)`, `FlowXonXoff` is accepted by `Config.Validate` for
protocol-profile/UI purposes but has no real implementation yet (the library doesn't model
software flow control as a `Mode` field) — see Known limitations.

`FakePort`/`FakeDevice` (`fake.go`) are a connected `io.Pipe` pair: `FakeDevice.Write` feeds what
`FakePort.Read` sees, `FakePort.Write` shows up on `FakeDevice.Read`. Both directions are
*unbuffered* — a `Write` blocks until a concurrent `Read` on the peer is ready, same as a stalled
real link with no driver buffer. Both directions being unbuffered is deliberate — it surfaced a
real deadlock in an early version of the session test suite before being documented on the type
itself, and it demands a concurrent reader in every TX-direction test; don't "fix" this into
something buffered without re-reading the type's doc comment first.

GoReleaser/CI should build darwin artifacts on a macOS runner (native compile, not a cross-compile
from Linux) to keep full enumeration in release binaries — see `.goreleaser.yaml`'s header comment
for the current (simpler, `CGO_ENABLED=0`-everywhere, single-runner) trade-off actually shipped.

## Session layer (`internal/session`)
`Session` owns one `serial.Port` + one `framing.Framer`. `Start(ctx)` launches the RX goroutine:
read → `Framer.Push` → drain `Framer.Next()` → `EventRX` on a buffered channel (default 256; full
channel drops rather than blocks the reader — the serial reader must never block because the TUI
is slow). `Send` writes and emits `EventTX`. A read error triggers `reconnect`:
if a `ReconnectPolicy` and `Opener` were given, retries with exponential backoff (`EventStatus`
disconnected/reconnecting/reconnected), replacing the port in place on success; otherwise the RX
loop ends and the `Events` channel closes. `Close()` cancels, closes the port, and waits for the
goroutine — no leaked reader blocked on a dead pipe.

## Device profiles (`internal/device`)
`Profile{Alias, VID, PID, SerialNumber, Manufacturer, Product, Path, Baud, DataBits, Parity,
StopBits, FlowControl, DefaultProtocol}`, stored in `devices.yaml`. `Resolve(profile, ports)`:
an exact live `Path` match wins outright; otherwise the highest-*specificity* USB-identity match
(serial number > manufacturer+product > VID+PID; VID/PID/manufacturer/product compared
case/`0x`-prefix-insensitively). Multiple ports tying at the same specificity is an **error**, not
a random pick. `Store` (`store.go`) persists via `internal/config.WriteFileAtomic`.

### Manual serial paths (PTYs, virtual ports, unrecognized adapters)
`internal/serial.Open(path, cfg)` has never consulted enumeration — it opens whatever OS path
it's given, real hardware or a socat PTY link, identically. The gap was one layer up: a
`Profile{Path: "..."}` with no VID/PID (no `hasIdentity()`) whose `Path` doesn't appear in
`serial.ListDetailed()` used to be treated as "not found" and rejected — exactly what happens on
macOS for a manually-created PTY (`/dev/ttys003`, or a `socat ...,link=/tmp/serialforge-a` symlink
to it), since `go.bug.st/serial`'s darwin enumerator only discovers `cu.*`/`tty.*`-named ports by
design (see "Serial engine" above) and was **not** broadened to match every `ttys*` — that would
make ordinary terminal sessions show up as if they were serial hardware. `Resolve` now trusts a
Path-only profile whose path isn't enumerated, returning a `PortInfo{Path: path}` with no USB
metadata (`IsUSB: false`, VID/PID/serial/manufacturer/product all empty) rather than erroring —
see `Resolve`'s doc comment for the exact three-tier priority. USB metadata is never required
alongside a manual path.

Ways to use one, all wired to this same `Resolve` fix:
- **CLI**: `serialforge monitor --port /tmp/serialforge-a --hex` (or `--path`, identical; `--baud`
  is optional — see "Serial setting precedence" under CLI) — parsed by the order-independent
  parser in `argparse.go`, wired into `monitor`/`send`/`batch run`. A bare positional argument that
  isn't a saved alias is also treated as a literal path (documented as secondary shorthand — see
  "CLI" below).
- **Saved profile**: `serialforge device add --alias virtual --path /tmp/serialforge-a --baud
  115200`, then `serialforge monitor --port virtual` — persists the path so it doesn't need
  retyping.
- **TUI, primary path**: Devices tab, `m` — opens the **Virtual / Manual endpoints** chooser
  (below), which surfaces candidates so most sessions never need typing at all.
- **TUI, fallback**: the chooser's trailing **"Enter custom path..."** row opens
  `manualConnectForm` (`internal/tui/devices.go`) — a small Path+Baud form that connects
  immediately, no saved profile required. `a` (save-as-profile) also has a Path field for the
  persistent version of the same thing.

Integration-tested against a real socat PTY pair — see [`CONTRIBUTING.md`](CONTRIBUTING.md)'s
Testing section and `scripts/pty-dev-test.sh` for the reproducible manual workflow.

### Virtual / manual endpoint discovery (`internal/device/virtual.go`, `recent.go`; `internal/tui/virtualchooser.go`)
Typing a raw path is the documented fallback, not the normal workflow — pressing `m` on the
Devices tab opens a chooser (`newVirtualChooser`/`buildVirtualRows`) instead of dropping straight
into a text field, listing candidates grouped by source:

- **Friendly symlinks** (`DiscoverFriendlySymlinks`): a non-recursive scan of `FriendlySymlinkDirs()`
  (`/tmp` plus `os.TempDir()` if different) for symlinks whose *target* looks pty-shaped
  (`LooksLikeSerialDevice`: `tty`/`cu.`-prefixed, or containing `/pts/`) — this is how a
  `socat ...,link=/tmp/serialforge-a` pair shows up unprompted. Deliberately **not** a scan of
  every `/dev/ttys*`/`/dev/pts/*` — see the invariant above and "Auto-discovery must never be
  broadened" below; only symlinks a dev workflow explicitly created are surfaced, never raw PTYs.
- **Recently used** (`RecentStore`, `recent.go`): the last `maxRecentEndpoints` (8) manually-
  connected paths, MRU-ordered, persisted to `<configDir>/recent_endpoints.yaml`. Selecting *any*
  chooser candidate calls `Touch(path)` regardless of source — a connect always updates recency.
  No promotion to a full profile is required for a path to reappear here.
- **Saved (path-only)**: profiles with no VID/PID identity (`!p.hasIdentity()`) — a normal
  `device.Profile` whose only real content is a path, i.e. exactly what "save as profile" (`s`,
  see below) produces for a virtual endpoint.

`BuildVirtualCandidates` merges the three sources and dedups by path (symlink > saved-profile >
recent priority) so the same endpoint never appears twice — a path that is both a live symlink
and a saved profile shows once, under "Friendly symlinks" (the higher-priority source); once the
symlink disappears (e.g. socat stopped) the same path reappears, alone, under "Saved (path-only)"
marked "unavailable". A trailing **"Enter custom path..."** row is always present, even
with zero candidates (empty state: "No virtual/manual endpoints found... Start a virtual PTY pair
or enter a custom path.") — arbitrary paths remain fully supported no matter what.

Selecting a non-fallback row resolves the connection through the same `device.ResolveSerialConfig`
four-tier precedence used everywhere else (no fifth ad hoc tier for "chooser-selected"); effective
settings are computed ahead of time and shown inline on each row (e.g. `115200 8N1`) and again in
the post-connect status banner (`Connected /tmp/serialforge-a @ 115200 8N1`) — baud is never asked
for when the default/profile/config already supplies it. Never auto-connects anything; every
candidate requires an explicit `Enter`. A stale endpoint (its target `os.Stat` fails) is marked
`unavailable` inline rather than hidden or silently dropped from recent history, and reconnects
cleanly if the path reappears (e.g. socat restarted). `refreshVirtualCount` caches the candidate
count on `model` (updated at startup, on `r` rescan, and after any devices/recent mutation) so
`View()` stays I/O-free, matching how `m.detected` is already handled.

`s` on the Devices tab, once connected, opens `saveProfileForm` — alias only, no USB metadata
required — and persists a path-only profile from the live connection's path + settings.

## Protocol profiles (`internal/protocol`)
`Store` persists `packet.Schema` values (name-keyed map) to `protocols.yaml`:
get/put/delete/rename/clone/export(single-schema YAML)/import. `Put` accepts an
in-progress/invalid draft — the designer needs to save work in progress; only actual use
(TX/RX/batch) requires `Validate()`. `examples/protocols/uart-demo.yaml` is a worked example, kept
honest by a test that loads the tracked file itself — deliberately generic (HEADER/COMMAND/
ADDRESS/DATA/RESERVED/CRC), not tied to any specific real hardware project.

## Saved packets (`internal/savedpacket`)
`SavedPacket{Name, Protocol, Values map[string]string, CRCMode, CRCOverride, Hotkey}` — a reusable,
optionally hotkey-bound packet: a protocol *reference by name* (resolved fresh against
`protocol.Store` on every use — **never an embedded schema copy**, so the protocol stays the single
source of truth for field order/size/endianness/CRC algorithm/packet size/serialization) plus
concrete field values (hex strings, the same representation TX Builder edits), a CRC mode
(`auto`/`override`), and an optional single-key hotkey. `Values` being keyed by field name is sound
only because `packet.Schema.Validate` already rejects duplicate field names
(`TestValidateRejectsDuplicateFieldNames`) — see `Resolve` below for how a *stored* schema that
transiently fails that (a draft `protocol.Store.Put` accepted) is still handled safely rather than
assumed valid.

- `Resolve(protocols *protocol.Store) Resolution` re-fetches the schema every call and reports one
  of: `StatusProtocolMissing` (protocol deleted), `StatusProtocolInvalid` (protocol exists but
  itself fails `Schema.Validate` — an explicit, defense-in-depth check before any field-name-keyed
  lookup, not an assumption), `StatusIncompatible` + `[]FieldProblem` (`missing_value` = a schema
  field with no stored value, i.e. added since save; `unknown_field` = a stored value for a field
  the schema no longer has; `size_mismatch` = stored hex length no longer matches the field's
  current size), or `StatusOK`. Never a hard error and never a crash — a broken/stale Saved Packet
  is a diagnosable, displayable state (the Saved Packets screen and CLI `saved show` both render
  it), and TX Builder's Load still loads whatever's usable from a `StatusIncompatible` packet so the
  user can repair it by editing fields (a `StatusProtocolMissing`/`StatusProtocolInvalid` packet
  isn't loaded — that's a Designer-level problem, not a field-editing one).
- `Build(protocols *protocol.Store) (*packet.Packet, error)` resolves, refuses non-OK with a
  specific message, decodes the stored hex into `packet.Values`, and calls `packet.Build` — the
  **same function TX Builder's send and the CLI's `packet build` use**. CRC follows `CRCMode`: Auto
  passes a nil override so `Serialize` always recomputes from the *current* schema/values (never a
  cached CRC byte written at save time); Override parses `CRCOverride` and passes it through,
  preserved exactly — the intentional fault-injection case. `Build` is the **one** call site every
  consumer (`internal/tui`'s TX Builder direct-send, Saved Packets list send, hotkey dispatch, and
  `cmd/serialforge saved send`) goes through — never a second serializer.
- `Store` (`store.go`) persists to `saved_packets.yaml`, slice-based like `internal/device.Store`
  (not map-based like `protocol.Store`) so **file/insertion order is preserved** — the order the
  Saved Packets screen and hotkey-help render in. `Get`/`Put`/`Delete`/`Rename`/`Duplicate` (which
  deliberately does *not* copy the hotkey — two Saved Packets can never share one) /
  `FindByHotkey`/`HotkeyConflict`.

### Hotkey policy (`internal/tui/keybindings.go`)
A Saved Packet hotkey is validated against `hotkeyPalette` — a small, deliberately-curated
**allowlist**, not "whatever key isn't currently used." This is a permanent, load-bearing
invariant: no future core/screen keybinding may ever be added from the palette (punctuation `' . ,
; / - = \` \` plus the handful of letters/digits never bound anywhere else); add new app shortcuts
from outside it instead. `TestPaletteKeysAreNeverConsumedByCoreDispatch`
(`internal/tui/keybindings_test.go`) drives every screen's Navigation-mode dispatch with every
palette key and asserts no state changes — the mechanical enforcement that makes this "automatic,"
not a hand-maintained list a future PR could silently invalidate.
`ValidateHotkeyAssignment(key, saved, excludeName)` is the one function every assignment path (TX
Builder's Save form, the Saved Packets screen's `h`) calls: empty (unbind) is always fine;
otherwise the key must be in the palette (a rejected key outside it gets a specific
`reservedKeyLabels` explanation, e.g. `key "q" is reserved for Quit`, when one's on file) and must
not already be bound to a *different* Saved Packet (`hotkey "'" is already used by "..."`).
`trySavedPacketHotkey` (`internal/tui/savedpackets.go`) is the global dispatch entry point, wired
into `model.handleKey` **after** every existing text-entry/modal intercept (designer/TX/devices
forms, protocol pickers) has had first refusal and **before** core tab-switch/quit handling —
exactly "Navigation mode" defined operationally as "nothing above claimed this key," which is what
lets hotkeys fire from every tab (not just Monitor/TX Builder/Saved) while still being structurally
impossible to trigger mid-edit.

## TUI (`internal/tui`)
Bubble Tea v1.3.10 + Lip Gloss v1.1.0, one flat `model` struct (a single `Update`/`View`, per-tab
helper functions, `mode`-tagged sub-states for text-entry forms — not a nested-submodel-per-screen
framework). `bubbles` was added to `go.mod` early on but never actually used (every form is a
small hand-rolled cursor/rune-buffer state) and `go mod tidy` has since dropped it — don't re-add
it without a reason.

### Key-hint styling
Every screen's keyboard-hint bar (`"x send   r refresh   ↑/↓ select"`-shaped footer/help text) goes
through one centralized primitive, `KeyHint{Key, Desc, Disabled}` + `renderHints(...)`
(`styles.go`) — no screen hand-assembles a raw hint string and wraps the whole thing in one style.
`renderHints` renders each hint's key in the accent color (`keyStyle`, ANSI-256 `81` — the same
color as the active tab's background, deliberately) and its description in readable, undimmed
foreground (`primaryStyle`, `255`), joined by a dim middot (`secondaryStyle`). A `Disabled` hint
(e.g. "s Save" before anything has changed) renders both halves in `disabledStyle` instead, so a
genuinely unavailable action stays visually distinct from both a normal hint and from ordinary
secondary/metadata text. Color semantics, as three (plus one) named roles rather than one shared
"dim" catch-all:
- **`keyStyle`/accent** — active tabs, selected controls, keyboard keys/shortcuts.
- **`primaryStyle`** — action descriptions, labels, normal readable values.
- **`secondaryStyle`** — offsets, metadata, subtle descriptions, separators.
- **`disabledStyle`** — genuinely unavailable actions/controls.

`secondaryStyle` and `disabledStyle` happen to share color `240` today (same as the pre-existing
`dimStyle`, which other, non-hint rendering in the package still uses directly), but are kept as
two separately named styles rather than one shared variable — retuning "how dim is secondary
metadata" must never also accidentally retune "how dim is a disabled control." Every screen's
hint bar (global footer, Monitor, Packets/Designer/TX Builder/RX Inspector/Saved, Devices, the
Virtual/Manual chooser, Batch, Config/Serial Defaults, and their modals/forms/pickers/confirm
dialogs) has been migrated to `renderHints`; Logs has no hint bar of its own (relies on the global
footer). Genuinely non-hint dim text (e.g. Config's `` `serialforge protocol …` `` command
examples, Designer's placeholder prose, an inline key mention inside a prose sentence) was
deliberately left alone — this primitive is specifically for interactive-affordance hint bars, not
a blanket brightness change. See `internal/tui/keyhint_test.go` for the semantic-rendering tests
(key/desc as separate styled spans, disabled state, narrow-width sanity).

### Bounded input
**The UI invariant**: wherever the packet/schema/CRC model already knows a hard maximum for a
value, the editor prevents the impossible keystroke while typing — it does not wait for Enter and
then report an error. Model validation remains authoritative on submit; the input-time bound is a
UX improvement layered on top of it, never a replacement for it (every field/CRC form still runs
its existing submit-time check — `packet.Schema.Validate`, `checksum.Params.Validate`, each form's
own parse/range check — unchanged).

A rejected keystroke never mutates the buffer — the visible text always matches exactly what the
user typed, never an after-the-fact clamp (typing "2" past a max of 11 leaves "1" on screen, it
never silently becomes "11"). This holds for paste too: bubbletea enables bracketed paste by
default, which delivers a whole paste as one `KeyMsg` with every pasted rune in `Runes`, so both
shared helpers below process incoming runes one at a time rather than accepting or rejecting a
batch wholesale — a paste can't insert more than the same limit interactive typing allows.

`internal/tui/boundedinput.go` is the one shared policy every bounded editor funnels through,
rather than each screen re-deriving its own acceptance rule:
- **`appendDigitsWithinMax(buf, runes, max)`** — never lets `buf` parse as a base-10 integer bigger
  than `max`; non-digit/unparseable runes pass through unconstrained (submit-time validation still
  catches those). Used by Designer's field-size editor (`fieldSizeMax` — the packet's remaining
  capacity, `packet.Schema.Remaining()`, crediting a field being *edited* its own current size back
  to the budget) and its custom-CRC Width field (max = `checksum.MaxWidth`, 64 — the CRC engine's
  own hard ceiling, `checksum.Params.Validate`'s bound, exported as a named constant precisely so
  this doesn't duplicate a literal).
- **`appendHexWithinDigitLimit(buf, runes, maxDigits)`** — never lets `buf`'s semantic hex-digit
  count (`0-9`/`A-F` only; a typed separator like a space doesn't count and is always passed
  through) exceed `maxDigits`. Used by TX Builder's per-field hex editor (`maxDigits =
  2*field.Size` — TX Builder edits every field as hex regardless of its declared `packet.Format`;
  see "TX Builder" below), TX Builder's manual CRC override (`maxDigits = 2*schema.CRCSize()` — the
  active checksum's actual reserved width, live, so widening the CRC immediately widens the
  override's own budget), and Designer's custom-CRC Polynomial/Init/XOR-Out fields (`maxDigits =
  2*ceil(width bits / 8)`, derived from whatever Width currently parses to, falling back to the
  widest bound while Width is empty/mid-edit rather than blocking hex entry).

Every limit is derived from the model (`packet.Schema`, `packet.Field`, `checksum.Definition`/
`Params`) — never recomputed independently in the TUI. Screens audited and found to have **no**
natural maximum to enforce (left as submit-time-only, on purpose — inventing a bound where the
model doesn't have one was explicitly out of scope): Designer's packet-total-size and save-name
fields, Batch's scenario-path field, Devices' alias/path/VID/PID/baud fields (VID/PID are free-form
comparison strings with no declared width; baud has no meaningful application-defined ceiling — see
"Serial Defaults" below), and Saved Packets' rename/duplicate/hotkey text fields. There is no
framing/delimiter-configuration screen in the TUI today (framing is chosen automatically — fixed-
size when a schema is active, raw otherwise — see `model.connect`), so nothing to bound there.

Saved Packets loaded into TX Builder edit through the exact same code path as a freshly-built TX
session (`loadSavedPacketIntoTX` only populates `txState.values`/`schema`; there is no second,
Saved-Packet-specific field editor), so the same bound applies automatically with no extra wiring.

Six tabs (`model.tab`, `1`-`6` or `Tab`/`Shift+Tab`): **Monitor** (live RX/TX event log,
hex/ascii/both, pause/clear, and — on a wide-enough terminal — a Saved Packets sidebar, see below),
**Packets** (four `[`/`]`-switched subviews — see below),
**Devices** (saved profiles, `serial.ListDetailed()` results under "Detected hardware ports", and a
separate "Virtual / manual endpoints" section — three visually distinct groups, never merged;
add-profile form (`a`, including a manual Path field), the Virtual/Manual chooser (`m` — see
"Virtual / manual endpoint discovery" above), save-connection-as-profile (`s`, once connected),
connect — the one place a `session.Session` gets created; `Packets`/`Batch` reuse
`model.sess`), **Batch** (runs a
scenario from `<configDir>/batch/*.yaml` or an explicit path against the active connection, live
per-step results via a goroutine pushing `tea.Program.Send`), **Logs** (the application/session
event journal — see "Three observability surfaces" below), **Config**
(two `[`/`]`-switched sections — **General**, config dir/version display + a couple of persisted UI
toggles, and **Serial Defaults** — see below).

### Three observability surfaces
SerialForge deliberately keeps three separate places a user or developer looks for "what
happened," each with a different audience and a different lifetime — conflating any two of them was
the actual root cause of a real bug (Logs looked "empty since the beginning of the project"; see
below):

1. **Monitor** (`model.events`, `monitor.go`) — raw serial TX/RX byte traffic. Every byte
   transmitted or received, with hex/ascii rendering. This is the wire-level view.
2. **Logs** (`model.appLog`, `applog.go`/`logs.go`) — the user-facing application/session event
   journal: connection lifecycle, protocol activation/switches, one line per meaningful send action,
   errors/warnings. Always populated during a normal session — never depends on any opt-in flag.
   This is the "what did the application do" view.
3. **`SERIALFORGE_DEBUG_LOG`** (`internal/debuglog`) — an opt-in, disabled-by-default, file-only
   internal developer trace (key routing decisions, connect/protocol/session-lifecycle detail, raw
   hex on every TX/RX). This is the "why did the application do that" view, for diagnosing a bug —
   never rendered inside the TUI itself, never required for Logs or Monitor to work.

**Why Logs was effectively empty before this fix**: `viewLogs()` filtered `model.events` (Monitor's
own raw buffer) down to just `session.EventStatus` entries — but `EventStatus` is emitted *only* by
`internal/session.Session`'s own automatic reconnect-on-read-error logic (session.go), never by a
user-initiated connect, disconnect, protocol switch, or send. Under normal use (no physical device
ever actually drops), that condition essentially never fires, so the screen showed
"No connection events yet." for the entire life of a session regardless of how much real activity
happened — narrow, incomplete wiring from day one, not a regression. There was also no `updateLogs`
in `handleKey`'s per-tab dispatch at all — the screen had no scrolling, no clear, nothing
interactive.

**`model.logEvent(level, format, args...)`** (applog.go) is the one centralized append path every
screen/action uses — mirrors `sendTX` being the one path for Monitor's TX events, same reasoning:
one choke point is what makes "no duplicate/missing entries" provable by that single function's
tests, rather than depending on every call site individually getting it right. Bounded to
`maxAppLog` (1000) entries, oldest dropped first. Populated from:
- `connect()` — `Connected <path> @ <baud> <frame>` on a genuine new connection
  (`connectReasonNew`); nothing extra on a protocol-triggered reframe (`connectReasonReframe` —
  `activateProtocol` already journals that transition itself; see below) or the error path connect()
  now also journals.
- The async session-event pump (`Update()`'s `sessionEventMsg` case) — `EventStatus` transitions
  (disconnected/reconnecting/reconnected — the only real source of these, since there is no
  standalone user-facing "disconnect" action anywhere in the TUI today).
- `activateProtocol` — one line per actual transition (`protocolLogMessage`): "Protocol activated:
  X" (first activation), "Protocol switched: X → Y", or "Protocol X updated" (same name, framing
  changed — the protocol itself was edited), each with "(session reframed)" appended only when a
  live session was actually reconnected. A same-protocol no-op (by far the common case — repeatedly
  invoking Saved Packets for whatever's already active) produces **no** entry — deliberately, so
  Logs never floods on repeated hotkey presses.
- `sendTX` — one `LogTX` entry per successful send ("Sent `<name>` · `N` B" / "TX Builder · `N` B"),
  one `LogError` on a failed write — the exact same choke point every interactive send path (TX
  Builder, Saved Packet hotkey/direct-send, Monitor sidebar Enter) already funnels through for its
  Monitor TX event, so "exactly one Logs entry per successful send" is true by construction, not
  by convention.
- The handful of pre-send/pre-connect validation failures each screen already produced a footer
  status for (protocol missing/invalid, build failure, not-connected, a resolve failure before
  `connect()` is even reached) — each now also calls `logEvent` so the failure survives in history
  after the transient footer status disappears.

Logs never renders raw bytes or a full developer trace — only these concise, one-line-per-event
messages; `SERIALFORGE_DEBUG_LOG` output never appears inside the TUI, only in its own file, and
Logs' own journal is populated unconditionally regardless of whether that env var is set.

**The Logs screen** (`logs.go`) is a standard log-viewer "tail -f": `↑`/`↓`/`j`/`k` scroll, `g`/`G`
jump to top/bottom, `c` clears the journal — chosen only after checking `keybindings.go`'s
centralized hotkey-palette policy (`g`/`G` moved out of the assignable palette into
`reservedKeyLabels` for this, the same process `f` went through for Monitor's pane focus; `c`
was already reserved app-wide). `logsState.followTail` implements the follow-the-tail idiom: true
by default, so new entries keep the view pinned to the newest one; scrolling up turns it off (so an
event arriving mid-read never yanks the view away — a "+N new" indicator in the title shows how
many entries are hidden below); jumping back to the bottom (`G`, or scrolling all the way down)
turns it back on. Sizing mirrors Monitor's own traffic-pane box budget for visual consistency
between the app's two scrollable panes.

### Active protocol context (`model.activateProtocol`, `internal/tui/model.go`)
There is exactly one notion of "the active protocol" in the TUI: `model.activeSchema`. Every place
that changes it — the real protocol pickers in TX Builder and RX Inspector (`o`), loading a Saved
Packet into TX Builder (`loadSavedPacketIntoTX`), and invoking a Saved Packet via hotkey or direct
send (`sendSavedPacket`, both in `savedpackets.go`) — funnels through the one shared
`model.activateProtocol(sc *packet.Schema)` helper, not a scattered set of `m.activeSchema = ...`
assignments. "Active protocol" is more than that pointer: a connected session's RX framing (fixed
vs. raw, sized from the schema's `TotalSize` — see `model.connect`'s own doc comment) is fixed at
connect time, so `activateProtocol` reconnects (`model.connect` with the same path/config, a new
schema) whenever a session is live *and* `sc` actually differs from the currently active schema in a
way that changes framing (`sameFraming` — same `Name` and `TotalSize`, the only two fields
`connect`'s framer construction reads), so the displayed active protocol, the TX schema, the RX
decode/framing context, and the Monitor sidebar's filtering all agree — never just the visible
pointer while the live session keeps decoding against the previous protocol. Repeatedly invoking
Saved Packets that already reference the currently active protocol — by far the most common real
usage — is a deliberate no-op: no disconnect/reopen/new session, just an `activeSchema` pointer
refresh (still needed: `sc` may carry updated `Fields`/`Checksum` even with unchanged `Name`+
`TotalSize`). When disconnected, there's no live framing to keep in sync, so it only sets the
pointer (still enough for the Monitor sidebar and TX Builder to reflect the switch immediately).
`activateProtocol` returns the `tea.Cmd` a genuine reconnect produces (`connect`'s own
`listenSession()` re-arm) — every call site propagates it up through `model.Update()` rather than
discarding it; see "TX/RX Monitor event recording" below for why that Cmd must never be dropped.
`sendSavedPacket` only ever calls this for a Saved Packet whose `Resolve` result actually carries a
real, current schema (`StatusOK` or `StatusIncompatible` — see `savedpacket.Resolution`'s doc
comment: both carry a valid `Schema`, only `StatusIncompatible`'s stored field values are stale) —
never for `StatusProtocolMissing`/`StatusProtocolInvalid`, so a broken Saved Packet can never corrupt
the active protocol.

This closed a real gap: before, `sendSavedPacket` never touched `activeSchema` at all, so a hotkey
or the Saved screen's direct-send could build and transmit a packet for protocol X while the TUI
kept showing a stale or absent active protocol — the Monitor sidebar (which filters strictly off
`activeSchema`) stayed empty or wrong until the user separately visited Packets → Saved → Enter, the
one path that already activated correctly. See `internal/tui/protocolactivation_test.go` for the
regression coverage.

A follow-up regression cluster (Monitor not showing local TX; Tab/quit becoming unreliable — see the
next two subsections) traced back to `activateProtocol`'s *first* version always reconnecting, even
for an already-active protocol: every Saved Packet send tore the session down and rebuilt it, and
every one of those reconnects discarded its own `listenSession()` Cmd (every call site used to). The
same-protocol no-op above, plus real Cmd propagation, is the fix — diagnosed with
`SERIALFORGE_DEBUG_LOG` (`internal/debuglog`) tracing `protocol`/`connect`/`tx` events across a real
socat PTY session; see that session's final report for the exact before/after log evidence.

### TX/RX Monitor event recording (`model.sendTX`, `internal/tui/model.go`)
Invariant: **a successful serial write is a Monitor TX event; a serial read is a Monitor RX event —
recorded through exactly one path each, and never one manufactured from the other.** Every
interactive TUI send action — TX Builder's `x`, Saved Packet hotkey send, Saved Packets' direct
send, and the Monitor sidebar's own Enter-to-send (the latter three all via `sendSavedPacket`) —
funnels through `model.sendTX(data []byte, source string) (int, error)`, never a direct
`m.sess.Send(...)` call of its own. `sendTX` writes to the session and, the instant `Send` succeeds,
appends the TX event to `model.events` **synchronously, in the same call** — not by waiting on the
session's own async `Events()` channel to be drained. `internal/session.Session.Send` still emits its
own `EventTX` on success (unchanged — the CLI's headless `serialforge monitor` reads `Events()`
directly and depends on that), but `model.Update`'s `sessionEventMsg` handler explicitly discards any
`EventTX` it receives (every one is redundant with what `sendTX` already recorded) — RX and status
events are the only kinds that still flow through that async path, since they originate from the
session's own background read loop, not a call the TUI makes.

This split exists because the async path alone proved fragile: `listenSession()`'s `tea.Cmd` re-arms
itself only if a caller actually returns it, and a discarded reconnect Cmd (see "Active protocol
context" above) silently killed the pump for the rest of the session — Monitor would then never show
another TX *or* RX event. Recording TX synchronously makes TX visibility immune to that entire class
of bug, independent of the async pump's health; the reconnect-Cmd-propagation fix (and the
same-protocol no-op that makes most reconnects unnecessary in the first place) is what keeps RX
equally reliable, since it has no synchronous alternative. A failed `Send` never produces a TX event.
Two connected endpoints of a socat PTY pair are opposite ends, not a loopback — bytes SerialForge
transmits do not arrive back as its own RX unless something external actually writes to the other
end; an echoing device legitimately producing both a TX and a matching RX event is expected and the
two are never deduplicated. See `internal/tui/txmonitor_test.go`.

### Key routing priority (`model.handleKey`, `internal/tui/model.go`)
Invariant: **global navigation and quit controls can never be shadowed by a screen or pane's own
local state — Monitor's Traffic/Saved Packets focus included.** `handleKey` resolves every key in
this fixed order, documented directly on the function:
1. A genuinely open text-entry/modal editor owns the key completely (the `handleKeyIfEditing`
   intercepts — Designer/TX/Saved/Devices' four modal forms/Serial Defaults).
2. Otherwise, **hard global controls** always win: quit (`q`/`ctrl+c`) and top-level tab navigation
   (`Tab`/`Shift+Tab`/`1`-`6`). No per-tab or per-pane dispatch runs before this step.
3. Then global Saved Packet hotkeys (`trySavedPacketHotkey`).
4. Then screen/pane-local navigation — the per-tab `update*` dispatch, including Monitor's own
   traffic/sidebar focus (`f`) and resize (`←`/`→`/`r`) keys.

An earlier version of Monitor's adjustable split repurposed `Tab`/`Shift+Tab` themselves to switch
between Monitor's two panes whenever the sidebar was visible — a step-4-shaped concern that had been
placed ahead of step 2, so opening Monitor on a wide terminal silently disabled the application's
global tab-cycling binding with no way back short of narrowing the terminal or using the `1`-`6`
shortcuts. The fix was two-fold: promote quit/tab to their own explicit, unconditional step (so no
future per-screen feature can repeat the mistake by construction, not by convention), and give
Monitor's pane focus its own dedicated key, `f` (chosen after checking `keybindings.go`'s centralized
hotkey palette — outside the palette so a Saved Packet can never be assigned it, unbound anywhere
else in the app, and a single plain key rather than a modifier combo for portability across
macOS/Linux/Windows terminals, some of which intercept certain Ctrl+letter combinations). The four
Devices-tab modal intercepts (`devAdd`/`devManual`/`devVirtual`/`devSave`) were additionally hardened
to also gate on `m.tab == tabDevices` (matching the pattern `txState`/`savedState` already used),
defense in depth against a modal ever continuing to swallow keys — including quit — outside the one
tab it can be opened from. See `internal/tui/navigation_test.go`.

### Monitor: Saved Packets sidebar (`internal/tui/monitorsidebar.go`)
On a wide-enough terminal, Monitor splits into the traffic pane (unchanged) and a Saved Packets
sidebar showing the packets belonging to the currently active protocol (`model.activeSchema`) —
turning Monitor into a device-control console: observe traffic and send common commands without
leaving the tab. **Architecturally the sidebar is a VIEW/controller surface, not a second Saved
Packet model**: it owns exactly two pieces of its own state, a selection cursor and a scroll
offset (`monitorSidebarState`) — everything it displays is read fresh from `savedpacket.Store` +
`model.activeSchema` on every render and every keypress (`filteredSavedPackets`,
`(*monitorSidebarState).selected` — the same "never cache, always read the store" pattern
`savedState.selected` already uses for the dedicated Saved Packets screen), and sending goes
through the exact same `sendSavedPacket` the dedicated screen's direct-send and the global hotkey
dispatch already call (`updateMonitorSaved`) — no Monitor-specific packet-building, serialization,
or a parallel store. The chain is:
```
savedpacket.Store  →  filter by model.activeSchema.Name  →  sidebar presentation  →  sendSavedPacket
```
A rename/hotkey change/delete/create/protocol-reference edit made anywhere else (the dedicated
screen, TX Builder's save/update, hand-editing `saved_packets.yaml`) is picked up the moment Monitor
next renders or handles a key — there is nothing to invalidate.

**Filtering**: `filteredSavedPackets` returns every `SavedPacket` whose `Protocol` field equals
`model.activeSchema.Name`, in the Store's own order (the same order the dedicated screen presents —
no independent sort). `nil` (not an error) when there's no active protocol.

**Responsive breakpoint**: `monitorSidebarVisible()` — the sidebar shows only when both panes' own
rendered footprint (`monitorTrafficMinWidth`/`monitorSidebarMinWidth`, each plus
`monitorBoxOverhead` for `boxStyle`'s rounded border, plus `monitorPaneGap` between them) actually
fits `model.width`; below that, Monitor renders exactly the pre-existing full-width traffic view —
the dedicated Packets → Saved screen and Saved Packet hotkeys remain fully available either way.
This check is independent of the split ratio described next — it's about whether both panes'
*minimums* fit at all, not about whatever share of the space the user has asked for. These
constants (and the arithmetic connecting them to `boxStyle.Width()`'s argument — verified directly,
not assumed: `Width(N)` already includes `boxStyle`'s own `Padding(0, 1)`, only the border adds
columns beyond `N`) live in `monitorsidebar.go`'s own doc comment.

**Adjustable split**: once visible, the traffic/sidebar divide is user-adjustable, not fixed.
`monitorSidebarWidth()` computes the sidebar's actual on-screen width by applying a *preferred
ratio* to the current terminal's splittable column budget
(`monitorSplitAvailable() = model.width - 2×monitorBoxOverhead - monitorPaneGap`), then clamps so
neither pane can drop below its minimum — floored at `monitorSidebarMinWidth`, capped so the
traffic pane always keeps at least `monitorTrafficMinWidth`. There is deliberately no sidebar
*maximum* width constant anymore (an earlier version capped it at 40 to keep a fully automatic
sidebar from dominating a wide terminal); now that the user explicitly controls the split, the
traffic pane's own minimum is the sidebar's only real ceiling.

The preferred ratio is stored as a normalized float (`config.App.UI.MonitorSavedPacketsRatio`,
0 < ratio < 1), not a column count, specifically so it scales with terminal width instead of
staying pinned to whatever column count happened to be true when it was set. It's the same
persisted application config every other TUI preference already uses (`config.SaveApp` — no
separate preference file), read fresh and normalized/defaulted on every render
(`normalizedMonitorSplitRatio`/`effectiveMonitorSplitRatio`): a missing (zero), negative, `>= 1`,
NaN, or `Inf` stored value falls back to `monitorDefaultSavedPacketsRatio` (0.30, chosen to match
this feature's predecessor's visual balance) rather than ever breaking Monitor's layout.

**Preferred ratio vs. actual rendered width are two different things, on purpose** — a terminal
resize must never overwrite the user's preference. `monitorSidebarWidth()` always recomputes the
*actual* width fresh from the *current* `model.width` and the *stored* ratio; it never writes back
to the stored ratio. Only a deliberate resize/reset keypress changes what's stored. So: a terminal
`tea.WindowSizeMsg` recomputes actual widths from the unchanged preferred ratio (collapsing the
sidebar entirely below the breakpoint, exactly as before this feature); a user resize changes the
preferred ratio, then actual widths follow from it. A 45/55 preference survives a
narrow-then-wide-again terminal resize because nothing about that sequence ever touches the stored
ratio — the sidebar simply wasn't rendered while the terminal was too narrow to show it.

**Resize keys** (only while the sidebar has focus — see Focus below): `Left`/`Right` move the
preferred ratio by `monitorSplitStep` (a percentage of the splittable width, not a fixed column
count, so the step still feels proportional at very wide or very narrow terminals), clamped in
column space against the current terminal so a resize that would violate a minimum clamps exactly
to that boundary (`resizeMonitorSplit`) rather than doing nothing or overshooting. `r` resets to
`monitorDefaultSavedPacketsRatio`. All three were chosen only after checking `keybindings.go`'s
centralized hotkey-palette policy: `left`/`right` are already globally reserved as generic
"navigate" (excluded from the palette a Saved Packet hotkey can be assigned from) and were not
previously bound to anything in Monitor; `r` is likewise already outside the palette (reserved,
labeled "rescan / refresh / rename" elsewhere) and unbound in Monitor. None collide with a
user-assignable Saved Packet hotkey — `TestPaletteKeysNeverConsumedByMonitorSidebarDispatch`
enforces this the same mechanical way `TestPaletteKeysAreNeverConsumedByCoreDispatch` already does
for the rest of the app.

Persistence is debounced, not synchronous: every resize/reset keypress updates
`model.app.UI.MonitorSavedPacketsRatio` (and thus what renders) immediately, and schedules a
`tea.Tick`-based `monitorSplitSaveMsg` `monitorSplitSaveDebounce` (300ms) later carrying the
generation it was scheduled at (`monitorSidebarState.saveGen`); `Update()` only actually calls
`config.SaveApp` if that generation is still current when the tick fires, so holding a resize key
under terminal key-repeat collapses into a single write once the user settles, not one write per
repeat event — the existing atomic-write config path, no new persistence mechanism. This is a UI
preference (`config.App.UI`), deliberately not a Session Profile property, so a future Session
Profile never needs to own it.

**Row content**: selection marker, name (truncated to fit), an incompatible-packet mark (the same
`!` `warnStyle` glyph and `SavedPacket.Resolve` check the dedicated screen's own list already uses
— a broken-but-matching-protocol packet stays visible, marked, and `sendSavedPacket` itself refuses
to send it through the same `Resolve`/`Build` validation every other send path already runs), and
the hotkey (`keyStyle` when assigned, a dim `·` placeholder when not — never a placeholder that
reads as an assigned key). When there's genuinely more height available, the selected packet's full
detail renders below the list in the same box, reusing `viewSavedDetail` (now parameterized by
width so both the dedicated screen and the sidebar call the identical renderer, never a second
packet-preview implementation) — Name/Protocol/Hotkey/fields/CRC line, and, above
`monitorDiagramMinWidth`, the register-style diagram via `RenderDiagram`.

**Focus**: `model.monitorFocus` (`monitorPaneTraffic` / `monitorPaneSaved`). `f` switches focus
between the two panes — handled entirely inside `updateMonitor` (Monitor-local, never touching
`Tab`/`Shift+Tab`, which always cycle top-level tabs regardless of Monitor's own state — see
ARCHITECTURE.md "Key routing priority" for why that boundary is load-bearing) — but only while the
sidebar is actually visible; at a terminal too narrow for it, `f` is inert rather than silently
flipping a pane nobody can see. While the sidebar has
focus: `↑`/`↓`/`j`/`k` move the selection (clamped against the current filtered list on every
keystroke — see below), `Enter` sends, and `Left`/`Right`/`r` resize/reset the split (see
"Adjustable split" above) — none of these touch selection, scroll, focus, the active protocol, or
the traffic pane's own event log; only layout changes. While the traffic pane has focus: `p`/`c`/`m`
behave exactly as before this feature — unchanged. The focused pane's border uses `selectedBorder` (the same
accent already used for the active tab and selected diagram cells); the unfocused pane keeps
`normalBorder` — no new color introduced, and a single full-width pane (sidebar collapsed) shows no
focus color at all, since there is nothing to distinguish it from.

**Hotkeys are unaffected by any of this** — `trySavedPacketHotkey` fires in `model.handleKey`
before per-tab dispatch ever runs (see "Hotkey policy" above), so a Saved Packet hotkey sends
identically regardless of which Monitor pane has focus, or whether the sidebar is even visible. The
sidebar merely makes those bindings visible; it is not a second hotkey registry.

**Safety**: `(*monitorSidebarState).clamp(n)` repositions cursor/scroll against the *current*
filtered list's length before every read or key handled — so a packet disappearing (deleted, or its
`Protocol` reference changed away from the active one) or the active protocol changing out from
under the sidebar can never leave the cursor pointing past the end of what it's now looking at.
`visibleWindow` keeps the selected row on screen when more packets exist than fit vertically,
adjusting only the sidebar's own scroll offset — independent of, and never disturbing, the traffic
pane's own event-log position.

**Packets** subviews, all built on `RenderDiagram`:
- **Designer** (`packetsDesigner`, `designer.go`): the schema editor — set total size (`enter` on
  that row), add (`n`)/edit (`enter`)/delete (`x`)/duplicate (`d`)/reorder (`</>`) fields, open the
  CRC picker (`enter` on the CRC row: pick a preset, disable with `n`, or `u` for a full
  custom-parameter form), save as a named profile (`s`) or open an existing one (`o`), start a new
  draft (`N`). The diagram re-renders after every change from `d.schema.Layout()` — never cached.
  **Tail-checksum invariant**: the field list always renders Packet size, then every user field in
  packet order, then the CRC row last — never a hand-picked visual reorder, but a direct read of
  `Layout()`'s own field-then-CRC ordering (`designerState.checksumRow`/`cursorField` in
  `designer.go`), so the list and the diagram below it can never disagree. A tail CRC isn't a
  `schema.Fields` entry at all (see `Schema.CRCOffset`), so add/delete/duplicate/reorder — all of
  which only ever touch `Fields` — structurally cannot place anything after it; the CRC row itself
  is reachable (to enable/disable/reconfigure it) but not a reorder target. The row reads `CRC   N
  B · <algorithm>` once enabled (the actual reserved tail size, not just the algorithm name) or
  `CRC   none` when disabled.
- **TX Builder** (`packetsTX`, `txrx.go`): pick a protocol (`o`), edit each field's hex value
  (`enter`), set/clear a manual CRC override (`c`), send over the active session (`x`) — live
  raw-bytes preview via the same diagram the whole time. Every field is edited as hex regardless of
  its declared `packet.Format` — `uint`/`int`/`ascii`/`raw` are Designer-only metadata today, not
  separate TX Builder editing modes — and the hex editor is bounded while typing to exactly 2 hex
  digits per declared byte (`editMaxHexDigits`, see "Bounded input" above), same for the manual CRC
  override (bounded to the active checksum's own reserved width). The field-list CRC row (`txCRCLine`, hidden
  entirely when the schema has no checksum) reads `<algorithm> · AUTO|OVERRIDE → <value>`, e.g.
  `CRC-8/MAXIM-DOW · AUTO → 61`, with the value visually emphasized over the mode word (it's the
  byte that's actually about to go out) and a `(calculated NN)` note appended whenever an override
  actually changed the byte. The diagram's own CRC cell (`CRCDisplay: CRCDisplayAuto`) mirrors
  this — value + AUTO/OVERRIDE, abbreviating the algorithm name to fit (`crcCellLabel`) and, once
  cramped, the mode word too (`crcAutoValueCell`: `"09 · AUTO"` → `"09 AUTO"` → bare `"09"`, and
  `"42 · OVERRIDE"` → `"42 OVR"` → bare `"42"` — tiered degradation, never a mid-word ellipsis like
  `"OVER…"`) — and, like the field-list row, never shows PASS/FAIL (see the PASS/FAIL hard
  invariant above).
- **RX Inspector** (`packetsRX`, `txrx.go`): pick a protocol to decode against (also reframes the
  live connection to that packet's `TotalSize`, fixed framing — see `model.connect`), browse a
  bounded (500-entry) history of decoded packets. The diagram (`CRCDisplay: CRCDisplayCompare`, the
  zero value) shows CRC PASS/FAIL in its cell; `rxCRCLine` spells out both sides of that comparison
  underneath as `CRC RX <value>   CALC <value>   PASS|FAIL` so a mismatch shows exactly which byte
  the device sent versus what the schema's algorithm computes.
- **Saved** (`packetsSaved`, `savedpackets.go`): the Saved Packets list + detail (name/hotkey/
  protocol, field values, the `txCRCLine` CRC row, and `RenderDiagram` when the packet resolves
  cleanly — a `savedpacket.Resolution` problem list otherwise, never a crash). `enter` loads into TX
  Builder (`model.loadSavedPacketIntoTX`); `x` sends directly; `d`/`r`/`delete` duplicate/rename/
  delete; `h` assigns/clears the hotkey. See "Saved packets" above for the model/build path and
  hotkey policy this subview and TX Builder's `s`/`u` (Save/Update) both sit on top of.

### Serial Defaults (Config tab)
An editable TUI view onto tier 3 of the serial-setting precedence chain (see "Device profiles" →
"Serial setting precedence"): `config.App.Serial` (`SerialPrefs`) plus `config.App.Reconnect.Enabled`
for the Auto Reconnect row — never a second serial-config representation, and never a new
persistence mechanism. `internal/tui/serialdefaults.go`'s `serialDefaultsState` holds an
edited-but-maybe-unsaved *working copy* (`config.SerialPrefs`) plus `autoReconn bool`; the row list
always displays the *effective* config (`device.ResolveSerialConfig(config.App{Serial: working},
nil, nil)`, i.e. tiers 3+4 only), so a freshly-opened screen shows concrete values (115200/8/None/
1/None/On) rather than a blank "unset" state, exactly like every other caller of
`ResolveSerialConfig`.

Rows: Baud (`enter` opens a picker of `serial.BaudPresets` plus a trailing "Custom…" row that opens
a small text form — reuses `textForm`, the same one-field-at-a-time widget Saved Packets' rename/
duplicate/hotkey forms and TX Builder's save-packet form use — for an arbitrary valid rate), Data
bits (`5678`), Parity (`None`/`Even`/`Odd`/`Mark`/`Space` — all five are real, verified against
`internal/serial/port.go`'s `toLibParity`), Stop bits (`1`/`1.5`/`2` — all three real, verified
against `toLibStopBits`), Flow control, and Auto Reconnect (`enter`/`space` toggles in place, no
picker). **Flow control deliberately offers only `None` and `RTS/CTS`** — `FlowXonXoff` is accepted
by `serial.Config.Validate` and modeled in the type, but `applyFlowControl` does nothing for it (no
real XON/XOFF implementation on the transport, see "Serial engine" / Known limitations); offering it
here would be a silent no-op once connected, so the picker only ever lists values the real
transport honors.

Any row edit sets `dirty`, shown as `Serial Defaults *` in the title (cleared back to `Serial
Defaults` on a successful save) — the same dirty/title-marker convention TX Builder's Saved-Packet
relationship uses. `s` validates the working copy through `device.ResolveSerialConfig(...).
Validate()` (the exact function/method every other caller uses — no parallel validation logic);
on success it writes `app.Serial`/`app.Reconnect.Enabled` and calls `config.SaveApp` (the existing
atomic-write path); on failure it sets an inline error and leaves the working copy dirty rather than
persisting anything invalid. `r` opens a confirm modal (`y`/`enter` confirm, `esc`/`n` cancel — the
same shape as the Saved Packets delete-confirm dialog) that resets only the five UART fields back to
a zero `SerialPrefs` (which already falls through to `serial.DefaultConfig()` — 115200 8N1, no flow
control — no new default constants introduced); Auto Reconnect is deliberately left untouched by
reset, since it isn't part of the UART framing this screen resets.

Designed to stay compatible with a future Session Profile tier (see "Saved packets" and Known
limitations): Serial Defaults remains exactly the standalone, referenceable tier 3 it already was —
a future Session Profile can add a tier above it without this screen or `config.SerialPrefs`
changing shape. See `internal/tui/serialdefaults_test.go` for behavioral coverage (persistence,
reload-after-restart, every field type, the Xon/Xoff exclusion, dirty/save, reset) and
`internal/device/resolve_serial_test.go` for the precedence-chain proof underneath it.

TX Builder additionally tracks its relationship to a loaded Saved Packet: `txState.savedName` (""
unless this session was loaded from one) and `dirty` (set the moment a field/CRC-override edit
actually changes a value while `savedName != ""`). Editing here **never** auto-mutates
`SavedPackets` — only `s` (Save/Save-as, opens a name+hotkey form) or `u` (Update, only enabled once
`savedName != ""`) write to the store, matching the product's explicit "load → edit → dirty →
original unchanged → Update" workflow, verified end-to-end (including a real PTY send proving the
unmodified original still transmits until Update is pressed) — see the manual verification section
of the feature's handoff report.

`model.connect(path, cfg, schema)` is the one path that opens a `session.Session`: fixed-size
framing when a schema is given, raw framing otherwise, `session.DefaultReconnectPolicy()` always
on. Incoming `EventRX` frames are decoded against `model.activeSchema` (if set) and appended to
`rx.history` inside `model.appendEvent` — the single funnel every tab's data flows through.

### TUI startup
Zero saved device profiles, zero detected ports, and no selected device are all normal, tested
starting states — `TestTuiStartsWithZeroDevicesAndZeroDetectedPorts` forces exactly that and
asserts every tab still renders (see Testing in CONTRIBUTING.md). The one thing that legitimately
prevents `Run` from opening anything is having no real terminal to draw into: Bubble Tea's own
input setup opens `/dev/tty` as a fallback when stdin isn't a terminal (so piped input still
works), and if that also fails — no controlling terminal at all — `p.Run()` returns `could not
open a new TTY: ...` immediately, before any frame is drawn. This is unrelated to device/USB
state. `friendlyStartError` in `model.go` recognizes this specific error and turns it into an
actionable message pointing at the headless commands, instead of a bare wrapped `os.Open` error.

**Limitations, honestly**: no horizontal-scroll mode for the diagram (multi-row wrap only — a
deliberate, documented choice, not a gap); no in-TUI custom-CRC-definition library (see CRC
engine); no on-disk raw capture from the TUI (see "Logging and captures").

## CLI (`cmd/serialforge`)
Manual dispatch (`main.go`'s `run(args)`), one `commands_*.go` file per group, no CLI framework.
Bare `serialforge` / `serialforge tui` launches the TUI (`commands_tui.go`
loads the config dir + both stores, then calls `tui.Run`). Global flags `--config <path>` and
`--json` are recognized anywhere in the argument list.

### CLI argument-parsing invariant
**User-facing CLI commands must not depend on arbitrary flag ordering. Named flags are canonical;
positional forms are optional shorthand and must not conflict with explicit arguments.** This was
violated once (`serialforge monitor --port /tmp/serialforge-a --baud 115200` tried to open a
device literally named `--port` — a first-token-is-the-device assumption that didn't actually
parse flags first) and must not be reintroduced.

### The parser (`argparse.go`)
`monitor`, `send`, and `batch run` — the three commands that mix flags with a device/payload — all
go through one generic, order-independent parser: `parseArgs(args, []flagDef)` scans every token
regardless of position, filing recognized flags (by any of their declared aliases) under a
canonical key and collecting everything else as positionals; an unrecognized `-`/`--`-prefixed
token is a hard parse error (never silently treated as a positional — that's the exact bug class
above), and a value-taking flag with nothing following it errors immediately.
`parsedArgs.single(canonical, label)` then resolves one flag's effective value, erroring if two
aliases (e.g. `--port`/`--path`) were given with *different* values (same value given twice is not
an error). Each command layers its own semantic resolution on top:
- **monitor** (`resolveDeviceArg`): `--port`/`--path` is canonical; a single positional is
  shorthand; positional-plus-flag is a conflict error; too many/zero positionals is an error.
- **send** (`resolveSendMode` + `resolveSendArgs`): `--hex`/`--text` are boolean mode flags (never
  take a value themselves — giving both is a conflict error, default is text); the payload is
  *always* a positional. With `--port`/`--path` given, exactly one positional (the payload) is
  expected; without it, exactly two (device, then payload — the positional-shorthand form). This
  is what makes `send --port /tmp/a --hex "AA 55"` and `send --hex "AA 55" --port /tmp/a` parse
  identically: the quoted payload is a single positional token regardless of where `--hex`/`--port`
  land in argv.
- **batch run**: `--device`/`--port`/`--path` are three aliases of one canonical field (falling
  back to the scenario file's `device:` if none given); the scenario path is the sole positional.
`packet build`/`packet decode`/`device add` never had this bug — they take no positional device
argument at all (schema/alias always via `--protocol`/`--alias`), so the pre-existing anywhere-in-
argv `flagValue` scan was already order-independent for them.

`--help`/`-h` are checked (`wantsHelp`, a cheap pre-scan) *before* strict parsing on every
subcommand, so `serialforge monitor --help` shows help even though there's no device — an
otherwise-invalid command must still be able to ask for help. `monitor`, `send`, `packet`,
`batch`, and `device` all have dedicated help text (usage, flags, defaults, examples); exit 0.

### Serial setting precedence
`internal/device.ResolveSerialConfig(appCfg, profile, overrideBaud) serial.Config` is the one
implementation of "what settings does this connection actually use," called identically by
`resolveDevice` (CLI), the TUI's manual-connect form, and the TUI's saved-profile connect — see
"Device profiles" above for the four-tier precedence and `config.SerialPrefs` (`app.yaml`'s
`serial:` block) for the app-config tier. No `--baud` is required for normal use; the connection
banner (`Connected <path> @ <baud> <frame>`, e.g. `Connected /tmp/serialforge-a @ 115200 8N1`) is
printed by `monitor`/`send`/`batch run` and shown in the TUI header/status line via
`serial.Config.FrameString()`.

Exact commands implemented: `version`, `help`, `config path`, `ports [--detailed] [--json]`,
`device list|show|add|delete|rename|clone` (+ `--help`), `protocol
list|show|import|export|delete|clone|rename`, `packet build --protocol NAME --field NAME=HEX...
[--crc-override HEX] [--json]` (+ `--help`), `packet decode --protocol NAME --hex "..." [--json]`,
`saved list|show|delete [--json]` and `saved send <name> [--port PATH] [--baud N] [--json]` (the
headless equivalent of pressing a Saved Packet's hotkey — `cmdSavedSend` builds through the exact
same `savedpacket.SavedPacket.Build` the TUI uses, then `resolveDevice`/`openSession`/`Send`, the
same helpers `monitor`/`send` call; `--port`/`--path`/positional-shorthand device resolution goes
through the same `resolveDeviceArg` as `monitor`/`send`, so argv order-independence is inherited,
not reimplemented — see `commands_saved_test.go`'s real-PTY, multiple-argv-shape test),
`batch run <file.yaml> [--protocol NAME] [--device ALIAS|PATH] [--baud N] [--json]` (+ `--help`,
non-zero exit on scenario failure — composes with CI), `monitor --port <path> [--baud N]
[--hex|--ascii|--both]` (+ `--help`; headless byte dump, Ctrl+C to stop), `send --port <path>
--hex|--text <payload> [--baud N]` (+ `--help`). `--port`/`--path` (aliases) resolve a saved alias
via `device.Resolve` first, falling back to treating the value as a literal OS path — a manual/
virtual path (a socat PTY link) works identically to a saved alias or a real device (see "Manual
serial paths" under Device profiles). Positional shorthand (a bare device argument in place of
`--port`, and for `send` a second bare payload argument) remains valid but is documented as
secondary; combining it with the equivalent explicit flag is a conflict error, never a silent
guess.

Every packet/batch command calls straight into `internal/packet`/`internal/batch` — no parallel
implementation. `cmd/serialforge` has real parser test coverage: `argparse_test.go` (the generic
parser primitives — order independence, aliasing, unknown/missing-value/conflicting-value errors),
`commands_monitor_test.go` (the resolver functions
`resolveDeviceArg`/`resolveSendArgs`/`resolveSendMode`, both valid and invalid argv forms),
`commands_serial_test.go` (`ResolveSerialConfig`-adjacent + a couple of resolver edge cases). Go
CLI entrypoints are otherwise conventionally tested by exercising the internal packages they call,
which already have their own suites.

## Batch engine (`internal/batch`)
`Scenario{Name, Protocol, Device, Steps []Step}`; `Step` is a tagged union (one populated
pointer field per step, YAML shape `- send_packet: {...}`). `Run(ctx, *session.Session,
*packet.Schema, Scenario, onStep)` executes steps in order, stops at the first failure, returns a
`Report{Steps, Pass, Elapsed}`. `onStep`, if given, fires synchronously after each step — the
TUI's live view and (optionally) any future streaming CLI output both use this; `cmd/serialforge
batch run`'s default text mode uses it for live progress lines, `--json` mode ignores it and
prints the final `Report`.

Implemented steps: `send` (raw hex), `send_packet` (schema fields + optional CRC override),
`sleep`, `expect`/`expect_hex` (literal byte match with timeout), `expect_packet` (decode next
frame against the schema), `assert_field` (compares a field's raw bytes), `assert_crc`, `clear_rx`
(drains buffered `session.Event`s), `log`. Not implemented: `open`/`close`/`reconnect`/`repeat`/
`set`/`extract`/`assert` (generic expression) /`capture` — **planned**, see Remaining work.
`examples/batch/uart-demo-smoke.yaml` is a worked example, kept honest by a test that parses the
tracked file itself.

## Automation / API
Implemented: the CLI's `--json` output on every read/build/decode/list command, `batch run`'s
non-zero exit on failure. **Not implemented**: a `serialforge daemon` / local JSON-RPC-over-socket
server. A Python (or any external) automation client today shells out to the
CLI and parses `--json` output — straightforward, but there's no persistent-process/lower-latency
path yet. See Remaining work.

## Configuration (`internal/config`)
`Dir(override)`: `override` (from `--config`) → `SERIALFORGE_CONFIG_DIR` env →
`os.UserConfigDir()/serialforge` on Linux (`SerialForge` on macOS/Windows), created if missing.
`WriteFileAtomic`: temp file in the same directory + `os.Rename`, so readers never see a partial
file and a crash mid-write leaves the original untouched. Files in the
directory: `app.yaml` (UI prefs, reconnect policy, and `SerialPrefs` — the app-wide serial-line
defaults, editable from the TUI's Config → Serial Defaults screen, see "TUI" above), `devices.yaml`,
`protocols.yaml`, `saved_packets.yaml`; a `batch/` subdirectory is where the TUI's Batch tab looks
for scenario files (created by the user/CLI, not auto-created).

## Logging and captures
**Implemented**: an in-memory, bounded (2000-entry) event log (`model.events`) shared by the
Monitor tab (all events, live) and Logs tab (status events only) — RAM-bounded, never unbounded.
RX packet history is a separate bounded (500-entry) buffer
(`rx.history`) holding full `*packet.Packet` (raw bytes + decoded fields + CRC result), not just
formatted strings. **Not implemented**: writing any of this to disk — no raw serial capture file,
no persisted packet-history log, no separate application-log file. Everything above lives only in
the running TUI process's memory and is lost on exit. See Remaining work.

## Cross-platform status
- **macOS (arm64)**: compiles (native, cgo default-on — full port enumeration) and
  `CGO_ENABLED=0` (fallback, path-only enumeration). Runtime-tested: CLI commands and TUI
  launch/quit. Hardware-tested: no.
- **macOS (amd64)**: `CGO_ENABLED=0` cross-compile only. Not runtime-tested, not hardware-tested.
- **Linux (amd64/arm64)**: `CGO_ENABLED=0` cross-compile only. Not
  runtime-tested, not hardware-tested.
- **Windows (amd64)**: `CGO_ENABLED=0` cross-compile only. Not runtime-tested, not
  hardware-tested. Windows-specific concerns not yet exercised: `COM*` path handling (the code
  never assumes `/dev/...`, but hasn't been proven against a real `COM` port), console/ANSI
  behavior for Lip Gloss output on legacy `cmd.exe` vs Windows Terminal.
- Compile success on a platform does not imply runtime correctness there — Linux and Windows are
  currently cross-compile-verified only; contributions with real testing on those platforms (and
  against real USB-serial hardware) are welcome.

## Performance / concurrency
- RX loop: one goroutine per `Session`, buffered `Read` → `Framer` → bounded (256) event channel;
  a full channel drops the event rather than blocking the reader.
- TUI: bounded 2000-entry event log, bounded 500-entry RX packet history; Monitor's rendered view
  additionally clips to the visible pane height, so a long-running session doesn't rebuild huge
  strings every frame.
- Batch runs execute in a goroutine (`batchview.go`), pushing messages back via
  `tea.Program.Send` — `Update()` is never blocked on scenario I/O/timeouts.
- Shutdown: `Session.Close()` cancels its context, closes the port, and blocks until the RX
  goroutine actually exits (`<-s.done`) — verified by `TestSessionCloseStopsCleanlyAndClosesEvents`
  and by `go test -race` not flagging a leak across the whole suite.
- Known gap: no `-race` coverage of the TUI's own goroutines (the batch-run goroutine's
  `program.Send` calls) — plausible but not proven race-free; see Remaining work.

## Important design decisions (do not revert without a reason)
- **CLI flags parse order-independently by construction, never by "the device is args[0]".**
  `argparse.go`'s `parseArgs` scans every token for a recognized flag regardless of position; a
  positional device/payload is only ever inferred from *tokens the flag scan didn't claim*, never
  from a fixed index. Don't reintroduce `args[0]`-as-device logic in `monitor`/`send`/`batch run` —
  see "CLI argument-parsing invariant" above for exactly what that broke.
- **Byte-serial bit-by-bit CRC, not a table-driven implementation.** Packets here are small; the
  bitwise form is what's easy to verify against a datasheet. Don't "optimize" this into a
  table-driven engine without re-verifying against every preset's check vector and the stdlib
  cross-checks.
- **CRC engine requires Width ≥ 8.** The byte-XOR-into-top-byte algorithm doesn't generalize below
  one byte; don't silently allow it — extend the algorithm properly (see Ross Williams' bit-by-bit
  insertion form) if sub-byte CRCs are ever needed.
- **`FakePort`'s two directions are unbuffered `io.Pipe`s, not buffered channels.** This is
  deliberate (simulates a stalled/undrained link) even though it demands a concurrent reader in
  every TX-direction test — see the type's doc comment before "fixing" this into something
  buffered.
- **One flat TUI `model`, not per-screen submodels.** Keeps cross-screen state (the active
  `session.Session`, the active schema) trivial to share; don't refactor into a nested-model
  architecture without a concrete reason.
- **`internal/protocol.Store.Put` accepts an invalid/incomplete schema.** The designer must be able
  to save drafts; validity is enforced at the point of *use* (serialize/decode/batch), not at the
  point of *save*.
- **darwin's build-tag enumeration split (`enumerate_enumerator.go` / `enumerate_fallback.go`) is
  load-bearing.** Don't merge them or add a runtime `if runtime.GOOS == "darwin"` check instead —
  the whole point is that the fallback file only exists in a build where the real one can't even
  compile.
- **Auto-discovery must never be broadened to list every `/dev/ttys*` (or every PTY-shaped path in
  general).** On macOS those names are shared with ordinary terminal sessions, not exclusive to
  serial hardware — enumerating them would make a plain terminal tab show up as if it were a
  serial device. Manual/virtual serial paths (PTYs included) are handled by trusting an *explicit*
  path (`device.Resolve`, `--port`/`--path`, the TUI's manual-connect form — see "Manual serial
  paths"), never by widening what gets auto-listed. If a future change touches
  `enumerate_enumerator.go`'s regex to be more permissive, that's very likely a regression of this
  invariant, not a feature.
- **`internal/tui/keybindings.go`'s `hotkeyPalette` is a permanent carve-out, never a snapshot of
  "keys not currently used."** A Saved Packet hotkey may only ever be assigned from this small,
  disjoint allowlist; a future core/screen keybinding must be added from *outside* it, never from
  inside — see "Saved packets" → "Hotkey policy" and
  `TestPaletteKeysAreNeverConsumedByCoreDispatch`, which fails the build if this is ever violated.
  Don't "simplify" hotkey validation into a denylist of currently-reserved keys — that's exactly the
  fragile design this replaced.

## Known limitations
- Sub-byte (bit-level) packet fields are not implemented — every `Field.Size` is whole bytes.
- CRC coverage is only ever "every byte before the CRC field" in practice — `Coverage{Mode:
  range}` is modeled/serializable but not selectable from any UI and not exercised by tests beyond
  the model itself.
- No named/reusable custom-CRC definition library — a custom CRC is entered per-protocol in the
  designer, not saved standalone and picked from a list.
- `FlowXonXoff` is accepted as a config value but not actually implemented on the real transport —
  the TUI's Serial Defaults flow-control picker deliberately never offers it (see "TUI" → "Serial
  Defaults") to avoid a silent no-op, but it remains settable by hand-editing `app.yaml`/a device
  profile, and is reported as-is (not hidden) if found there.
- No on-disk capture/logging of any kind — raw serial bytes, packet history, and the app's own
  event log all live only in the running process's memory (TUI) or aren't persisted at all (CLI,
  beyond command output).
- No local automation daemon/API — automation is "shell out to the CLI with `--json`" only.
- No quick-send hotkey palette (a `p`-triggered "pick from a list of bound hotkeys" overlay) — the
  Saved Packets screen already shows every binding, and the model (a `savedpacket.Store` +
  centralized keybinding policy) supports adding this cleanly later; not built in v1 since it's an
  explicitly optional addition, not a gap in the core save/load/send/hotkey feature set.
- No Session Profiles (device + serial settings + protocol + a Saved Packet set/bindings, bundled
  and switchable as one unit) — intentionally not built yet, but `SavedPacket` was designed to be
  standalone and reusable (a name-keyed, independently-persisted entity, referenced by name rather
  than embedded), and Serial Defaults (see "TUI") is a standalone, referenceable precedence tier
  rather than something embedded elsewhere, specifically so a future Session Profile can reference
  existing Saved Packets and layer above Serial Defaults without a persistence-format change to
  either.
- Batch steps `open`/`close`/`reconnect`/`repeat`/`set`/`extract`/generic `assert`/`capture` are
  not implemented.
- No horizontal-scroll or zoom mode for the packet diagram — large packets always wrap to
  multiple rows instead (a deliberate choice, not an oversight, but it does mean a very wide
  single-row view is never offered even when the user might prefer it).
- Nothing in this codebase has been run against real *physical* serial hardware — the real
  transport path (`internal/serial.Open`, `internal/session`) has been exercised against a real
  OS-level PTY (via `socat`), which is real kernel serial-line I/O and a genuine integration test
  of the transport code, but it is not a USB adapter, and PTYs can't exercise USB-specific concerns
  (VID/PID enumeration timing, cable-removal detection, baud/parity/flow-control behavior a real
  UART chip enforces that a PTY doesn't). See "Manual serial paths" and CONTRIBUTING.md's PTY
  integration tests.
- No CI has actually executed `.github/workflows/*.yml` yet — they're authored and reviewed for
  correctness, not proven green.
- `.goreleaser.yaml` builds all five targets with `CGO_ENABLED=0` from one Linux runner, so a real
  release built by `release.yml` today would ship darwin binaries with the *degraded* (path-only)
  port enumeration, not the full IOKit-backed one — see that file's header comment.

## Remaining work (priority order)
1. Physical hardware validation: connect at least one real USB-serial adapter and confirm open/
   read/write/close, baud changes, VID/PID enumeration, and cable-removal/reconnect all behave as
   designed — PTY testing (see "Manual serial paths") covers the transport code path but not
   USB-specific behavior.
2. Deeper visual/UX passes as new screens are added — the key-hint contrast pass and Serial
   Defaults were manually verified in a real scripted-pty terminal session (colors, spacing,
   keyboard flow, persistence), but that coverage is per-feature, not a standing guarantee for
   screens added later.
3. On-disk logging/capture: a raw binary-safe capture writer and a persisted structured packet
   history — currently both are in-memory-only.
4. Named custom-CRC definitions, saved and reusable across protocols (currently per-schema only).
5. Selective CRC coverage UI (the `range` mode already exists in the model).
6. Local automation daemon (`serialforge daemon`, Unix socket + Windows named pipe) for lower-latency
   automation than shelling out to the CLI per call.
7. Remaining batch steps: `repeat`, `set`/`extract` (variable capture from a decoded field, for
   chained assertions), `capture`.
8. A macOS-runner leg in the release pipeline so shipped darwin binaries keep full port
   enumeration instead of `.goreleaser.yaml`'s current `CGO_ENABLED=0`-everywhere shortcut.
9. Actually run `.github/workflows/ci.yml`/`release.yml` against GitHub Actions at least once.
10. Sub-byte field support, if a real protocol needs it (the model has a documented evolution path
    but no implementation).
