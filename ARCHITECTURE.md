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
- **Engine requires `Width` in 8..64 bits.** `Compute` XORs each (optionally reflected) input byte
  into the top byte-position of the width-bit register, then shifts 8×, conditionally XORing `Poly`
  — the standard byte-serial bit-by-bit CRC simulation. Widths below 8 bits are rejected by
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
  `enter` on the Checksum row) exposes preset selection and a full custom-parameter form
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

Six tabs (`model.tab`, `1`-`6` or `Tab`/`Shift+Tab`): **Monitor** (live RX/TX event log,
hex/ascii/both, pause/clear), **Packets** (four `[`/`]`-switched subviews — see below),
**Devices** (saved profiles, `serial.ListDetailed()` results under "Detected hardware ports", and a
separate "Virtual / manual endpoints" section — three visually distinct groups, never merged;
add-profile form (`a`, including a manual Path field), the Virtual/Manual chooser (`m` — see
"Virtual / manual endpoint discovery" above), save-connection-as-profile (`s`, once connected),
connect — the one place a `session.Session` gets created; `Packets`/`Batch` reuse
`model.sess`), **Batch** (runs a
scenario from `<configDir>/batch/*.yaml` or an explicit path against the active connection, live
per-step results via a goroutine pushing `tea.Program.Send`), **Logs** (connection-lifecycle
history — a filtered view of the same bounded `model.events` buffer Monitor reads), **Config**
(config dir path, a couple of persisted toggles, `s` to save `app.yaml`).

**Packets** subviews, all built on `RenderDiagram`:
- **Designer** (`packetsDesigner`, `designer.go`): the schema editor — set total size (`enter` on
  that row), add (`n`)/edit (`enter`)/delete (`x`)/duplicate (`d`)/reorder (`</>`) fields, open the
  CRC picker (`enter` on the Checksum row: pick a preset, disable with `n`, or `u` for a full
  custom-parameter form), save as a named profile (`s`) or open an existing one (`o`), start a new
  draft (`N`). The diagram re-renders after every change from `d.schema.Layout()` — never cached.
- **TX Builder** (`packetsTX`, `txrx.go`): pick a protocol (`o`), edit each field's hex value
  (`enter`), set/clear a manual CRC override (`c`), send over the active session (`x`) — live
  raw-bytes preview via the same diagram the whole time. The field-list CRC row (`txCRCLine`, hidden
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
engine); no on-disk raw capture from the TUI (see "Logging and captures"); the TUI has not been
reviewed by a human sitting at a keyboard in a real terminal — see Known limitations.

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
directory: `app.yaml` (UI prefs + reconnect policy), `devices.yaml`, `protocols.yaml`,
`saved_packets.yaml`; a `batch/` subdirectory is where the TUI's Batch tab looks for scenario files
(created by the user/CLI, not auto-created).

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
- `FlowXonXoff` is accepted as a config value but not actually implemented on the real transport.
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
  than embedded) specifically so a future Session Profile can reference existing Saved Packets
  without a persistence-format change.
- Batch steps `open`/`close`/`reconnect`/`repeat`/`set`/`extract`/generic `assert`/`capture` are
  not implemented.
- No horizontal-scroll or zoom mode for the packet diagram — large packets always wrap to
  multiple rows instead (a deliberate choice, not an oversight, but it does mean a very wide
  single-row view is never offered even when the user might prefer it).
- The TUI has not been visually reviewed in a real interactive terminal session by a human.
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
2. Visual/UX pass on the TUI in a real terminal (colors, spacing, the diagram at various realistic
   widths, keyboard flow) — everything so far is compile+smoke-test verified, not eyeballed.
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
