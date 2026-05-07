package runner

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Event is a single decoded stream-json event. Two upstream shapes
// land here:
//
//   - The raw Anthropic streaming API: `message_start` /
//     `content_block_start` / `content_block_delta` /
//     `content_block_stop` / `message_stop`. Used by the proxy.
//   - The Claude Code CLI `--output-format stream-json` envelope:
//     `system` / `assistant` / `user` / `result`. The CLI batches
//     messages instead of streaming deltas — each `assistant` event
//     carries the complete message.
//
// The decoder keeps an opaque Raw field so unknown variants (schema
// drift) still pass through. Both shapes coexist on the Event struct;
// the renderer + absorbEvent dispatch on Type.
type Event struct {
	Type string          `json:"type"`
	Raw  json.RawMessage `json:"-"`

	// Used by both shapes (assistant message envelope, message_start).
	Message *EventMessage `json:"message,omitempty"`

	// Anthropic streaming: content_block_start / content_block_delta /
	// content_block_stop.
	Index        int             `json:"index,omitempty"`
	ContentBlock json.RawMessage `json:"content_block,omitempty"`
	Delta        json.RawMessage `json:"delta,omitempty"`

	// Claude Code CLI: result event carries cost + final usage at the
	// top level (not nested under .message).
	Subtype     string `json:"subtype,omitempty"`
	Result      string `json:"result,omitempty"`
	IsError     bool   `json:"is_error,omitempty"`
	NumTurns    int    `json:"num_turns,omitempty"`
	TotalCost   float64 `json:"total_cost_usd,omitempty"`
	StopReason  string `json:"stop_reason,omitempty"`
	ResultUsage *MessageUsage `json:"usage,omitempty"`

	// error events
	Error *EventError `json:"error,omitempty"`
}

// EventMessage is the message envelope shared between the Anthropic
// streaming API (`message_start`) and the Claude Code CLI's
// `assistant` / `user` events. The CLI nests the full message
// (content blocks, usage) inside .message; the API nests partial
// state and emits deltas separately.
type EventMessage struct {
	ID      string         `json:"id,omitempty"`
	Model   string         `json:"model,omitempty"`
	Usage   *MessageUsage  `json:"usage,omitempty"`
	Stop    string         `json:"stop_reason,omitempty"`
	Content []EventContent `json:"content,omitempty"`
	Extra   map[string]any `json:"-"`
}

// EventContent is one content block inside .message.content.
type EventContent struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ID       string          `json:"id,omitempty"`
	Name     string          `json:"name,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
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
// Both upstream shapes converge here: the Anthropic API emits
// `message_stop`; the Claude Code CLI emits `result`.
func (e Event) FinalText() bool {
	return e.Type == "message_stop" || e.Type == "result"
}

// AssistantText returns the concatenated text from a Claude Code
// `assistant` event's nested content blocks. Empty for other event
// shapes.
func (e Event) AssistantText() string {
	if e.Type != "assistant" || e.Message == nil {
		return ""
	}
	var s string
	for _, c := range e.Message.Content {
		if c.Type == "text" {
			s += c.Text
		}
	}
	return s
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
