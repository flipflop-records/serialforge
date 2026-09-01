//go:build !(darwin && !cgo)

package serial

import (
	gobugst "go.bug.st/serial"
	"go.bug.st/serial/enumerator"
)

// This build covers everything except a forced CGO_ENABLED=0 build
// targeting darwin: linux and windows use go.bug.st/serial/enumerator's
// pure-Go metadata backends, and a native darwin build (cgo on by default)
// uses its IOKit-backed one. See ARCHITECTURE.md "Serial engine" for how
// this was verified (cross-compile probes for all five release targets).

func nativeList() ([]string, error) {
	return gobugst.GetPortsList()
}

func nativeListDetailed() ([]PortInfo, error) {
	// enumerator.All enables active USB probing for every device so
	// Manufacturer/Product get populated — without at least one filter the
	// library probes nothing and those fields come back empty even when
	// available (see enumerator.GetDetailedPortsList's doc comment).
	details, err := enumerator.GetDetailedPortsList(enumerator.All)
	if err != nil {
		return nil, err
	}
	out := make([]PortInfo, 0, len(details))
	for _, d := range details {
		out = append(out, PortInfo{
			Path:         d.Name,
			IsUSB:        d.IsUSB,
			VID:          d.VID,
			PID:          d.PID,
			SerialNumber: d.SerialNumber,
			Manufacturer: d.Manufacturer,
			Product:      d.Product,
		})
	}
	return out, nil
}
