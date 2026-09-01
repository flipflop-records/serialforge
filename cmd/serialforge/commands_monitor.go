package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/vtemnyakov/serialforge/internal/framing"
	"github.com/vtemnyakov/serialforge/internal/session"
)

var monitorDefs = []flagDef{
	{names: []string{"--port", "--path"}, takesValue: true},
	{names: []string{"--baud"}, takesValue: true},
	{names: []string{"--hex"}, takesValue: false},
	{names: []string{"--ascii"}, takesValue: false},
	{names: []string{"--both"}, takesValue: false},
}

const monitorHelp = `usage: serialforge monitor --port <path> [--baud N] [--hex|--ascii|--both]
   or: serialforge monitor <device> [--baud N] [--hex|--ascii|--both]   (positional shorthand)

Stream RX/TX bytes from a serial device to stdout until interrupted
(Ctrl+C). No hardware/USB metadata is required — a saved alias, a real OS
path, or a manual/virtual path (a socat PTY link) all work identically.

Flags:
  --port, --path <path>   device: a saved alias (see 'device add') or a
                           literal OS serial path. Aliases for the same
                           value; giving both with different values is an
                           error.
  --baud <n>               overrides the connection's baud rate. Without
                           it: saved profile's baud, else the app config
                           default, else the built-in default (115200).
  --hex                    show bytes as hex only
  --ascii                  show bytes as ASCII only (non-printable as '.')
  --both                   show both (default)
  --help, -h               show this help

Positional shorthand: a single non-flag argument is the same as --port —
convenient, but it conflicts (hard error) with an explicit --port/--path
given at the same time; use one or the other, not both.

Examples:
  serialforge monitor --port /tmp/serialforge-a --hex
  serialforge monitor --baud 921600 --port /tmp/serialforge-a --hex
  serialforge monitor fpga --both
`

func cmdMonitor(g globalFlags, args []string) error {
	if wantsHelp(args) {
		fmt.Print(monitorHelp)
		return nil
	}
	parsed, err := parseArgs(args, monitorDefs)
	if err != nil {
		return fmt.Errorf("%w\n\n%s", err, monitorHelp)
	}
	deviceArg, overrideBaud, err := resolveDeviceArg(parsed)
	if err != nil {
		return err
	}

	modesGiven := 0
	for _, m := range []string{"--hex", "--ascii", "--both"} {
		if parsed.has(m) {
			modesGiven++
		}
	}
	if modesGiven > 1 {
		return fmt.Errorf("choose only one of --hex/--ascii/--both")
	}
	mode := "both"
	switch {
	case parsed.has("--hex"):
		mode = "hex"
	case parsed.has("--ascii"):
		mode = "ascii"
	}

	path, cfg, err := resolveDevice(g, deviceArg, overrideBaud)
	if err != nil {
		return err
	}

	f, _ := framing.New(framing.KindRaw, framing.Options{})
	sess, err := openSession(path, cfg, f)
	if err != nil {
		return err
	}
	defer sess.Close()

	fmt.Fprintf(os.Stderr, "Connected %s @ %d %s — Ctrl+C to stop\n", path, cfg.Baud, cfg.FrameString())

	ctx, cancel := contextWithSignal()
	defer cancel()
	sess.Start(ctx)

	for {
		select {
		case e, ok := <-sess.Events():
			if !ok {
				return nil
			}
			printEvent(e, mode)
		case <-ctx.Done():
			return nil
		}
	}
}

// resolveDeviceArg applies the device-resolution rule shared by monitor and
// (its no-payload half) send: --port/--path is canonical; a single
// positional argument is accepted as shorthand; both at once is a hard
// conflict error rather than a silent guess (product spec, "prefer
// rejecting conflicting duplicate specification because it is safer").
// overrideBaud is nil when --baud wasn't given, so resolveDevice's
// precedence chain can tell "not specified" apart from "explicitly 0".
func resolveDeviceArg(parsed parsedArgs) (deviceArg string, overrideBaud *int, err error) {
	port, portGiven, err := parsed.single("--port", "--port/--path")
	if err != nil {
		return "", nil, err
	}
	switch {
	case portGiven && len(parsed.positionals) > 0:
		return "", nil, fmt.Errorf("conflicting device: both positional argument %q and --port/--path %q given — use only one", parsed.positionals[0], port)
	case portGiven:
		deviceArg = port
	case len(parsed.positionals) == 1:
		deviceArg = parsed.positionals[0]
	case len(parsed.positionals) == 0:
		return "", nil, fmt.Errorf("no device given — pass --port <path> (or a saved alias), or a positional device argument")
	default:
		return "", nil, fmt.Errorf("too many positional arguments: %v", parsed.positionals)
	}

	if baudStr, ok, err := parsed.single("--baud", "--baud"); err != nil {
		return "", nil, err
	} else if ok {
		b, err := strconv.Atoi(baudStr)
		if err != nil {
			return "", nil, fmt.Errorf("--baud: %w", err)
		}
		overrideBaud = &b
	}
	return deviceArg, overrideBaud, nil
}

func printEvent(e session.Event, mode string) {
	ts := e.Timestamp.Format("15:04:05.000")
	switch e.Kind {
	case session.EventRX:
		fmt.Printf("%s RX %s\n", ts, formatByMode(e.Data, mode))
	case session.EventTX:
		fmt.Printf("%s TX %s\n", ts, formatByMode(e.Data, mode))
	case session.EventStatus:
		msg := e.Status
		if e.Err != nil {
			msg += ": " + e.Err.Error()
		}
		fmt.Fprintf(os.Stderr, "%s -- %s\n", ts, msg)
	}
}

func formatByMode(data []byte, mode string) string {
	switch mode {
	case "hex":
		return formatHexBytes(data)
	case "ascii":
		return printableASCII(data)
	default:
		return formatHexBytes(data) + "   " + printableASCII(data)
	}
}

func printableASCII(b []byte) string {
	out := make([]byte, len(b))
	for i, c := range b {
		if c >= 0x20 && c < 0x7F {
			out[i] = c
		} else {
			out[i] = '.'
		}
	}
	return string(out)
}

var sendDefs = []flagDef{
	{names: []string{"--port", "--path"}, takesValue: true},
	{names: []string{"--baud"}, takesValue: true},
	{names: []string{"--hex"}, takesValue: false},
	{names: []string{"--text"}, takesValue: false},
}

const sendHelp = `usage: serialforge send --port <path> --hex <bytes> [--baud N]
   or: serialforge send --port <path> --text <string> [--baud N]
   or: serialforge send <device> <payload> [--hex|--text]   (positional shorthand)

Send one payload to a serial device and exit. No hardware/USB metadata is
required — a saved alias, a real OS path, or a manual/virtual path (a
socat PTY link) all work identically.

Flags:
  --port, --path <path>   device: a saved alias or a literal OS path.
                           Aliases for the same value; conflicting values
                           between the two is an error.
  --baud <n>               overrides the connection's baud rate (same
                           precedence as 'monitor' — see its --help).
  --hex                    payload mode: bytes as hex, e.g. "AA 55 02"
  --text                   payload mode: bytes as literal text (default
                           if neither --hex nor --text is given)
  --help, -h               show this help

The payload is always a positional argument — one is required. With
--port/--path given, exactly one positional (the payload) is expected;
without it, exactly two (device, then payload) — that's the positional-
shorthand form. Mixing a positional device with an explicit --port/--path
is a conflict error, not a silent guess. --hex and --text together is
also an error — pick one.

Examples:
  serialforge send --port /tmp/serialforge-a --hex "AA 55 02 00 C0 17 FF 00 80"
  serialforge send --hex "AA 55 02 00 C0 17 FF 00 80" --port /tmp/serialforge-a
  serialforge send --baud 921600 --port /tmp/serialforge-a --hex "AA 55"
  serialforge send /tmp/serialforge-a "AA 55" --hex
  serialforge send fpga "hello"
`

func cmdSend(g globalFlags, args []string) error {
	if wantsHelp(args) {
		fmt.Print(sendHelp)
		return nil
	}
	parsed, err := parseArgs(args, sendDefs)
	if err != nil {
		return fmt.Errorf("%w\n\n%s", err, sendHelp)
	}

	mode, err := resolveSendMode(parsed)
	if err != nil {
		return err
	}

	deviceArg, payloadArg, err := resolveSendArgs(parsed)
	if err != nil {
		return err
	}

	var data []byte
	if mode == "hex" {
		data, err = parseHexArg(payloadArg)
		if err != nil {
			return fmt.Errorf("--hex payload: %w", err)
		}
	} else {
		data = []byte(payloadArg)
	}
	if len(data) == 0 {
		return fmt.Errorf("nothing to send (empty payload)")
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

	f, _ := framing.New(framing.KindRaw, framing.Options{})
	sess, err := openSession(path, cfg, f)
	if err != nil {
		return err
	}
	defer sess.Close()

	ctx, cancel := contextWithSignal()
	defer cancel()
	sess.Start(ctx)

	if _, err := sess.Send(data); err != nil {
		return err
	}
	fmt.Printf("Connected %s @ %d %s\n", path, cfg.Baud, cfg.FrameString())
	fmt.Printf("sent %d bytes: %s\n", len(data), formatHexBytes(data))
	return nil
}

// resolveSendMode returns "hex" or "text" (the default — product spec §4,
// "if no mode is supplied, choose and document a sensible default"),
// erroring if both --hex and --text were given: an unambiguous conflict,
// not a fragile argv-position guess.
func resolveSendMode(parsed parsedArgs) (string, error) {
	if parsed.has("--hex") && parsed.has("--text") {
		return "", fmt.Errorf("choose only one of --hex/--text")
	}
	if parsed.has("--hex") {
		return "hex", nil
	}
	return "text", nil
}

// resolveSendArgs resolves (device, payload) from parsed per send's
// documented contract — see sendHelp. The payload is always positional;
// device is either --port/--path or the first of two positionals.
func resolveSendArgs(parsed parsedArgs) (deviceArg, payloadArg string, err error) {
	port, portGiven, err := parsed.single("--port", "--port/--path")
	if err != nil {
		return "", "", err
	}
	n := len(parsed.positionals)
	switch {
	case portGiven:
		switch n {
		case 0:
			return "", "", fmt.Errorf("no payload given — pass it as a positional argument along with --hex or --text")
		case 1:
			return port, parsed.positionals[0], nil
		default:
			return "", "", fmt.Errorf("too many positional arguments: %v (device was given via --port/--path, so exactly one positional payload is expected)", parsed.positionals)
		}
	default:
		switch n {
		case 0:
			return "", "", fmt.Errorf("no device or payload given — pass --port <path> plus a payload, or 'serialforge send <device> <payload>'")
		case 1:
			return "", "", fmt.Errorf("a payload is required in addition to the device — pass --port explicitly with one positional payload, or give both '<device> <payload>' positionally")
		case 2:
			return parsed.positionals[0], parsed.positionals[1], nil
		default:
			return "", "", fmt.Errorf("too many positional arguments: %v", parsed.positionals)
		}
	}
}

// contextWithSignal returns a context canceled on Ctrl+C/SIGTERM, for
// commands (monitor, send) that hold an open session for the process's
// lifetime — the same cancellation path session.Session.Start expects
// everywhere else.
func contextWithSignal() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-c:
			cancel()
		case <-ctx.Done():
		}
		signal.Stop(c)
	}()
	return ctx, cancel
}
