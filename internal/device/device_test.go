package device

import (
	"strings"
	"testing"

	"github.com/vtemnyakov/serialforge/internal/serial"
)

func TestResolveByExactPath(t *testing.T) {
	p := Profile{Alias: "fpga", Path: "/dev/cu.usbserial-1410"}
	ports := []serial.PortInfo{
		{Path: "/dev/cu.usbserial-1410", VID: "0403", PID: "6010"},
		{Path: "/dev/cu.usbserial-9999", VID: "0403", PID: "6010"},
	}
	got, err := Resolve(p, ports)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Path != "/dev/cu.usbserial-1410" {
		t.Errorf("got %q, want the exact path match", got.Path)
	}
}

// TestResolveTrustsManualPathNotInEnumeration is the regression case for
// the actual bug reported: a profile that only carries an explicit Path
// (e.g. a socat PTY link, or /dev/ttys003 — neither matches go.bug.st/
// serial's darwin cu.*/tty.* enumeration pattern) must still resolve, even
// though it never appears in the enumerated ports list. Auto-discovery
// staying blind to it is correct and expected; Resolve refusing to open it
// anyway was the bug.
func TestResolveTrustsManualPathNotInEnumeration(t *testing.T) {
	p := Profile{Alias: "virtual", Path: "/tmp/serialforge-a", Baud: 115200}
	ports := []serial.PortInfo{
		{Path: "/dev/cu.debug-console"},
		{Path: "/dev/cu.Bluetooth-Incoming-Port"},
	}
	got, err := Resolve(p, ports)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Path != "/tmp/serialforge-a" {
		t.Errorf("got %q, want the manual path trusted as-is", got.Path)
	}
	// No USB metadata is available for a manual path, and Resolve must not
	// invent any — see device.Resolve's doc comment point 2.
	if got.IsUSB || got.VID != "" || got.PID != "" || got.SerialNumber != "" {
		t.Errorf("manual-path PortInfo should carry no USB metadata, got %+v", got)
	}
}

// TestResolveManualPathWorksWithZeroDetectedPorts covers the "no hardware
// attached at all" case explicitly — enumeration returning nothing must not
// change how a manual path resolves.
func TestResolveManualPathWorksWithZeroDetectedPorts(t *testing.T) {
	p := Profile{Alias: "virtual", Path: "/tmp/serialforge-a"}
	got, err := Resolve(p, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Path != "/tmp/serialforge-a" {
		t.Errorf("got %q, want the manual path", got.Path)
	}
}

// TestResolvePathWithIdentityStillFallsBackToIdentity: a profile that sets
// BOTH a Path and identity fields, whose Path is no longer present, should
// still fall back to identity-based matching (a stale path hint must not
// block resolution when the device is genuinely findable another way) —
// this is unchanged behavior, guarded here so the manual-path trust logic
// added alongside it doesn't accidentally widen to profiles that do have
// identity.
func TestResolvePathWithIdentityStillFallsBackToIdentity(t *testing.T) {
	p := Profile{Alias: "fpga", Path: "/dev/cu.usbserial-OLD", VID: "0403", PID: "6010"}
	ports := []serial.PortInfo{
		{Path: "/dev/cu.usbserial-NEW", VID: "0403", PID: "6010"},
	}
	got, err := Resolve(p, ports)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Path != "/dev/cu.usbserial-NEW" {
		t.Errorf("got %q, want the identity match since the stale path is gone", got.Path)
	}
}

func TestResolveByVIDPID(t *testing.T) {
	p := Profile{Alias: "fpga", VID: "0403", PID: "6010"}
	ports := []serial.PortInfo{
		{Path: "/dev/cu.usbserial-NEW", VID: "0403", PID: "6010"},
	}
	got, err := Resolve(p, ports)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Path != "/dev/cu.usbserial-NEW" {
		t.Errorf("got %q, want the renumbered path to still resolve", got.Path)
	}
}

func TestResolvePathIsCaseAndPrefixTolerant(t *testing.T) {
	p := Profile{Alias: "fpga", VID: "0x0403", PID: "0X6010"}
	ports := []serial.PortInfo{{Path: "/dev/x", VID: "0403", PID: "6010"}}
	if _, err := Resolve(p, ports); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
}

func TestResolvePrefersMostSpecificMatch(t *testing.T) {
	p := Profile{Alias: "fpga", VID: "0403", PID: "6010", SerialNumber: "ABC123"}
	ports := []serial.PortInfo{
		{Path: "/dev/other-device", VID: "0403", PID: "6010", SerialNumber: "ZZZ999"},
		{Path: "/dev/right-device", VID: "0403", PID: "6010", SerialNumber: "ABC123"},
	}
	got, err := Resolve(p, ports)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Path != "/dev/right-device" {
		t.Errorf("got %q, want the serial-number match preferred over VID/PID-only", got.Path)
	}
}

func TestResolveNeverGuessesAmongAmbiguousMatches(t *testing.T) {
	p := Profile{Alias: "fpga", VID: "0403", PID: "6010"}
	ports := []serial.PortInfo{
		{Path: "/dev/a", VID: "0403", PID: "6010"},
		{Path: "/dev/b", VID: "0403", PID: "6010"},
	}
	_, err := Resolve(p, ports)
	if err == nil {
		t.Fatal("Resolve() = nil error for two equally-specific matches, want an ambiguity error")
	}
	if !strings.Contains(err.Error(), "ambiguously") {
		t.Errorf("error = %q, want it to mention the ambiguity", err)
	}
}

func TestResolveNoMatch(t *testing.T) {
	p := Profile{Alias: "fpga", VID: "0403", PID: "6010"}
	ports := []serial.PortInfo{{Path: "/dev/x", VID: "1234", PID: "5678"}}
	if _, err := Resolve(p, ports); err == nil {
		t.Fatal("Resolve() = nil error, want error when nothing matches")
	}
}

func TestStoreCRUDAndRoundTrip(t *testing.T) {
	dir := t.TempDir()

	s, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(s.All()) != 0 {
		t.Fatalf("fresh store has %d profiles, want 0", len(s.All()))
	}

	if err := s.Put(Profile{Alias: "fpga", VID: "0403", PID: "6010", Baud: 115200}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load (reload): %v", err)
	}
	p, ok := reloaded.Get("fpga")
	if !ok || p.Baud != 115200 {
		t.Fatalf("reloaded profile = %+v, ok=%v", p, ok)
	}

	if err := reloaded.Rename("fpga", "fpga-a"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, ok := reloaded.Get("fpga"); ok {
		t.Error("old alias still resolves after Rename")
	}
	if err := reloaded.Clone("fpga-a", "fpga-b"); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if len(reloaded.All()) != 2 {
		t.Fatalf("after clone: %d profiles, want 2", len(reloaded.All()))
	}
	if !reloaded.Delete("fpga-b") {
		t.Error("Delete returned false for an existing alias")
	}
	if len(reloaded.All()) != 1 {
		t.Fatalf("after delete: %d profiles, want 1", len(reloaded.All()))
	}
}

func TestStoreRejectsDuplicateRename(t *testing.T) {
	dir := t.TempDir()
	s, _ := Load(dir)
	s.Put(Profile{Alias: "a"})
	s.Put(Profile{Alias: "b"})
	if err := s.Rename("a", "b"); err == nil {
		t.Fatal("Rename to an existing alias should fail")
	}
}

func TestSerialConfigFillsDefaults(t *testing.T) {
	p := Profile{Alias: "fpga", Baud: 9600}
	c := p.SerialConfig()
	if c.Baud != 9600 {
		t.Errorf("Baud = %d, want 9600", c.Baud)
	}
	if c.DataBits != serial.DefaultConfig().DataBits {
		t.Errorf("DataBits should fall back to default, got %d", c.DataBits)
	}
}
