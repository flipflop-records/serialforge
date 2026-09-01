package main

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/vtemnyakov/serialforge/internal/config"
	"github.com/vtemnyakov/serialforge/internal/packet"
	"github.com/vtemnyakov/serialforge/internal/protocol"
)

func resolveSchema(g globalFlags, args []string) (packet.Schema, error) {
	name, ok := flagValue(args, "--protocol")
	if !ok {
		return packet.Schema{}, fmt.Errorf("--protocol NAME is required")
	}
	dir, err := config.Dir(g.configPath)
	if err != nil {
		return packet.Schema{}, err
	}
	store, err := protocol.Load(dir)
	if err != nil {
		return packet.Schema{}, err
	}
	sc, ok := store.Get(name)
	if !ok {
		return packet.Schema{}, fmt.Errorf("no protocol profile named %q (see `serialforge protocol list`)", name)
	}
	return sc, nil
}

// parseFieldFlags collects every "--field NAME=HEX" pair in args.
func parseFieldFlags(args []string) map[string]string {
	fields := map[string]string{}
	for i, a := range args {
		if a != "--field" || i+1 >= len(args) {
			continue
		}
		kv := args[i+1]
		name, hexVal, found := strings.Cut(kv, "=")
		if !found {
			continue
		}
		fields[name] = hexVal
	}
	return fields
}

const packetHelp = `usage: serialforge packet build --protocol NAME --field NAME=HEX... [--crc-override HEX] [--json]
   or: serialforge packet decode --protocol NAME --hex "AA 55 ..." [--json]

Build or decode one packet against a saved protocol schema (see
'serialforge protocol list'/'protocol import') — the same schema the TUI's
Designer/TX Builder/RX Inspector use, so a packet built here decodes
identically there and vice versa.

build:
  --protocol <name>        required — the schema to build against
  --field <name>=<hex>      repeatable — one per schema field, e.g.
                            --field command=02 --field address=00C017FF
  --crc-override <hex>      optional — send this CRC instead of the AUTO-
                            computed one, for fault-injection testing
  --json                    machine-readable output (global flag)

decode:
  --protocol <name>        required
  --hex "<bytes>"           required — the raw packet bytes to decode
  --json                    machine-readable output (global flag)

Examples:
  serialforge packet build --protocol uart-demo --field header=AA55 \
    --field command=02 --field address=00C017FF --field payload=000000000001
  serialforge packet decode --protocol uart-demo --hex "AA 55 02 00 C0 17 FF 00 00 00 00 00 01 47"
`

func cmdPacket(g globalFlags, args []string) error {
	if len(args) == 0 || wantsHelp(args) {
		fmt.Print(packetHelp)
		return nil
	}
	sub, rest := args[0], args[1:]

	switch sub {
	case "build":
		return cmdPacketBuild(g, rest)
	case "decode":
		return cmdPacketDecode(g, rest)
	default:
		return fmt.Errorf("unknown packet subcommand %q\n\n%s", sub, packetHelp)
	}
}

func cmdPacketBuild(g globalFlags, args []string) error {
	sc, err := resolveSchema(g, args)
	if err != nil {
		return err
	}
	if err := sc.Validate(); err != nil {
		return fmt.Errorf("protocol %q is not a valid schema: %w", sc.Name, err)
	}

	values := packet.Values{}
	for name, hexStr := range parseFieldFlags(args) {
		f, _, ok := sc.FieldByName(name)
		if !ok {
			return fmt.Errorf("schema %q has no field %q", sc.Name, name)
		}
		raw, err := hex.DecodeString(cleanHex(hexStr))
		if err != nil {
			return fmt.Errorf("field %q: %w", name, err)
		}
		if len(raw) != f.Size {
			return fmt.Errorf("field %q: got %d bytes, want %d", name, len(raw), f.Size)
		}
		values[name] = raw
	}

	var crcOverride *uint64
	if s, ok := flagValue(args, "--crc-override"); ok {
		raw, err := hex.DecodeString(cleanHex(s))
		if err != nil {
			return fmt.Errorf("--crc-override: %w", err)
		}
		var v uint64
		for _, b := range raw {
			v = v<<8 | uint64(b)
		}
		crcOverride = &v
	}

	pkt, err := packet.Build(sc, values, crcOverride)
	if err != nil {
		return err
	}

	if g.json {
		return printJSON(jsonPacket(pkt))
	}
	fmt.Println(formatHexBytes(pkt.Raw))
	if pkt.CRC != nil {
		note := ""
		if pkt.CRC.Overridden {
			note = "  (manually overridden — does not match AUTO)"
		}
		fmt.Printf("CRC: calc=0x%X sent=0x%X%s\n", pkt.CRC.Calculated, pkt.CRC.Received, note)
	}
	return nil
}

func cmdPacketDecode(g globalFlags, args []string) error {
	sc, err := resolveSchema(g, args)
	if err != nil {
		return err
	}
	hexStr, ok := flagValue(args, "--hex")
	if !ok {
		return fmt.Errorf("--hex \"AA 55 ...\" is required")
	}
	raw, err := hex.DecodeString(cleanHex(hexStr))
	if err != nil {
		return fmt.Errorf("--hex: %w", err)
	}
	pkt, err := packet.Decode(sc, raw)
	if err != nil {
		return err
	}
	if g.json {
		return printJSON(jsonPacket(pkt))
	}
	fmt.Printf("%s  (%d bytes)\n", sc.Name, len(pkt.Raw))
	for _, fv := range pkt.Fields {
		fmt.Printf("  %-16s %s", fv.Field.Name, formatHexBytes(fv.Raw))
		if fv.UintOK {
			fmt.Printf("   = %d (0x%X)", fv.Uint, fv.Uint)
		}
		fmt.Println()
	}
	if pkt.CRC != nil {
		status := "PASS"
		if !pkt.CRC.Valid {
			status = "FAIL"
		}
		fmt.Printf("  CRC              calc=0x%X rx=0x%X  %s\n", pkt.CRC.Calculated, pkt.CRC.Received, status)
	}
	return nil
}

// jsonPacket is packet.Packet flattened into a JSON-friendly shape (raw
// bytes and per-field raw bytes as hex strings rather than byte arrays).
func jsonPacket(pkt *packet.Packet) map[string]any {
	fields := make([]map[string]any, len(pkt.Fields))
	for i, fv := range pkt.Fields {
		m := map[string]any{
			"name":  fv.Field.Name,
			"hex":   formatHexBytes(fv.Raw),
			"ascii": fv.ASCII,
		}
		if fv.UintOK {
			m["uint"] = fv.Uint
		}
		if fv.IntOK {
			m["int"] = fv.Int
		}
		fields[i] = m
	}
	out := map[string]any{
		"schema": pkt.Schema.Name,
		"raw":    formatHexBytes(pkt.Raw),
		"fields": fields,
	}
	if pkt.CRC != nil {
		out["crc"] = map[string]any{
			"width":      pkt.CRC.Width,
			"calculated": fmt.Sprintf("%X", pkt.CRC.Calculated),
			"received":   fmt.Sprintf("%X", pkt.CRC.Received),
			"valid":      pkt.CRC.Valid,
			"manual":     pkt.CRC.Manual,
			"overridden": pkt.CRC.Overridden,
		}
	}
	return out
}

func formatHexBytes(b []byte) string {
	parts := make([]string, len(b))
	for i, v := range b {
		parts[i] = fmt.Sprintf("%02X", v)
	}
	return strings.Join(parts, " ")
}

func cleanHex(s string) string {
	return strings.NewReplacer(" ", "", "0x", "", "0X", "", ",", "", "\t", "", "\n", "").Replace(s)
}
