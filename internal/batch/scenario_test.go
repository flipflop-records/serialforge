package batch

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestTrackedExampleParses guards examples/batch/uart-demo-smoke.yaml against
// silently drifting from the Scenario shape this package actually parses.
func TestTrackedExampleParses(t *testing.T) {
	data, err := os.ReadFile("../../examples/batch/uart-demo-smoke.yaml")
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	var sc Scenario
	if err := yaml.Unmarshal(data, &sc); err != nil {
		t.Fatalf("parse example: %v", err)
	}
	if sc.Protocol != "uart-demo" || sc.Device != "dev-board" {
		t.Fatalf("unexpected scenario: %+v", sc)
	}
	if len(sc.Steps) != 5 {
		t.Fatalf("got %d steps, want 5: %+v", len(sc.Steps), sc.Steps)
	}
	if sc.Steps[1].SendPacket == nil || sc.Steps[1].SendPacket.Fields["command"] != "02" {
		t.Fatalf("step 1 should be send_packet with command=02: %+v", sc.Steps[1])
	}
	if sc.Steps[3].AssertField == nil || sc.Steps[3].AssertField.Field != "command" {
		t.Fatalf("step 3 should be assert_field on command: %+v", sc.Steps[3])
	}
	if sc.Steps[4].AssertCRC == nil || !sc.Steps[4].AssertCRC.Valid {
		t.Fatalf("step 4 should be assert_crc valid=true: %+v", sc.Steps[4])
	}
}
