package runner

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

// Renderer converts the typed event stream into something a human
// wants to read. The default in OutputStream mode prints text deltas
// inline, tool calls as `▸ tool(args)` lines, and errors prominently
// in red. For OutputFinal we suppress everything and only emit the
// concatenated assistant text at the end.
type Renderer interface {
	OnEvent(ev Event)
	OnFinish(result *RunResult)
}

// streamRenderer is the default for OutputStream — incremental
// output, friendly to a TTY.
type streamRenderer struct {
	w        io.Writer
	mu       sync.Mutex
	finalBuf strings.Builder
	tokenBuf strings.Builder
}

// NewStreamRenderer wires the live renderer.
func NewStreamRenderer(w io.Writer) Renderer { return &streamRenderer{w: w} }

func (r *streamRenderer) OnEvent(ev Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch ev.Type {
	// --- Anthropic streaming API shape (proxy mode, raw API) ---
	case "content_block_start":
		switch ev.ContentBlockType() {
		case "tool_use":
			fmt.Fprintf(r.w, "\n▸ %s", ev.ToolUseName())
		}
	case "content_block_delta":
		switch ev.ContentBlockType() {
		case "text_delta":
			fmt.Fprint(r.w, ev.TextDelta())
			r.finalBuf.WriteString(ev.TextDelta())
		case "input_json_delta":
			r.tokenBuf.WriteString(jsonStringField(ev.Delta, "partial_json"))
		}
	case "content_block_stop":
		if r.tokenBuf.Len() > 0 {
			fmt.Fprintf(r.w, "(%s)", trimToolArgs(r.tokenBuf.String()))
			r.tokenBuf.Reset()
		}
	case "message_start", "message_delta":
		// Token counts come through here but aren't worth rendering live.
	case "message_stop":
		fmt.Fprintln(r.w)

	// --- Claude Code CLI envelope (`--output-format stream-json`) ---
	case "system":
		// init metadata; not interesting for the live view
	case "assistant":
		// The CLI batches: each `assistant` event holds a complete
		// message. Render text blocks and any tool uses.
		if ev.Message == nil {
			return
		}
		for _, c := range ev.Message.Content {
			switch c.Type {
			case "text":
				if c.Text != "" {
					fmt.Fprint(r.w, c.Text)
					r.finalBuf.WriteString(c.Text)
				}
			case "tool_use":
				fmt.Fprintf(r.w, "\n▸ %s(%s)", c.Name, trimToolArgs(string(c.Input)))
			case "thinking":
				// suppressed in live render
			}
		}
		fmt.Fprintln(r.w)
	case "user":
		// Tool results coming back from the agent loop. Render a
		// short marker so the user can see the loop progressing.
		if ev.Message == nil {
			return
		}
		for _, c := range ev.Message.Content {
			if c.Type == "tool_result" {
				fmt.Fprintln(r.w, "  ↩ tool result received")
			}
		}
	case "result":
		fmt.Fprintln(r.w)

	case "error":
		if ev.Error != nil {
			fmt.Fprintf(r.w, "\nERROR: %s\n", ev.Error.Message)
		}
	}
}

func (r *streamRenderer) OnFinish(_ *RunResult) {}

// finalRenderer collects the assistant text and prints only when the
// run is done — used by --output=final and by the delegate_to MCP
// tool, which wants a single string back.
type finalRenderer struct {
	w        io.Writer
	mu       sync.Mutex
	finalBuf strings.Builder
}

// NewFinalRenderer wires the post-hoc renderer.
func NewFinalRenderer(w io.Writer) Renderer { return &finalRenderer{w: w} }

func (r *finalRenderer) OnEvent(ev Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch ev.Type {
	case "content_block_delta":
		if ev.ContentBlockType() == "text_delta" {
			r.finalBuf.WriteString(ev.TextDelta())
		}
	case "assistant":
		r.finalBuf.WriteString(ev.AssistantText())
	}
}

func (r *finalRenderer) OnFinish(result *RunResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if result != nil {
		result.FinalText = r.finalBuf.String()
	}
	fmt.Fprint(r.w, r.finalBuf.String())
	if !strings.HasSuffix(r.finalBuf.String(), "\n") {
		fmt.Fprintln(r.w)
	}
}

// jsonRenderer mirrors raw stream-json to stdout. Useful for piping
// through `jq`. The runner still tees a copy to the log file
// independently, so this is purely a passthrough render.
type jsonRenderer struct {
	w  io.Writer
	mu sync.Mutex
}

// NewJSONRenderer wires the raw passthrough.
func NewJSONRenderer(w io.Writer) Renderer { return &jsonRenderer{w: w} }

func (r *jsonRenderer) OnEvent(ev Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(ev.Raw) > 0 {
		_, _ = r.w.Write(ev.Raw)
		_, _ = r.w.Write([]byte{'\n'})
	}
}

func (r *jsonRenderer) OnFinish(_ *RunResult) {}

// quietRenderer drops everything. Used when --quiet is set; the
// runner still writes to the log file and notify channel.
type quietRenderer struct{}

// NewQuietRenderer wires the no-op renderer.
func NewQuietRenderer() Renderer                 { return &quietRenderer{} }
func (quietRenderer) OnEvent(_ Event)            {}
func (quietRenderer) OnFinish(_ *RunResult)      {}

// chooseRenderer maps spec → renderer. Centralised so the runner
// stays simple.
func chooseRenderer(spec RunSpec, w io.Writer) Renderer {
	if spec.Quiet {
		return NewQuietRenderer()
	}
	switch spec.OutputMode {
	case OutputJSON:
		return NewJSONRenderer(w)
	case OutputFinal:
		return NewFinalRenderer(w)
	default:
		return NewStreamRenderer(w)
	}
}

// trimToolArgs presents a one-line summary of a (potentially partial
// JSON) tool input string. We don't strictly parse — just collapse
// whitespace and cap length so the live render stays readable.
func trimToolArgs(s string) string {
	// Try to parse as JSON for a clean reformat; if that fails just
	// strip whitespace.
	var v any
	if err := json.Unmarshal([]byte(s), &v); err == nil {
		clean, _ := json.Marshal(v)
		s = string(clean)
	}
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}
