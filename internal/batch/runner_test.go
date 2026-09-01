package batch

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/vtemnyakov/serialforge/internal/checksum"
	"github.com/vtemnyakov/serialforge/internal/framing"
	"github.com/vtemnyakov/serialforge/internal/packet"
	"github.com/vtemnyakov/serialforge/internal/serial"
	"github.com/vtemnyakov/serialforge/internal/session"
)

// testSchema: 4-byte packet — 1-byte command, 2-byte value, CRC-8 tail.
func testSchema() packet.Schema {
	return packet.Schema{
		Name:      "test",
		TotalSize: 4,
		Fields: []packet.Field{
			{Name: "command", Size: 1, Format: packet.FormatUint},
			{Name: "value", Size: 2, Endianness: packet.BigEndian, Format: packet.FormatHex},
		},
		Checksum: checksum.Definition{Mode: checksum.ModePreset, Preset: "CRC-8"},
	}
}

// newTestSession wires a Session over a FakePort with fixed-size framing
// matching testSchema's 4-byte packets, and starts its RX loop. The
// returned FakeDevice plays the role of the DUT: dev.Write feeds
// "responses", and reading dev.Read shows what the scenario sent it.
func newTestSession(t *testing.T) (*session.Session, *serial.FakeDevice) {
	t.Helper()
	port, dev := serial.NewFakePort()
	f, err := framing.New(framing.KindFixed, framing.Options{Size: 4})
	if err != nil {
		t.Fatal(err)
	}
	s := session.New(session.Config{Port: port, Framer: f})
	s.Start(context.Background())
	t.Cleanup(func() { s.Close(); dev.Close() })
	return s, dev
}

func TestRunSendPacketExpectPacketAssertPass(t *testing.T) {
	sess, dev := newTestSession(t)
	schema := testSchema()

	// The "DUT": once it sees a command byte, it echoes back
	// command|0x80 with the same value and a correct CRC.
	go func() {
		buf := make([]byte, 4)
		if _, err := io.ReadFull(dev, buf); err != nil {
			return
		}
		resp, _ := packet.EncodeUint(schema.Fields[0], uint64(buf[0]|0x80))
		val := append([]byte(nil), buf[1:3]...)
		values := packet.Values{"command": resp, "value": val}
		raw, _, _ := packet.Serialize(schema, values, nil)
		dev.Write(raw)
	}()

	scenario := Scenario{
		Steps: []Step{
			{SendPacket: &SendPacketStep{Fields: map[string]string{"command": "02", "value": "00FF"}}},
			{ExpectPacket: &ExpectPacketStep{Timeout: "1s"}},
			{AssertField: &AssertFieldStep{Field: "command", Equals: "82"}},
			{AssertCRC: &AssertCRCStep{Valid: true}},
		},
	}

	report := Run(context.Background(), sess, &schema, scenario, nil)
	if !report.Pass {
		t.Fatalf("report.Pass = false, steps: %+v", report.Steps)
	}
	if len(report.Steps) != 4 {
		t.Fatalf("got %d step results, want 4", len(report.Steps))
	}
	for _, s := range report.Steps {
		if s.Status != StatusPass {
			t.Errorf("step %d (%s) = %s, want pass: %s", s.Index, s.Label, s.Status, s.Message)
		}
	}
}

func TestRunAssertFieldFailureStopsScenario(t *testing.T) {
	sess, dev := newTestSession(t)
	schema := testSchema()

	go func() {
		buf := make([]byte, 4)
		io.ReadFull(dev, buf)
		// Reply with the WRONG command byte (no 0x80 set) — the assertion
		// below should fail.
		values := packet.Values{"command": buf[0:1], "value": buf[1:3]}
		raw, _, _ := packet.Serialize(schema, values, nil)
		dev.Write(raw)
	}()

	scenario := Scenario{
		Steps: []Step{
			{SendPacket: &SendPacketStep{Fields: map[string]string{"command": "02", "value": "00FF"}}},
			{ExpectPacket: &ExpectPacketStep{Timeout: "1s"}},
			{AssertField: &AssertFieldStep{Field: "command", Equals: "82"}}, // will fail: got 02
			{AssertCRC: &AssertCRCStep{Valid: true}},                        // must not run
		},
	}

	report := Run(context.Background(), sess, &schema, scenario, nil)
	if report.Pass {
		t.Fatal("report.Pass = true, want false")
	}
	if len(report.Steps) != 3 {
		t.Fatalf("got %d step results, want 3 (stop at first failure): %+v", len(report.Steps), report.Steps)
	}
	if report.Steps[2].Status != StatusFail {
		t.Errorf("step 2 status = %s, want fail", report.Steps[2].Status)
	}
}

func TestRunAssertCRCFailureDetectsCorruption(t *testing.T) {
	sess, dev := newTestSession(t)
	schema := testSchema()

	go func() {
		buf := make([]byte, 4)
		io.ReadFull(dev, buf)
		values := packet.Values{"command": {0x82}, "value": buf[1:3]}
		raw, _, _ := packet.Serialize(schema, values, nil)
		raw[3] ^= 0xFF // corrupt the CRC byte
		dev.Write(raw)
	}()

	scenario := Scenario{
		Steps: []Step{
			{SendPacket: &SendPacketStep{Fields: map[string]string{"command": "02", "value": "00FF"}}},
			{ExpectPacket: &ExpectPacketStep{Timeout: "1s"}},
			{AssertCRC: &AssertCRCStep{Valid: true}},
		},
	}
	report := Run(context.Background(), sess, &schema, scenario, nil)
	if report.Pass {
		t.Fatal("report.Pass = true, want false for a corrupted CRC")
	}
}

func TestRunExpectPacketTimeout(t *testing.T) {
	sess, _ := newTestSession(t)
	schema := testSchema()
	scenario := Scenario{
		Steps: []Step{
			{ExpectPacket: &ExpectPacketStep{Timeout: "50ms"}},
		},
	}
	start := time.Now()
	report := Run(context.Background(), sess, &schema, scenario, nil)
	if report.Pass {
		t.Fatal("report.Pass = true, want false on timeout")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timeout step took %s, way longer than its 50ms budget", elapsed)
	}
}

func TestRunManualCRCOverrideStep(t *testing.T) {
	sess, dev := newTestSession(t)
	schema := testSchema()

	bad := "00"
	scenario := Scenario{
		Steps: []Step{
			{SendPacket: &SendPacketStep{
				Fields:      map[string]string{"command": "02", "value": "00FF"},
				CRCOverride: &bad,
			}},
		},
	}

	got := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 4)
		io.ReadFull(dev, buf)
		got <- buf
	}()

	report := Run(context.Background(), sess, &schema, scenario, nil)
	if !report.Pass {
		t.Fatalf("send_packet with a CRC override should still succeed as a send: %+v", report.Steps)
	}
	raw := <-got
	if raw[3] != 0x00 {
		t.Errorf("last byte = 0x%02X, want the overridden CRC 0x00", raw[3])
	}
	pkt := report.Steps[0].Packet
	if pkt == nil || !pkt.CRC.Overridden {
		t.Errorf("step result packet CRC.Overridden = %v, want true", pkt)
	}
}

func TestRunOnStepCallbackFiresPerStep(t *testing.T) {
	sess, _ := newTestSession(t)
	scenario := Scenario{Steps: []Step{
		{Log: &LogStep{Message: "one"}},
		{Log: &LogStep{Message: "two"}},
	}}
	var seen []string
	Run(context.Background(), sess, nil, scenario, func(r StepResult) {
		seen = append(seen, r.Message)
	})
	if len(seen) != 2 || seen[0] != "one" || seen[1] != "two" {
		t.Fatalf("onStep callbacks = %v, want [one two]", seen)
	}
}

func TestRunSendPacketWithoutSchemaFails(t *testing.T) {
	sess, _ := newTestSession(t)
	scenario := Scenario{Steps: []Step{
		{SendPacket: &SendPacketStep{Fields: map[string]string{"command": "02"}}},
	}}
	report := Run(context.Background(), sess, nil, scenario, nil)
	if report.Pass {
		t.Fatal("send_packet with no schema should fail, not silently pass")
	}
}
