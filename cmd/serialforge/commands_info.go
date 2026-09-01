package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/vtemnyakov/serialforge/internal/config"
	"github.com/vtemnyakov/serialforge/internal/serial"
)

func cmdConfig(g globalFlags, args []string) error {
	dir, err := config.Dir(g.configPath)
	if err != nil {
		return err
	}
	if len(args) == 0 || args[0] == "path" {
		if g.json {
			return printJSON(map[string]string{"config_dir": dir})
		}
		fmt.Println(dir)
		return nil
	}
	return fmt.Errorf("usage: serialforge config path")
}

func cmdPorts(g globalFlags, args []string) error {
	detailed := hasFlag(args, "--detailed")
	if !detailed && !g.json {
		// Plain listing is more useful with detail by default when it's
		// for a human; --json without --detailed still gives paths only,
		// matching what was actually asked for.
		detailed = true
	}

	if detailed {
		ports, err := serial.ListDetailed()
		if err != nil {
			return fmt.Errorf("list ports: %w", err)
		}
		if g.json {
			return printJSON(ports)
		}
		if len(ports) == 0 {
			fmt.Println("no serial ports found")
			return nil
		}
		for _, p := range ports {
			label := p.Path
			if p.VID != "" || p.PID != "" {
				label += fmt.Sprintf("  VID:PID=%s:%s", p.VID, p.PID)
			}
			if p.Manufacturer != "" || p.Product != "" {
				label += fmt.Sprintf("  %s %s", p.Manufacturer, p.Product)
			}
			if p.SerialNumber != "" {
				label += fmt.Sprintf("  SN=%s", p.SerialNumber)
			}
			fmt.Println(label)
		}
		return nil
	}

	paths, err := serial.List()
	if err != nil {
		return fmt.Errorf("list ports: %w", err)
	}
	if g.json {
		return printJSON(paths)
	}
	for _, p := range paths {
		fmt.Println(p)
	}
	return nil
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name {
			return true
		}
	}
	return false
}

func flagValue(args []string, name string) (string, bool) {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1], true
		}
		if v, ok := trimFlagPrefix(a, name+"="); ok {
			return v, true
		}
	}
	return "", false
}

func trimFlagPrefix(a, prefix string) (string, bool) {
	if len(a) > len(prefix) && a[:len(prefix)] == prefix {
		return a[len(prefix):], true
	}
	return "", false
}
