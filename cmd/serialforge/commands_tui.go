package main

import (
	"github.com/vtemnyakov/serialforge/internal/config"
	"github.com/vtemnyakov/serialforge/internal/device"
	"github.com/vtemnyakov/serialforge/internal/protocol"
	"github.com/vtemnyakov/serialforge/internal/tui"
)

func cmdTUI(g globalFlags, args []string) error {
	dir, err := config.Dir(g.configPath)
	if err != nil {
		return err
	}
	appCfg, err := config.LoadApp(dir)
	if err != nil {
		return err
	}
	devices, err := device.Load(dir)
	if err != nil {
		return err
	}
	protocols, err := protocol.Load(dir)
	if err != nil {
		return err
	}
	recent, err := device.LoadRecent(dir)
	if err != nil {
		return err
	}
	return tui.Run(tui.RunConfig{
		ConfigDir: dir,
		App:       appCfg,
		Devices:   devices,
		Protocols: protocols,
		Recent:    recent,
		Version:   Version,
	})
}
