# Contributing

Thanks for looking at SerialForge. This document covers building, testing, and the conventions
the codebase follows. See [`ARCHITECTURE.md`](ARCHITECTURE.md) for package layout and design
invariants, and [`docs/product.md`](docs/product.md) for the product specification.

## Build

Requires Go 1.26+.

```sh
go build -o serialforge ./cmd/serialforge
```

## Testing

```sh
go test ./...          # unit tests — no hardware required anywhere
go test ./... -race    # concurrency check
go vet ./...
gofmt -l .              # should print nothing
```

`internal/checksum`/`internal/packet` are pure functions; `internal/serial`/`internal/session`/
`internal/batch` build on `serial.FakePort`/`FakeDevice`, an in-memory `io.Pipe`-based transport,
so the whole suite runs without any hardware attached. Notes on specific areas:

- **CRC correctness**: a catalogue check-vector table (`internal/checksum/crc_test.go`) plus
  stdlib cross-validation (`hash/crc32`/`hash/crc64` for the 32/64-bit presets) — treat any new
  preset addition as needing both, since a transcription error in a polynomial/init/xorout
  constant can still look plausible without an independent cross-check.
- **Packet correctness**: round-trip (fields → serialize → decode → same fields + CRC PASS), the
  explicit failure cases (over/under-allocated schema, CRC reservation math, reorder), CRC
  corruption detection, and manual-override marking (`Manual` vs `Overridden` on `CRCResult` are
  genuinely distinct — an override that happens to match the calculated value is
  `Manual=true, Overridden=false`).
- **TUI**: `internal/tui/model_test.go` renders every tab and every Packets subview under `go
  test` (no TTY needed) and drives a full designer interaction (set size → add field → schema
  validates). `internal/tui/devices_test.go` drives the Virtual/Manual chooser end to end.
  `internal/device/virtual_test.go` and `recent_test.go` cover discovery/dedup and the recent-
  endpoints store in isolation, with no TUI or real `/tmp` dependency.
- **Saved packets**: `internal/savedpacket/*_test.go` covers the model/store in isolation
  (persistence round-trip, protocol-reference-not-copy, AUTO recalculation vs. OVERRIDE
  preservation, every `Resolve` status including a duplicate-field-name draft schema, rename/
  duplicate/delete). `internal/tui/keybindings_test.go`'s
  `TestPaletteKeysAreNeverConsumedByCoreDispatch` drives every screen's Navigation-mode dispatch
  with every hotkey-palette key and asserts nothing is consumed — the regression guard behind the
  "hotkeys live in a space core bindings never touch" design (see `ARCHITECTURE.md`). `internal/tui/
  savedpackets_test.go` and `txrx_test.go` cover Save/Load/Update/dirty-state, direct send and
  hotkey send through a fake `session.Session` (proving one keypress sends exactly one packet, and
  a hotkey is suppressed while any text-entry form is open and resumes once it closes), and
  collision/rendering. `cmd/serialforge/commands_saved_test.go` drives `saved send` through a real
  `socat` PTY with three different argv shapes (flags reordered, positional shorthand) to prove
  order-independence end to end, not just at the resolver level.
- **PTY integration tests** (`internal/serial/pty_test.go`, `internal/session/pty_test.go`,
  `//go:build !windows`): spin up a real `socat`-linked PTY pair and exercise the actual
  `internal/serial.Open`/`internal/session.Session` code paths against it — no fakes. Cover both
  directions with a binary-unsafe test vector (embedded NUL + non-ASCII bytes), and structured
  `packet.Decode` (with CRC) over the same PTY. Skip (not fail) when `socat` isn't installed. Run
  explicitly with:

  ```sh
  go test ./internal/serial/... ./internal/session/... -run PTY -v
  ```
- Cross-compilation should be checked whenever transport/build-tag code changes, not just once:

  ```sh
  for target in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64; do
    os="${target%/*}"; arch="${target#*/}"
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -o /dev/null ./...
  done
  ```

  (macOS also gets a native `darwin/arm64` build with cgo default-on, which is what enables full
  USB metadata enumeration there — see `ARCHITECTURE.md`'s "Serial engine".)

## Developing without hardware

You don't need a physical serial device to work on SerialForge. Two options:

1. **`internal/serial.FakePort`/`FakeDevice`** — an in-memory, `io.Pipe`-based fake transport used
   throughout the unit test suite. Reach for this for any test that doesn't need to prove real
   OS-level I/O.
2. **A real `socat`-linked PTY pair** — genuine kernel serial-line I/O, without a physical device:

   ```sh
   brew install socat   # macOS; most Linux distros ship it in their package manager

   socat -d -d pty,raw,echo=0,link=/tmp/serialforge-a pty,raw,echo=0,link=/tmp/serialforge-b
   ```

   Anything written to one link appears on the other, standing in for a serial cable. Point
   SerialForge at `/tmp/serialforge-a` (CLI `--port`, or the TUI's Devices tab — press `m` for the
   Virtual/Manual endpoints picker, which discovers a `socat`-created link automatically under
   "Friendly symlinks"). `scripts/pty-dev-test.sh` wraps this into a ready-to-run automated smoke
   check (send/receive round trip) or, with `--manual`, sets up the pair and prints instructions
   for interactive testing:

   ```sh
   scripts/pty-dev-test.sh            # automated send/receive smoke check
   scripts/pty-dev-test.sh --manual   # set up the PTY pair, print instructions, leave it running
   ```

   For manually verifying Saved Packets / hotkeys specifically (the automated tests above already
   cover this, but a real interactive pass is worth repeating after any change to
   `internal/tui/keybindings.go`, `savedpackets.go`, or `txrx.go`'s save/load/update code):

   1. Start the PTY pair above; connect to `/tmp/serialforge-a` from the TUI's Devices tab (`m` →
      pick it).
   2. Build a protocol in Designer, fill it in TX Builder, press `s` to save it with a hotkey (e.g.
      `'`).
   3. Quit (`q`), relaunch, confirm it's still there on the Saved subview.
   4. From Monitor (or any non-form screen), press the hotkey — confirm the exact expected bytes
      arrive on `/tmp/serialforge-b` (`xxd < /tmp/serialforge-b` or similar) and the footer shows
      `<key> → <name> · sent`. Press it several times — one complete packet per press.
   5. Open a text-entry form (e.g. a Designer field-name prompt) and press the same hotkey — confirm
      it's typed into the field, not sent; `esc` back to Navigation mode and confirm the hotkey
      sends again.
   6. Load the Saved Packet into TX Builder (`enter` from the Saved subview), edit a field, confirm
      the header shows `modified` and the *original* still transmits unchanged via its hotkey; press
      `u` to Update, confirm the new value now transmits.
   7. Try assigning a reserved key (e.g. `q`) or another packet's hotkey — confirm a specific
      rejection message, not a silent overwrite.

## Code style / conventions

- **No CLI framework.** `cmd/serialforge` dispatches manually (`main.go`'s `run(args)`, one
  `commands_*.go` file per command group). Keep new commands consistent with this rather than
  introducing a framework dependency.
- **No CLI flag-ordering assumptions.** Named flags (`--port`, `--baud`, ...) are canonical;
  a positional argument is always optional shorthand, never inferred from a fixed index like
  `args[0]`. See `ARCHITECTURE.md`'s "CLI argument-parsing invariant" — this was violated once and
  the fix (an explicit, order-independent parser) must not be undone.
- **One flat TUI model**, not a submodel-per-screen framework. `internal/tui/model.go` holds all
  cross-screen state directly; per-tab files provide `view*`/`update*` functions rather than their
  own model types.
- **The packet diagram has exactly one implementation** (`internal/tui.RenderDiagram`). Every
  screen that shows a packet's byte layout — designer, TX builder, RX inspector — calls it with
  the same `DiagramOptions` shape. Don't hand-roll a second box-drawing renderer for a new screen.
- **CRC logic lives only in `internal/checksum`**, and CRC algorithm *naming* lives only in
  `checksum.Definition.AlgorithmName`/`AlgorithmLabels`. No other package should compute a
  checksum or invent its own abbreviation of an algorithm's name.
- **PASS/FAIL is an RX-only claim** (a received byte compared against a recalculation). TX-side
  display shows the actual value plus AUTO/OVERRIDE, never PASS/FAIL — see `ARCHITECTURE.md`'s
  hard architectural invariants for the full reasoning.
- Keep architecture/status documentation in sync: when a change affects package layout,
  invariants, or what's actually implemented versus planned, update `ARCHITECTURE.md` in the same
  commit. Distinguish explicitly between implemented + tested / implemented but not
  hardware-tested / compile-tested only / scaffolded / planned — don't describe a scaffold as
  complete.

## Adding a CRC preset

Add the entry to `internal/checksum/presets.go` with its published `Check` value for the standard
`"123456789"` self-check vector, then let `TestPresetCheckVectors` (in `crc_test.go`) confirm it.
For 32/64-bit widths, also add a cross-check against Go's own `hash/crc32`/`hash/crc64` alongside
the existing ones in `crc_test.go` — verifying a CRC purely from its published parameters, without
an independent implementation to compare against, is exactly how a subtle transcription error
slips through.

## Reporting issues / opening PRs

Include the exact command or TUI flow that reproduces an issue, your platform (`go env GOOS
GOARCH`), and whether it involves real hardware, a `socat` PTY, or the fake transport. For
serial-transport or enumeration bugs, `ports --detailed --json` output is usually the fastest way
to show what SerialForge actually saw.
