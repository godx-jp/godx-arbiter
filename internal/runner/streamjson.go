package runner

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Event is a single decoded stream-json event. Claude Code documents
// the outer envelope as `type` + variant fields; we keep an opaque
// Raw field so unknown variants (schema drift) still pass through.
type Event struct {
	Type string          `json:"type"`
	Raw  json.RawMessage `json:"-"`

	// message_start / message_delta payloads
	Message *EventMessage `json:"message,omitempty"`

	// content_block_start / content_block_delta / content_block_stop
	Index        int             `json:"index,omitempty"`
	ContentBlock json.RawMessage `json:"content_block,omitempty"`
	Delta        json.RawMessage `json:"delta,omitempty"`

	// error events
	Error *EventError `json:"error,omitempty"`
}

// EventMessage is the message envelope inside message_start /
// message_delta. We pull out only the fields we actually use.
type EventMessage struct {
	ID    string         `json:"id,omitempty"`
	Model string         `json:"model,omitempty"`
	Usage *MessageUsage  `json:"usage,omitempty"`
	Stop  string         `json:"stop_reason,omitempty"`
	Extra map[string]any `json:"-"`
}

// MessageUsage tracks token counts. Claude Code emits the running
// totals on every message_delta, then a final aggregate at
// message_stop.
type MessageUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// EventError is the payload of an error-typed event.
type EventError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// ContentBlockType returns the discriminator inside content_block (or
// delta) — "text", "tool_use", "input_json_delta", "text_delta", etc.
// Returns "" when the block can't be parsed.
func (e Event) ContentBlockType() string {
	if len(e.ContentBlock) > 0 {
		return jsonStringField(e.ContentBlock, "type")
	}
	if len(e.Delta) > 0 {
		return jsonStringField(e.Delta, "type")
	}
	return ""
}

// TextDelta returns the incremental text from a content_block_delta
// of type "text_delta".
func (e Event) TextDelta() string {
	if len(e.Delta) == 0 {
		return ""
	}
	return jsonStringField(e.Delta, "text")
}

// ToolUseName / ToolUseID extract identifiers from a content_block_start
// of type "tool_use".
func (e Event) ToolUseName() string {
	return jsonStringField(e.ContentBlock, "name")
}

func (e Event) ToolUseID() string {
	return jsonStringField(e.ContentBlock, "id")
}

// FinalText is true when this event signals the end of the run.
// Claude Code uses message_stop; we treat top-level result events the
// same way for forward compatibility.
func (e Event) FinalText() bool {
	return e.Type == "message_stop" || e.Type == "result"
}

// jsonStringField pulls out a single string field from a raw JSON
// object. Returns "" on any error.
func jsonStringField(raw json.RawMessage, field string) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	if v, ok := m[field].(string); ok {
		return v
	}
	return ""
}

// DecodeStream reads stream-json events from r line-by-line. Each
// non-empty line must parse as a JSON object; corrupted lines emit a
// warning to warn (when non-nil) and are skipped — the rest of the
// stream still flows. Returns when r reaches EOF or ctx is cancelled
// at the caller level (ctx isn't passed; the caller closes r to abort).
//
// Designed for the Claude Code stream-json shape but tolerant of
// schema drift: unknown event types end up as Event{Type, Raw} with
// the rest of the typed fields empty. The renderer can decide what to
// do — soft-fail in our case (log once per run, keep going).
func DecodeStream(r io.Reader, warn func(error)) (<-chan Event, <-chan error) {
	events := make(chan Event, 32)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		sc := bufio.NewScanner(r)
		// Stream-json lines can be larger than the default 64 KiB.
		// 1 MiB is enough headroom for any single event we expect.
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Bytes()
			if len(line) == 0 {
				continue
			}
			var ev Event
			ev.Raw = append(ev.Raw[:0], line...)
			if err := json.Unmarshal(line, &ev); err != nil {
				if warn != nil {
					warn(fmt.Errorf("stream-json: %w (line=%s)", err, truncate(string(line), 240)))
				}
				continue
			}
			events <- ev
		}
		if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
			errs <- err
		}
	}()
	return events, errs
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
