package main

import (
	"fmt"

	"github.com/vtemnyakov/serialforge/internal/config"
	"github.com/vtemnyakov/serialforge/internal/framing"
	"github.com/vtemnyakov/serialforge/internal/protocol"
	"github.com/vtemnyakov/serialforge/internal/savedpacket"
)

const savedHelp = `usage: serialforge saved list [--json]
   or: serialforge saved show <name> [--json]
   or: serialforge saved send <name> [--port <path>|--path <path>] [--baud N] [--json]
   or: serialforge saved delete <name>

Saved Packets: a Protocol Profile reference + concrete field values + CRC
mode, saved once (from the TUI's TX Builder, 's') and sent headlessly here
— the exact same build path as the TUI's Saved Packets screen and hotkeys
(internal/savedpacket.SavedPacket.Build), so 'saved send' produces
byte-identical output to pressing the packet's hotkey in the TUI.

send flags:
  --port, --path <path>   device: a saved alias (see 'device add') or a
                           literal OS serial path. Required (a saved
                           packet needs somewhere to send to); positional
                           shorthand also works, same as 'monitor'/'send'.
  --baud <n>               overrides the connection's baud rate (same
                           precedence as 'monitor' — see its --help).
  --json                    machine-readable output (global flag; list/show
                           also support it)

Examples:
  serialforge saved list
  serialforge saved show get-status
  serialforge saved send get-status --port /tmp/serialforge-a
  serialforge saved send get-status /tmp/serialforge-a
  serialforge saved delete get-status
`

func cmdSaved(g globalFlags, args []string) error {
	if len(args) == 0 || wantsHelp(args) {
		fmt.Print(savedHelp)
		return nil
	}
	sub, rest := args[0], args[1:]

	dir, err := config.Dir(g.configPath)
	if err != nil {
		return err
	}
	saved, err := savedpacket.Load(dir)
	if err != nil {
		return err
	}

	switch sub {
	case "list":
		return cmdSavedList(g, saved)
	case "show":
		return cmdSavedShow(g, saved, dir, rest)
	case "send":
		return cmdSavedSend(g, saved, dir, rest)
	case "delete":
		return cmdSavedDelete(rest, saved)
	default:
		return fmt.Errorf("unknown saved subcommand %q\n\n%s", sub, savedHelp)
	}
}

func cmdSavedList(g globalFlags, saved *savedpacket.Store) error {
	all := saved.All()
	if g.json {
		return printJSON(all)
	}
	if len(all) == 0 {
		fmt.Println("no saved packets yet — see 's' (Save packet) in the TUI's TX Builder")
		return nil
	}
	for _, sp := range all {
		hotkey := "-"
		if sp.Hotkey != "" {
			hotkey = sp.Hotkey
		}
		fmt.Printf("%-24s hotkey=%-4s protocol=%s\n", sp.Name, hotkey, sp.Protocol)
	}
	return nil
}

func cmdSavedShow(g globalFlags, saved *savedpacket.Store, dir string, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: serialforge saved show <name>")
	}
	sp, ok := saved.Get(args[0])
	if !ok {
		return fmt.Errorf("no saved packet named %q", args[0])
	}
	protocols, err := protocol.Load(dir)
	if err != nil {
		return err
	}
	res := sp.Resolve(protocols)

	if g.json {
		out := map[string]any{"saved_packet": sp, "status": res.Status}
		if len(res.Problems) > 0 {
			problems := make([]string, len(res.Problems))
			for i, p := range res.Problems {
				problems[i] = p.String()
			}
			out["problems"] = problems
		}
		return printJSON(out)
	}

	fmt.Printf("%-10s %s\n", "Name", sp.Name)
	fmt.Printf("%-10s %s\n", "Protocol", sp.Protocol)
	hotkey := "(none)"
	if sp.Hotkey != "" {
		hotkey = sp.Hotkey
	}
	fmt.Printf("%-10s %s\n", "Hotkey", hotkey)
	fmt.Printf("%-10s %s\n", "CRC mode", sp.CRCMode)
	fmt.Println()
	switch res.Status {
	case savedpacket.StatusProtocolMissing:
		fmt.Println("protocol missing:", sp.Protocol)
		return nil
	case savedpacket.StatusProtocolInvalid:
		fmt.Println("protocol schema invalid:", res.Err())
		return nil
	case savedpacket.StatusIncompatible:
		fmt.Println("incompatible with the current protocol:")
		for _, p := range res.Problems {
			fmt.Println("  -", p.String())
		}
		return nil
	}
	for _, f := range res.Schema.Fields {
		fmt.Printf("  %-16s %s\n", f.Name, sp.Values[f.Name])
	}
	if pkt, err := sp.Build(protocols); err == nil {
		fmt.Println()
		fmt.Println(formatHexBytes(pkt.Raw))
		if pkt.CRC != nil {
			fmt.Printf("CRC: calc=0x%X sent=0x%X\n", pkt.CRC.Calculated, pkt.CRC.Received)
		}
	}
	return nil
}

func cmdSavedDelete(args []string, saved *savedpacket.Store) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: serialforge saved delete <name>")
	}
	if !saved.Delete(args[0]) {
		return fmt.Errorf("no saved packet named %q", args[0])
	}
	return saved.Save()
}

var savedSendDefs = []flagDef{
	{names: []string{"--port", "--path"}, takesValue: true},
	{names: []string{"--baud"}, takesValue: true},
}

// cmdSavedSend is the headless equivalent of pressing a Saved Packet's
// hotkey in the TUI: it resolves the same argv device-resolution rule
// monitor/send use (resolveDeviceArg), then builds and sends through
// SavedPacket.Build — no separate serialization path.
func cmdSavedSend(g globalFlags, saved *savedpacket.Store, dir string, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: serialforge saved send <name> [--port <path>] [--baud N]")
	}
	name := args[0]
	rest := args[1:]

	sp, ok := saved.Get(name)
	if !ok {
		return fmt.Errorf("no saved packet named %q", name)
	}

	parsed, err := parseArgs(rest, savedSendDefs)
	if err != nil {
		return fmt.Errorf("%w\n\n%s", err, savedHelp)
	}
	deviceArg, overrideBaud, err := resolveDeviceArg(parsed)
	if err != nil {
		return err
	}

	protocols, err := protocol.Load(dir)
	if err != nil {
		return err
	}
	pkt, err := sp.Build(protocols)
	if err != nil {
		return err
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

	if _, err := sess.Send(pkt.Raw); err != nil {
		return err
	}

	if g.json {
		out := map[string]any{
			"name": sp.Name, "device": path, "sent_bytes": len(pkt.Raw), "raw": formatHexBytes(pkt.Raw),
		}
		return printJSON(out)
	}
	fmt.Printf("Connected %s @ %d %s\n", path, cfg.Baud, cfg.FrameString())
	fmt.Printf("sent %s · %d bytes: %s\n", sp.Name, len(pkt.Raw), formatHexBytes(pkt.Raw))
	return nil
}
