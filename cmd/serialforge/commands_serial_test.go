package main

import "testing"

func TestResolveDeviceArgOrderIndependence(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"port then baud then hex", []string{"--port", "/tmp/a", "--baud", "921600", "--hex"}},
		{"baud then port", []string{"--baud", "921600", "--port", "/tmp/a", "--hex"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			parsed, err := parseArgs(c.args, monitorDefs)
			if err != nil {
				t.Fatalf("parseArgs: %v", err)
			}
			device, baud, err := resolveDeviceArg(parsed)
			if err != nil {
				t.Fatalf("resolveDeviceArg: %v", err)
			}
			if device != "/tmp/a" {
				t.Errorf("device = %q, want /tmp/a", device)
			}
			if baud == nil || *baud != 921600 {
				t.Errorf("baud = %v, want 921600", baud)
			}
		})
	}
}

func TestResolveDeviceArgPositionalShorthand(t *testing.T) {
	parsed, err := parseArgs([]string{"/tmp/a", "--hex"}, monitorDefs)
	if err != nil {
		t.Fatal(err)
	}
	device, baud, err := resolveDeviceArg(parsed)
	if err != nil {
		t.Fatalf("resolveDeviceArg: %v", err)
	}
	if device != "/tmp/a" {
		t.Errorf("device = %q, want /tmp/a", device)
	}
	if baud != nil {
		t.Errorf("baud = %v, want nil (not given)", baud)
	}
}

func TestResolveDeviceArgRejectsPositionalAndFlagConflict(t *testing.T) {
	parsed, err := parseArgs([]string{"/tmp/a", "--port", "/tmp/b"}, monitorDefs)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolveDeviceArg(parsed); err == nil {
		t.Fatal("positional device + --port with a different value should conflict, got nil error")
	}
}

func TestResolveDeviceArgRequiresADevice(t *testing.T) {
	parsed, err := parseArgs([]string{"--hex"}, monitorDefs)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolveDeviceArg(parsed); err == nil {
		t.Fatal("no device given at all should error")
	}
}
