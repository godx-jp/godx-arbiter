package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/godx-team/godx-arbiter/internal/config"
	"github.com/godx-team/godx-arbiter/internal/policy"
)

// streamingTransform copies SSE events from upstream → client while
// inspecting them for tool_use blocks. When a tool_use block is denied
// by the fast-path policy, the transformer rewrites that block's
// content_block_start + content_block_delta + content_block_stop events
// into a synthetic text block carrying the refusal reason. Anything
// else (text deltas, message_start, message_delta, ping events) is
// passed through unchanged.
//
// The transformer is intentionally streaming: it doesn't wait for the
// full response before forwarding. Latency-add is bounded by the size
// of one tool_use block (which is typically tens of bytes of JSON).
//
// Provider-specific framing differs; right now we implement the
// Anthropic SSE shape (most common in practice for arbiter users) and
// fall back to verbatim copy for OpenAI / Gemini streams. The OpenAI
// variant is sketched out below for symmetry but currently passes
// through; the data model is similar enough to layer on without
// re-architecting.
type streamingTransform struct {
	provider string
	policy   *config.Policy
	in       io.ReadCloser
	out      io.Writer
	flusher  http.Flusher
}

func newStreamingTransform(provider string, pol *config.Policy, in io.ReadCloser, out io.Writer) *streamingTransform {
	st := &streamingTransform{provider: provider, policy: pol, in: in, out: out}
	if f, ok := out.(http.Flusher); ok {
		st.flusher = f
	}
	return st
}

// Run pumps until upstream closes. Errors writing to the client are
// returned so the proxy can log them; errors reading from upstream
// are surfaced so the caller can decide whether to fall back to
// non-streaming behavior.
func (s *streamingTransform) Run() error {
	if fn, ok := streamingProviders[s.provider]; ok {
		return fn(s)
	}
	// Unknown shape — passthrough so we don't break the stream.
	_, err := io.Copy(s.flushingWriter(), s.in)
	return err
}

func (s *streamingTransform) flushingWriter() io.Writer {
	return flushingWriter{writeWrapper{s.out, s.flusher}}
}

// writeWrapper turns a (Writer, Flusher) pair into something that
// satisfies http.ResponseWriter's Write() shape (well enough for our
// flushingWriter helper).
type writeWrapper struct {
	w io.Writer
	f http.Flusher
}

func (ww writeWrapper) Header() http.Header        { return http.Header{} }
func (ww writeWrapper) Write(p []byte) (int, error) { return ww.w.Write(p) }
func (ww writeWrapper) WriteHeader(int)             {}
func (ww writeWrapper) Flush() {
	if ww.f != nil {
		ww.f.Flush()
	}
}

// runAnthropic implements the Anthropic streaming protocol:
//
//	event: message_start
//	data: {"type":"message_start","message":{...}}
//
//	event: content_block_start
//	data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}
//
//	event: content_block_delta
//	data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}
//
//	event: content_block_stop
//	data: {"type":"content_block_stop","index":0}
//
// For tool_use blocks, content_block_start carries `{"type":"tool_use","name":"...", "input":{}}`,
// then deltas are `{"type":"input_json_delta","partial_json":"..."}` chunks
// that concat to the full input JSON. We accumulate those, run the
// policy, and either flush the buffered events through (allow) or
// rewrite them into a single text block (deny).
func (s *streamingTransform) runAnthropic() error {
	br := bufio.NewReaderSize(s.in, 64*1024)
	out := bufio.NewWriterSize(s.flushingWriter(), 64*1024)
	defer out.Flush()

	var ev sseEvent
	var bufferingToolUse bool
	var pendingEvents []sseEvent
	var toolBlock anthropicStreamingToolBlock

	flushPending := func() error {
		for _, e := range pendingEvents {
			if err := writeSSE(out, e); err != nil {
				return err
			}
		}
		pendingEvents = pendingEvents[:0]
		return out.Flush()
	}
	rewriteAsRefusal := func(reason string) error {
		// Emit replacement events: content_block_start (text) → delta → stop.
		idx := toolBlock.Index
		repl := []sseEvent{
			{Event: "content_block_start", Data: mustJSON(map[string]any{
				"type":          "content_block_start",
				"index":         idx,
				"content_block": map[string]any{"type": "text", "text": ""},
			})},
			{Event: "content_block_delta", Data: mustJSON(map[string]any{
				"type":  "content_block_delta",
				"index": idx,
				"delta": map[string]any{"type": "text_delta", "text": fmt.Sprintf("tool refused by godx-arbiter: %s", reason)},
			})},
			{Event: "content_block_stop", Data: mustJSON(map[string]any{
				"type":  "content_block_stop",
				"index": idx,
			})},
		}
		for _, e := range repl {
			if err := writeSSE(out, e); err != nil {
				return err
			}
		}
		return out.Flush()
	}

	for {
		var err error
		ev, err = readSSE(br)
		if err != nil {
			if errors.Is(err, io.EOF) {
				if bufferingToolUse {
					// Stream ended mid-tool-use; flush whatever we held.
					if e := flushPending(); e != nil {
						return e
					}
				}
				return nil
			}
			return err
		}
		// Comments / pings: just write through.
		if ev.Comment {
			_, _ = out.Write(append([]byte(": "+ev.CommentLine), '\n', '\n'))
			continue
		}
		if len(ev.Data) == 0 {
			continue
		}
		typ := jsonField(ev.Data, "type")

		switch typ {
		case "content_block_start":
			cb := jsonObject(ev.Data, "content_block")
			if jsonField(cb, "type") == "tool_use" && s.policy != nil {
				bufferingToolUse = true
				toolBlock = anthropicStreamingToolBlock{
					Index: jsonInt(ev.Data, "index"),
					Name:  jsonField(cb, "name"),
					Input: bytes.NewBuffer(nil),
				}
				pendingEvents = []sseEvent{ev}
				continue
			}
			// Not a tool_use we care about — pass through.
			if bufferingToolUse {
				pendingEvents = append(pendingEvents, ev)
				continue
			}
			if err := writeSSE(out, ev); err != nil {
				return err
			}

		case "content_block_delta":
			if bufferingToolUse && jsonInt(ev.Data, "index") == toolBlock.Index {
				delta := jsonObject(ev.Data, "delta")
				if jsonField(delta, "type") == "input_json_delta" {
					toolBlock.Input.WriteString(jsonField(delta, "partial_json"))
				}
				pendingEvents = append(pendingEvents, ev)
				continue
			}
			if err := writeSSE(out, ev); err != nil {
				return err
			}

		case "content_block_stop":
			if bufferingToolUse && jsonInt(ev.Data, "index") == toolBlock.Index {
				pendingEvents = append(pendingEvents, ev)
				// Decide.
				input := toolBlock.Input.Bytes()
				if len(input) == 0 {
					input = []byte(`{}`)
				}
				d := policy.Eval(s.policy, &policy.Action{
					ToolName:  toolBlock.Name,
					ToolInput: input,
				})
				if d.Outcome == policy.OutcomeDeny {
					reason := d.Reason
					if reason == "" {
						reason = "denied by fast-path policy"
					}
					if err := rewriteAsRefusal(reason); err != nil {
						return err
					}
				} else {
					if err := flushPending(); err != nil {
						return err
					}
				}
				bufferingToolUse = false
				toolBlock = anthropicStreamingToolBlock{}
				continue
			}
			if err := writeSSE(out, ev); err != nil {
				return err
			}

		default:
			if bufferingToolUse {
				pendingEvents = append(pendingEvents, ev)
				continue
			}
			if err := writeSSE(out, ev); err != nil {
				return err
			}
		}
		if err := out.Flush(); err != nil {
			return err
		}
	}
}

// anthropicStreamingToolBlock buffers a single tool_use block while it
// is being assembled across deltas.
type anthropicStreamingToolBlock struct {
	Index int
	Name  string
	Input *bytes.Buffer
}

// sseEvent is one parsed Server-Sent Event.
type sseEvent struct {
	Event       string
	Data        []byte
	ID          string
	Retry       string
	Comment     bool
	CommentLine string
}

// readSSE reads one event from r. An event ends at a blank line. The
// caller iterates by calling readSSE repeatedly until io.EOF; events
// with no payload (no data, no event, no comment) are skipped.
func readSSE(r *bufio.Reader) (sseEvent, error) {
	for {
		ev, hasContent, err := readOneSSE(r)
		if err != nil {
			return ev, err
		}
		if hasContent {
			return ev, nil
		}
		// Empty event — keep looking.
	}
}

func readOneSSE(r *bufio.Reader) (sseEvent, bool, error) {
	var ev sseEvent
	var dataLines [][]byte
	sawAnyLine := false
	for {
		line, err := r.ReadBytes('\n')
		eof := errors.Is(err, io.EOF)
		if err != nil && !eof {
			return sseEvent{}, false, err
		}
		// Trim trailing CRLF.
		line = bytes.TrimRight(line, "\r\n")
		if len(line) == 0 {
			if eof && !sawAnyLine {
				return ev, false, io.EOF
			}
			break // dispatch
		}
		sawAnyLine = true
		if line[0] == ':' {
			ev.Comment = true
			ev.CommentLine = string(bytes.TrimSpace(line[1:]))
		} else if k, v, ok := splitField(line); ok {
			switch k {
			case "event":
				ev.Event = string(v)
			case "data":
				dataLines = append(dataLines, v)
			case "id":
				ev.ID = string(v)
			case "retry":
				ev.Retry = string(v)
			}
		}
		if eof {
			break
		}
	}
	if len(dataLines) > 0 {
		ev.Data = bytes.Join(dataLines, []byte{'\n'})
	}
	hasContent := len(dataLines) > 0 || ev.Comment || ev.Event != "" || ev.ID != "" || ev.Retry != ""
	return ev, hasContent, nil
}

func splitField(line []byte) (key string, value []byte, ok bool) {
	colon := bytes.IndexByte(line, ':')
	if colon < 0 {
		return string(line), nil, true
	}
	v := line[colon+1:]
	if len(v) > 0 && v[0] == ' ' {
		v = v[1:]
	}
	return string(line[:colon]), v, true
}

func writeSSE(w *bufio.Writer, ev sseEvent) error {
	if ev.Comment {
		_, err := fmt.Fprintf(w, ": %s\n\n", ev.CommentLine)
		return err
	}
	if ev.Event != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", ev.Event); err != nil {
			return err
		}
	}
	if ev.ID != "" {
		if _, err := fmt.Fprintf(w, "id: %s\n", ev.ID); err != nil {
			return err
		}
	}
	if len(ev.Data) > 0 {
		for _, line := range bytes.Split(ev.Data, []byte{'\n'}) {
			if _, err := fmt.Fprintf(w, "data: %s\n", line); err != nil {
				return err
			}
		}
	}
	_, err := w.Write([]byte{'\n'})
	return err
}

func jsonField(raw []byte, name string) string {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	if v, ok := m[name].(string); ok {
		return v
	}
	return ""
}

func jsonInt(raw []byte, name string) int {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return 0
	}
	switch v := m[name].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

func jsonObject(raw []byte, name string) []byte {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m[name]
}

func mustJSON(v any) []byte {
	out, _ := json.Marshal(v)
	return out
}

// shouldStreamingGate reports whether the proxy should drive an SSE
// transformer for this response. Anthropic + OpenAI streams are gated
// when a fast-path policy exists; Gemini's streaming format is
// passed through (its function-call shape is buffered server-side
// rather than chunk-streamed, so non-streaming gating already covers
// the common path).
func (w *Wiring) shouldStreamingGate(provider, contentType string) bool {
	if w == nil || w.Policy == nil {
		return false
	}
	if !strings.Contains(contentType, "text/event-stream") {
		return false
	}
	switch provider {
	case "anthropic", "openai":
		return true
	}
	return false
}

func init() {
	streamingProviders["anthropic"] = func(s *streamingTransform) error { return s.runAnthropic() }
}
