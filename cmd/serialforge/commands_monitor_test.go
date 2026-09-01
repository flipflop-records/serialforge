package main

import "testing"

// These tests execute the real argv-parsing layer cmdMonitor/cmdSend
// actually call (parseArgs against monitorDefs/sendDefs, then
// resolveDeviceArg/resolveSendArgs/resolveSendMode) — not just parseArgs in
// isolation — against the exact argv forms from the CLI UX bug report and
// its required test matrix. This is the regression suite for "serialforge
// monitor --port /tmp/serialforge-a --baud 115200 --hex tried to open a
// device literally named --port."

func monitorArgs(t *testing.T, args ...string) (device string, baud *int) {
	t.Helper()
	parsed, err := parseArgs(args, monitorDefs)
	if err != nil {
		t.Fatalf("parseArgs(%v): %v", args, err)
	}
	device, baud, err = resolveDeviceArg(parsed)
	if err != nil {
		t.Fatalf("resolveDeviceArg(%v): %v", args, err)
	}
	return device, baud
}

func TestMonitorPortHex(t *testing.T) {
	device, baud := monitorArgs(t, "--port", "/tmp/a", "--hex")
	if device != "/tmp/a" || baud != nil {
		t.Errorf("device=%q baud=%v, want /tmp/a, nil", device, baud)
	}
}

func TestMonitorHexPort(t *testing.T) {
	device, baud := monitorArgs(t, "--hex", "--port", "/tmp/a")
	if device != "/tmp/a" || baud != nil {
		t.Errorf("device=%q baud=%v, want /tmp/a, nil", device, baud)
	}
}

func TestMonitorBaudPort(t *testing.T) {
	device, baud := monitorArgs(t, "--baud", "921600", "--port", "/tmp/a")
	if device != "/tmp/a" || baud == nil || *baud != 921600 {
		t.Errorf("device=%q baud=%v, want /tmp/a, 921600", device, baud)
	}
}

func TestMonitorPositionalHex(t *testing.T) {
	device, baud := monitorArgs(t, "/tmp/a", "--hex")
	if device != "/tmp/a" || baud != nil {
		t.Errorf("device=%q baud=%v, want /tmp/a, nil", device, baud)
	}
}

func TestMonitorPositionalConflictsWithPort(t *testing.T) {
	parsed, err := parseArgs([]string{"/tmp/a", "--port", "/tmp/b"}, monitorDefs)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolveDeviceArg(parsed); err == nil {
		t.Fatal("monitor /tmp/a --port /tmp/b should be a conflict error")
	}
}

func sendArgs(t *testing.T, args ...string) (device, payload, mode string) {
	t.Helper()
	parsed, err := parseArgs(args, sendDefs)
	if err != nil {
		t.Fatalf("parseArgs(%v): %v", args, err)
	}
	mode, err = resolveSendMode(parsed)
	if err != nil {
		t.Fatalf("resolveSendMode(%v): %v", args, err)
	}
	device, payload, err = resolveSendArgs(parsed)
	if err != nil {
		t.Fatalf("resolveSendArgs(%v): %v", args, err)
	}
	return device, payload, mode
}

func TestSendPortHexPayload(t *testing.T) {
	device, payload, mode := sendArgs(t, "--port", "/tmp/a", "--hex", "AA 55")
	if device != "/tmp/a" || payload != "AA 55" || mode != "hex" {
		t.Errorf("got %q %q %q, want /tmp/a, AA 55, hex", device, payload, mode)
	}
}

func TestSendHexPayloadThenPort(t *testing.T) {
	device, payload, mode := sendArgs(t, "--hex", "AA 55", "--port", "/tmp/a")
	if device != "/tmp/a" || payload != "AA 55" || mode != "hex" {
		t.Errorf("got %q %q %q, want /tmp/a, AA 55, hex", device, payload, mode)
	}
}

func TestSendPortBaudHex(t *testing.T) {
	parsed, err := parseArgs([]string{"--port", "/tmp/a", "--baud", "921600", "--hex", "AA 55"}, sendDefs)
	if err != nil {
		t.Fatal(err)
	}
	device, payload, err := resolveSendArgs(parsed)
	if err != nil {
		t.Fatal(err)
	}
	baud, ok, err := parsed.single("--baud", "--baud")
	if err != nil || !ok || baud != "921600" {
		t.Fatalf("baud = %q, %v, %v; want 921600, true, nil", baud, ok, err)
	}
	if device != "/tmp/a" || payload != "AA 55" {
		t.Errorf("got %q %q, want /tmp/a, AA 55", device, payload)
	}
}

func TestSendPositionalDeviceAndPayload(t *testing.T) {
	device, payload, mode := sendArgs(t, "/tmp/a", "AA 55", "--hex")
	if device != "/tmp/a" || payload != "AA 55" || mode != "hex" {
		t.Errorf("got %q %q %q, want /tmp/a, AA 55, hex", device, payload, mode)
	}
}

func TestSendFlagOrderNeverChangesResolution(t *testing.T) {
	// All four of these must resolve identically — the exact "changing the
	// flag order must not change behavior" requirement.
	forms := [][]string{
		{"--port", "/tmp/serialforge-a", "--hex", "AA 55 02 00 C0 17 FF 00 80"},
		{"--hex", "AA 55 02 00 C0 17 FF 00 80", "--port", "/tmp/serialforge-a"},
		{"--baud", "115200", "--port", "/tmp/serialforge-a", "--hex", "AA 55 02 00 C0 17 FF 00 80"},
		{"--hex", "AA 55 02 00 C0 17 FF 00 80", "--baud", "115200", "--port", "/tmp/serialforge-a"},
	}
	var want [3]string // device, payload, mode
	for i, args := range forms {
		parsed, err := parseArgs(args, sendDefs)
		if err != nil {
			t.Fatalf("form %d parseArgs: %v", i, err)
		}
		mode, err := resolveSendMode(parsed)
		if err != nil {
			t.Fatalf("form %d resolveSendMode: %v", i, err)
		}
		device, payload, err := resolveSendArgs(parsed)
		if err != nil {
			t.Fatalf("form %d resolveSendArgs: %v", i, err)
		}
		got := [3]string{device, payload, mode}
		if i == 0 {
			want = got
			continue
		}
		if got != want {
			t.Errorf("form %d = %v, want %v (must match form 0)", i, got, want)
		}
	}
}

// --- invalid / conflicting cases -------------------------------------------

func TestSendRejectsHexAndText(t *testing.T) {
	parsed, err := parseArgs([]string{"--hex", "--text", "hello"}, sendDefs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolveSendMode(parsed); err == nil {
		t.Fatal("send --hex --text hello should error: both modes given")
	}
}

func TestSendMissingPortValueIsError(t *testing.T) {
	_, err := parseArgs([]string{"--port"}, sendDefs)
	if err == nil {
		t.Fatal("send --port (nothing after it) should error")
	}
}

func TestSendUnknownFlagIsError(t *testing.T) {
	_, err := parseArgs([]string{"--port", "/tmp/a", "--bogus"}, sendDefs)
	if err == nil {
		t.Fatal("an unrecognized flag should error, not be silently ignored/treated as positional")
	}
}

func TestSendMissingPayloadIsError(t *testing.T) {
	parsed, err := parseArgs([]string{"--port", "/tmp/a"}, sendDefs)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolveSendArgs(parsed); err == nil {
		t.Fatal("send --port /tmp/a with no payload should error")
	}
}

func TestSendOnlyDeviceNoPayloadPositionalIsError(t *testing.T) {
	parsed, err := parseArgs([]string{"/tmp/a"}, sendDefs)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolveSendArgs(parsed); err == nil {
		t.Fatal("send /tmp/a (device only, no payload) should error, not guess")
	}
}

func TestSendTooManyPositionalsIsError(t *testing.T) {
	parsed, err := parseArgs([]string{"/tmp/a", "AA 55", "extra"}, sendDefs)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolveSendArgs(parsed); err == nil {
		t.Fatal("three bare positional arguments should error, not silently pick two")
	}
}

func TestSendPositionalDeviceConflictsWithPort(t *testing.T) {
	parsed, err := parseArgs([]string{"/tmp/a", "AA 55", "--port", "/tmp/b", "--hex"}, sendDefs)
	if err != nil {
		t.Fatal(err)
	}
	// --port given AND 2 positionals present: ambiguous — the resolver
	// requires exactly one positional (the payload) once --port is given.
	if _, _, err := resolveSendArgs(parsed); err == nil {
		t.Fatal("a positional device alongside --port should be rejected as ambiguous, not silently resolved")
	}
}

func TestMonitorUnknownFlagIsError(t *testing.T) {
	_, err := parseArgs([]string{"--port", "/tmp/a", "--verbose"}, monitorDefs)
	if err == nil {
		t.Fatal("an unrecognized flag should error")
	}
}

func TestMonitorMissingBaudValueIsError(t *testing.T) {
	_, err := parseArgs([]string{"--port", "/tmp/a", "--baud"}, monitorDefs)
	if err == nil {
		t.Fatal("--baud with nothing following it should error")
	}
}

func TestMonitorConflictingPortAndPathValues(t *testing.T) {
	parsed, err := parseArgs([]string{"--port", "/tmp/a", "--path", "/tmp/b", "--hex"}, monitorDefs)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolveDeviceArg(parsed); err == nil {
		t.Fatal("--port and --path with different values should conflict")
	}
}

func TestMonitorHelpRequestedShortCircuits(t *testing.T) {
	if !wantsHelp([]string{"--port", "/tmp/a", "--help"}) {
		t.Error("wantsHelp should find --help anywhere in argv")
	}
	if !wantsHelp([]string{"-h"}) {
		t.Error("wantsHelp should recognize -h")
	}
	if wantsHelp([]string{"--port", "/tmp/a"}) {
		t.Error("wantsHelp should be false with no help flag present")
	}
}
