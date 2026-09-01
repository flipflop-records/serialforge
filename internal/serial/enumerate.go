package serial

// PortInfo describes one discovered serial port. Metadata fields are
// best-effort: a plain UART, or a platform build without the metadata
// backend (see enumerate_fallback.go), leaves them empty rather than
// guessing — device.Matcher treats an empty field as "unknown", not "".
type PortInfo struct {
	Path         string // OS device path/name: "/dev/cu.usbserial-1410", "COM3"
	IsUSB        bool
	VID          string // 4 hex digits, e.g. "0403"
	PID          string // 4 hex digits, e.g. "6010"
	SerialNumber string
	Manufacturer string
	Product      string
}

// nativeList and nativeListDetailed are provided per-platform/build-tag —
// see enumerate_enumerator.go (the common case: pure-Go on linux/windows,
// cgo-backed IOKit on darwin) and enumerate_fallback.go (forced
// CGO_ENABLED=0 on darwin: path-only, no metadata — see ARCHITECTURE.md
// "Serial engine").

// List returns every serial port path the OS reports, with no metadata.
func List() ([]string, error) {
	return nativeList()
}

// ListDetailed returns every serial port with whatever metadata the
// current platform/build can provide (see PortInfo's doc comment on when
// fields are left empty).
func ListDetailed() ([]PortInfo, error) {
	return nativeListDetailed()
}
