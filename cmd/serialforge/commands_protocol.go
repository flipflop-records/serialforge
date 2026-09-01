package main

import (
	"fmt"
	"os"

	"github.com/vtemnyakov/serialforge/internal/config"
	"github.com/vtemnyakov/serialforge/internal/device"
	"github.com/vtemnyakov/serialforge/internal/protocol"
)

func cmdProtocol(g globalFlags, args []string) error {
	dir, err := config.Dir(g.configPath)
	if err != nil {
		return err
	}
	store, err := protocol.Load(dir)
	if err != nil {
		return err
	}

	if len(args) == 0 {
		return fmt.Errorf("usage: serialforge protocol list|show|import|export|delete|clone|rename <args>")
	}
	sub, rest := args[0], args[1:]

	switch sub {
	case "list":
		names := store.Names()
		if g.json {
			return printJSON(names)
		}
		if len(names) == 0 {
			fmt.Println("no protocol profiles saved yet — see examples/protocols/uart-demo.yaml")
			return nil
		}
		for _, n := range names {
			fmt.Println(n)
		}
		return nil

	case "show":
		if len(rest) < 1 {
			return fmt.Errorf("usage: serialforge protocol show <name>")
		}
		sc, ok := store.Get(rest[0])
		if !ok {
			return fmt.Errorf("no protocol profile named %q", rest[0])
		}
		if g.json {
			return printJSON(sc)
		}
		data, err := store.Export(rest[0])
		if err != nil {
			return err
		}
		os.Stdout.Write(data)
		return nil

	case "import":
		if len(rest) < 1 {
			return fmt.Errorf("usage: serialforge protocol import <file.yaml>")
		}
		data, err := os.ReadFile(rest[0])
		if err != nil {
			return err
		}
		sc, err := store.Import(data)
		if err != nil {
			return err
		}
		if err := store.Save(); err != nil {
			return err
		}
		fmt.Printf("imported %q (%d bytes, %d fields)\n", sc.Name, sc.TotalSize, len(sc.Fields))
		return nil

	case "export":
		if len(rest) < 1 {
			return fmt.Errorf("usage: serialforge protocol export <name>")
		}
		data, err := store.Export(rest[0])
		if err != nil {
			return err
		}
		os.Stdout.Write(data)
		return nil

	case "delete":
		if len(rest) < 1 {
			return fmt.Errorf("usage: serialforge protocol delete <name>")
		}
		if !store.Delete(rest[0]) {
			return fmt.Errorf("no protocol profile named %q", rest[0])
		}
		return store.Save()

	case "clone":
		if len(rest) < 2 {
			return fmt.Errorf("usage: serialforge protocol clone <name> <new-name>")
		}
		if err := store.Clone(rest[0], rest[1]); err != nil {
			return err
		}
		return store.Save()

	case "rename":
		if len(rest) < 2 {
			return fmt.Errorf("usage: serialforge protocol rename <name> <new-name>")
		}
		if err := store.Rename(rest[0], rest[1]); err != nil {
			return err
		}
		return store.Save()

	default:
		return fmt.Errorf("unknown protocol subcommand %q", sub)
	}
}

const deviceHelp = `usage: serialforge device list
   or: serialforge device show <alias>
   or: serialforge device add --alias NAME [--vid HEX] [--pid HEX] [--serial SN] [--path PATH] [--baud N]
   or: serialforge device delete <alias>
   or: serialforge device rename <alias> <new-alias>
   or: serialforge device clone <alias> <new-alias>

Saved device profiles — a stable name for a physical or manual/virtual
serial port. VID/PID are optional: a profile identified only by --path
(no USB identity at all) is exactly how a manual/virtual path (a socat
PTY link) gets a reusable alias — see 'serialforge monitor --help'.

add flags:
  --alias <name>      required
  --vid, --pid <hex>  USB identity, optional
  --serial <sn>       USB serial number, optional
  --path <path>       a literal OS path this profile always resolves to
                      first if still present, optional
  --baud <n>          default 115200 if not given (see the serial-setting
                      precedence in 'serialforge monitor --help')

Examples:
  serialforge device add --alias fpga --vid 0403 --pid 6010 --baud 115200
  serialforge device add --alias virtual --path /tmp/serialforge-a --baud 115200
  serialforge device list
`

func cmdDevice(g globalFlags, args []string) error {
	if len(args) == 0 || wantsHelp(args) {
		fmt.Print(deviceHelp)
		return nil
	}
	dir, err := config.Dir(g.configPath)
	if err != nil {
		return err
	}
	store, err := device.Load(dir)
	if err != nil {
		return err
	}

	sub, rest := args[0], args[1:]

	switch sub {
	case "list":
		profiles := store.All()
		if g.json {
			return printJSON(profiles)
		}
		if len(profiles) == 0 {
			fmt.Println("no device profiles saved yet")
			return nil
		}
		for _, p := range profiles {
			fmt.Printf("%s\tvid=%s pid=%s baud=%d\n", p.Alias, p.VID, p.PID, p.SerialConfig().Baud)
		}
		return nil

	case "show":
		if len(rest) < 1 {
			return fmt.Errorf("usage: serialforge device show <alias>")
		}
		p, ok := store.Get(rest[0])
		if !ok {
			return fmt.Errorf("no device profile named %q", rest[0])
		}
		return printJSON(p)

	case "add":
		alias, _ := flagValue(rest, "--alias")
		if alias == "" {
			return fmt.Errorf("usage: serialforge device add --alias NAME [--vid HEX] [--pid HEX] [--serial SN] [--path PATH] [--baud N]")
		}
		p := device.Profile{Alias: alias}
		p.VID, _ = flagValue(rest, "--vid")
		p.PID, _ = flagValue(rest, "--pid")
		p.SerialNumber, _ = flagValue(rest, "--serial")
		p.Path, _ = flagValue(rest, "--path")
		if baud, ok := flagValue(rest, "--baud"); ok {
			fmt.Sscanf(baud, "%d", &p.Baud)
		}
		if err := store.Put(p); err != nil {
			return err
		}
		return store.Save()

	case "delete":
		if len(rest) < 1 {
			return fmt.Errorf("usage: serialforge device delete <alias>")
		}
		if !store.Delete(rest[0]) {
			return fmt.Errorf("no device profile named %q", rest[0])
		}
		return store.Save()

	case "rename":
		if len(rest) < 2 {
			return fmt.Errorf("usage: serialforge device rename <alias> <new-alias>")
		}
		if err := store.Rename(rest[0], rest[1]); err != nil {
			return err
		}
		return store.Save()

	case "clone":
		if len(rest) < 2 {
			return fmt.Errorf("usage: serialforge device clone <alias> <new-alias>")
		}
		if err := store.Clone(rest[0], rest[1]); err != nil {
			return err
		}
		return store.Save()

	default:
		return fmt.Errorf("unknown device subcommand %q", sub)
	}
}
