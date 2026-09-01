package batch

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/vtemnyakov/serialforge/internal/packet"
	"github.com/vtemnyakov/serialforge/internal/session"
)

// Status is one step's outcome.
type Status string

const (
	StatusPass Status = "pass"
	StatusFail Status = "fail"
)

// StepResult is one executed step's outcome, structured for both a TUI's
// live list and JSON automation output (product spec §23/§24).
type StepResult struct {
	Index    int            `json:"index"`
	Label    string         `json:"label"`
	Status   Status         `json:"status"`
	Duration time.Duration  `json:"duration"`
	Message  string         `json:"message,omitempty"`
	Packet   *packet.Packet `json:"-"` // set by send_packet/expect_packet, for a live diagram; not JSON-serialized (raw Go struct)
}

// Report is a scenario's complete result.
type Report struct {
	Scenario Scenario      `json:"-"`
	Steps    []StepResult  `json:"steps"`
	Pass     bool          `json:"pass"`
	Elapsed  time.Duration `json:"elapsed"`
}

// Sender is the minimal interface Run needs to transmit bytes — satisfied
// by *session.Session. Kept as an interface so tests (and any future
// caller) aren't forced to stand up a full Session.
type Sender interface {
	Send([]byte) (int, error)
}

// Run executes scenario's steps in order against sess, using schema (may be
// nil if the scenario has no send_packet/expect_packet/assert_* steps) to
// serialize/decode structured packets. It stops at the first failing step
// (standard test-runner semantics — a failed send_packet or a CRC mismatch
// mid-sequence makes every later assertion meaningless). onStep, if
// non-nil, is called synchronously after each step completes — the TUI's
// live batch view uses this; CLI JSON output just collects the final
// Report.
func Run(ctx context.Context, sess *session.Session, schema *packet.Schema, scenario Scenario, onStep func(StepResult)) Report {
	start := time.Now()
	report := Report{Scenario: scenario}
	var lastPacket *packet.Packet

	events := sess.Events()

	for i, step := range scenario.Steps {
		stepStart := time.Now()
		res := StepResult{Index: i}
		err := runStep(ctx, sess, events, schema, step, &lastPacket, &res)
		res.Duration = time.Since(stepStart)
		if err != nil {
			res.Status = StatusFail
			if res.Message == "" {
				res.Message = err.Error()
			}
		} else {
			res.Status = StatusPass
		}
		report.Steps = append(report.Steps, res)
		if onStep != nil {
			onStep(res)
		}
		if res.Status == StatusFail {
			report.Elapsed = time.Since(start)
			report.Pass = false
			return report
		}
	}
	report.Elapsed = time.Since(start)
	report.Pass = true
	return report
}

func runStep(ctx context.Context, sess Sender, events <-chan session.Event, schema *packet.Schema, step Step, lastPacket **packet.Packet, res *StepResult) error {
	switch {
	case step.Send != nil:
		res.Label = "send " + step.Send.Hex
		data, err := parseHex(step.Send.Hex)
		if err != nil {
			return err
		}
		_, err = sess.Send(data)
		return err

	case step.SendPacket != nil:
		res.Label = "send_packet"
		if schema == nil {
			return errNoSchema
		}
		values, crcOverride, err := resolveFields(*schema, step.SendPacket.Fields, step.SendPacket.CRCOverride)
		if err != nil {
			return err
		}
		pkt, err := packet.Build(*schema, values, crcOverride)
		if err != nil {
			return err
		}
		res.Packet = pkt
		_, err = sess.Send(pkt.Raw)
		return err

	case step.Sleep != nil:
		d, err := parseDuration(step.Sleep.Duration, 0)
		if err != nil {
			return err
		}
		res.Label = fmt.Sprintf("sleep %s", d)
		select {
		case <-time.After(d):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}

	case step.Expect != nil:
		res.Label = "expect " + step.Expect.Hex
		want, err := parseHex(step.Expect.Hex)
		if err != nil {
			return err
		}
		timeout, err := parseDuration(step.Expect.Timeout, time.Second)
		if err != nil {
			return err
		}
		frame, err := waitFrame(ctx, events, timeout)
		if err != nil {
			return err
		}
		if string(frame) != string(want) {
			return fmt.Errorf("got % X, want % X", frame, want)
		}
		return nil

	case step.ExpectPacket != nil:
		res.Label = "expect_packet"
		if schema == nil {
			return errNoSchema
		}
		timeout, err := parseDuration(step.ExpectPacket.Timeout, time.Second)
		if err != nil {
			return err
		}
		frame, err := waitFrame(ctx, events, timeout)
		if err != nil {
			return err
		}
		pkt, err := packet.Decode(*schema, frame)
		if err != nil {
			return err
		}
		res.Packet = pkt
		*lastPacket = pkt
		return nil

	case step.AssertField != nil:
		res.Label = fmt.Sprintf("assert_field %s == %s", step.AssertField.Field, step.AssertField.Equals)
		if *lastPacket == nil {
			return errNoPacket
		}
		want, err := parseHex(step.AssertField.Equals)
		if err != nil {
			return err
		}
		for _, fv := range (*lastPacket).Fields {
			if fv.Field.Name == step.AssertField.Field {
				if string(fv.Raw) != string(want) {
					return fmt.Errorf("field %q = % X, want % X", fv.Field.Name, fv.Raw, want)
				}
				return nil
			}
		}
		return fmt.Errorf("no field named %q in the decoded packet", step.AssertField.Field)

	case step.AssertCRC != nil:
		res.Label = fmt.Sprintf("assert_crc valid=%v", step.AssertCRC.Valid)
		if *lastPacket == nil {
			return errNoPacket
		}
		if (*lastPacket).CRC == nil {
			return fmt.Errorf("decoded packet has no CRC (schema has no checksum configured)")
		}
		if (*lastPacket).CRC.Valid != step.AssertCRC.Valid {
			return fmt.Errorf("CRC valid=%v, want %v", (*lastPacket).CRC.Valid, step.AssertCRC.Valid)
		}
		return nil

	case step.ClearRX != nil:
		res.Label = "clear_rx"
		drainPending(events)
		return nil

	case step.Log != nil:
		res.Label = "log"
		res.Message = step.Log.Message
		return nil

	default:
		return errEmptyStep
	}
}

// resolveFields converts hex-string field values into packet.Values and an
// optional CRC override, validated against schema.
func resolveFields(schema packet.Schema, fields map[string]string, crcOverrideHex *string) (packet.Values, *uint64, error) {
	values := packet.Values{}
	for name, hexStr := range fields {
		f, _, ok := schema.FieldByName(name)
		if !ok {
			return nil, nil, fmt.Errorf("schema %q has no field %q", schema.Name, name)
		}
		raw, err := parseHex(hexStr)
		if err != nil {
			return nil, nil, fmt.Errorf("field %q: %w", name, err)
		}
		if len(raw) != f.Size {
			return nil, nil, fmt.Errorf("field %q: got %d bytes, want %d", name, len(raw), f.Size)
		}
		values[name] = raw
	}
	var crcOverride *uint64
	if crcOverrideHex != nil {
		raw, err := parseHex(*crcOverrideHex)
		if err != nil {
			return nil, nil, fmt.Errorf("crc_override: %w", err)
		}
		v := uint64(0)
		for _, b := range raw {
			v = v<<8 | uint64(b)
		}
		crcOverride = &v
	}
	return values, crcOverride, nil
}

// waitFrame blocks for the next RX event, honoring both ctx and an
// explicit per-step timeout, and skipping over non-RX (TX/status) events
// rather than being confused by them.
func waitFrame(ctx context.Context, events <-chan session.Event, timeout time.Duration) ([]byte, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case e, ok := <-events:
			if !ok {
				return nil, fmt.Errorf("session closed while waiting for a frame")
			}
			if e.Kind == session.EventRX {
				return e.Data, nil
			}
		case <-deadline.C:
			return nil, fmt.Errorf("timed out after %s waiting for a frame", timeout)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// drainPending discards every event currently buffered on the channel
// without blocking — clear_rx's implementation.
func drainPending(events <-chan session.Event) {
	for {
		select {
		case <-events:
		default:
			return
		}
	}
}

func parseHex(s string) ([]byte, error) {
	s = strings.NewReplacer(" ", "", "0x", "", "0X", "", ",", "", "\t", "", "\n", "").Replace(s)
	if len(s)%2 != 0 {
		return nil, fmt.Errorf("hex string %q has an odd number of digits", s)
	}
	return hex.DecodeString(s)
}

var (
	errNoSchema  = fmt.Errorf("batch: step requires a protocol schema but none was resolved for this scenario")
	errNoPacket  = fmt.Errorf("batch: no packet has been received yet (expect_packet must run first)")
	errEmptyStep = fmt.Errorf("batch: step has no action set")
)
