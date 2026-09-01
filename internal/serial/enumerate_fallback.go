//go:build darwin && !cgo

package serial

import gobugst "go.bug.st/serial"

// go.bug.st/serial/enumerator's darwin backend calls IOKit via cgo and does
// not even compile with CGO_ENABLED=0 (confirmed by cross-compile probe —
// see ARCHITECTURE.md "Serial engine"). A forced-no-cgo darwin build therefore
// gets path-only enumeration here instead of failing to build at all; a
// normal native darwin build (cgo on by default) uses the full
// enumerator-backed implementation in enumerate_enumerator.go.

func nativeList() ([]string, error) {
	return gobugst.GetPortsList()
}

func nativeListDetailed() ([]PortInfo, error) {
	paths, err := gobugst.GetPortsList()
	if err != nil {
		return nil, err
	}
	out := make([]PortInfo, 0, len(paths))
	for _, p := range paths {
		out = append(out, PortInfo{Path: p})
	}
	return out, nil
}
