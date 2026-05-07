package runner

import (
	"strings"
	"testing"
)

func TestDecodeStream_HappyPath(t *testing.T) {
	in := strings.NewReader(`{"type":"message_start","message":{"id":"x","usage":{"input_tokens":10}}}
{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}
{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}
{"type":"content_block_stop","index":0}
{"type":"message_stop"}
`)
	var warnings []error
	events, _ := DecodeStream(in, func(e error) { warnings = append(warnings, e) })
	var collected []Event
	for ev := range events {
		collected = append(collected, ev)
	}
	if len(collected) != 5 {
		t.Fatalf("got %d events, want 5", len(collected))
	}
	if collected[0].Type != "message_start" {
		t.Errorf("first event = %s", collected[0].Type)
	}
	if collected[2].TextDelta() != "hi" {
		t.Errorf("text delta = %q", collected[2].TextDelta())
	}
	if !collected[4].FinalText() {
		t.Errorf("last event should be final")
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
}

func TestDecodeStream_SoftFailUnknown(t *testing.T) {
	// Schema drift simulation: include a corrupted line + a line with
	// a totally unknown event type. We expect the corrupted line to
	// trigger a warning, and the unknown type to pass through opaque.
	in := strings.NewReader(`{"type":"message_start","message":{"id":"x"}}
not actually json
{"type":"future_event_2027","payload":{"shape":"unknown"}}
{"type":"message_stop"}
`)
	var warnings []error
	events, _ := DecodeStream(in, func(e error) { warnings = append(warnings, e) })
	var collected []Event
	for ev := range events {
		collected = append(collected, ev)
	}
	if len(collected) != 3 {
		t.Fatalf("got %d events, want 3 (corrupted line should be skipped, future event passes through)", len(collected))
	}
	if collected[1].Type != "future_event_2027" {
		t.Errorf("future event was lost: %v", collected[1])
	}
	if len(warnings) != 1 {
		t.Errorf("expected 1 warning for corrupted line, got %d: %v", len(warnings), warnings)
	}
}

func TestEvent_ToolUseFields(t *testing.T) {
	in := strings.NewReader(`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tu_abc","name":"Bash","input":{}}}
`)
	events, _ := DecodeStream(in, nil)
	ev := <-events
	if ev.ContentBlockType() != "tool_use" {
		t.Errorf("content_block type = %q", ev.ContentBlockType())
	}
	if ev.ToolUseName() != "Bash" {
		t.Errorf("tool name = %q", ev.ToolUseName())
	}
	if ev.ToolUseID() != "tu_abc" {
		t.Errorf("tool id = %q", ev.ToolUseID())
	}
}
