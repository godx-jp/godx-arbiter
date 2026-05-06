package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/godx-team/godx-arbiter/internal/policy"
)

// runOpenAI implements OpenAI's Chat Completions SSE streaming
// protocol. Tool calls arrive as delta chunks:
//
//	data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_x",
//	  "function":{"name":"Bash","arguments":"{\"command\":\""}}]}}]}
//
// The arguments JSON is assembled across many chunks. We accumulate
// them per (choice, index), and on the *first* finished argument
// stream (signaled either by the next tool_call appearing, or by
// finish_reason in a subsequent chunk) we run policy.Eval. On deny we
// rewrite the buffered tool_calls into a single synthetic refusal:
// arguments become {"arbiter_refused":true,"reason":"..."} and
// finish_reason flips to "tool_calls" so the calling agent re-runs
// with the refusal as a tool result.
func (s *streamingTransform) runOpenAI() error {
	br := bufio.NewReaderSize(s.in, 64*1024)
	out := bufio.NewWriterSize(s.flushingWriter(), 64*1024)
	defer out.Flush()

	var p openaiPending
	resetPending := func() {
		p = openaiPending{calls: map[int]*openaiCallBuf{}}
	}
	resetPending()

	emitRefusal := func(reason string) error {
		// Build a single synthetic SSE chunk that overrides the buffered
		// tool_calls with a refusal payload, then a finish chunk.
		var calls []map[string]any
		for idx, c := range p.calls {
			calls = append(calls, map[string]any{
				"index": idx,
				"id":    c.ID,
				"type":  "function",
				"function": map[string]any{
					"name":      c.Name,
					"arguments": fmt.Sprintf(`{"arbiter_refused":true,"reason":%q}`, reason),
				},
			})
		}
		chunk := map[string]any{
			"choices": []any{map[string]any{
				"index": 0,
				"delta": map[string]any{"tool_calls": calls},
			}},
		}
		raw, _ := json.Marshal(chunk)
		if _, err := fmt.Fprintf(out, "data: %s\n\n", raw); err != nil {
			return err
		}
		return out.Flush()
	}

	flushPending := func() error {
		for _, raw := range p.events {
			if _, err := out.Write(raw); err != nil {
				return err
			}
		}
		resetPending()
		return out.Flush()
	}

	decide := func() error {
		if s.policy == nil || len(p.calls) == 0 {
			return flushPending()
		}
		// Evaluate every accumulated call; first deny wins.
		for _, c := range p.calls {
			input := c.Args.Bytes()
			if len(input) == 0 || !json.Valid(input) {
				input = []byte(`{}`)
			}
			d := policy.Eval(s.policy, &policy.Action{
				ToolName: c.Name, ToolInput: input,
			})
			if d.Outcome == policy.OutcomeDeny {
				reason := d.Reason
				if reason == "" {
					reason = "denied by fast-path policy"
				}
				if err := emitRefusal(reason); err != nil {
					return err
				}
				resetPending()
				return out.Flush()
			}
		}
		return flushPending()
	}

	for {
		ev, err := readSSE(br)
		if err != nil {
			if errors.Is(err, io.EOF) {
				if err := flushPending(); err != nil {
					return err
				}
				return nil
			}
			return err
		}
		if ev.Comment {
			_, _ = fmt.Fprintf(out, ": %s\n\n", ev.CommentLine)
			continue
		}
		if string(ev.Data) == "[DONE]" {
			if err := flushPending(); err != nil {
				return err
			}
			_, _ = out.Write([]byte("data: [DONE]\n\n"))
			return out.Flush()
		}
		if len(ev.Data) == 0 {
			continue
		}

		buffered := serializeSSE(ev)
		consumed := s.absorbOpenAIChunk(ev.Data, &p)
		if consumed {
			p.events = append(p.events, buffered)
		} else {
			// Not a tool-call delta — flush any held tool-call work first
			// to preserve order, then forward this chunk.
			if len(p.events) > 0 {
				if err := decide(); err != nil {
					return err
				}
			}
			if _, err := out.Write(buffered); err != nil {
				return err
			}
			if err := out.Flush(); err != nil {
				return err
			}
		}

		// Finish — when the model signals end of tool_calls, evaluate.
		if isFinishChunk(ev.Data) {
			if err := decide(); err != nil {
				return err
			}
		}
	}
}

// openaiCallBuf is the assembled state of one tool_call across deltas.
type openaiCallBuf struct {
	ID   string
	Name string
	Args bytes.Buffer
}

// openaiPending is the per-decision pending state — held events that
// will either flush (allow) or be replaced by a refusal (deny).
type openaiPending struct {
	events [][]byte
	calls  map[int]*openaiCallBuf
}

func (s *streamingTransform) absorbOpenAIChunk(raw []byte, p *openaiPending) bool {
	var chunk struct {
		Choices []struct {
			Delta struct {
				ToolCalls []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &chunk); err != nil {
		return false
	}
	any := false
	for _, c := range chunk.Choices {
		for _, tc := range c.Delta.ToolCalls {
			any = true
			buf, ok := p.calls[tc.Index]
			if !ok {
				buf = &openaiCallBuf{}
				p.calls[tc.Index] = buf
			}
			if tc.ID != "" {
				buf.ID = tc.ID
			}
			if tc.Function.Name != "" {
				buf.Name = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				buf.Args.WriteString(tc.Function.Arguments)
			}
		}
	}
	return any
}

func isFinishChunk(raw []byte) bool {
	var chunk struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &chunk); err != nil {
		return false
	}
	for _, c := range chunk.Choices {
		if c.FinishReason != "" {
			return true
		}
	}
	return false
}

func serializeSSE(ev sseEvent) []byte {
	var b bytes.Buffer
	if ev.Event != "" {
		fmt.Fprintf(&b, "event: %s\n", ev.Event)
	}
	if ev.ID != "" {
		fmt.Fprintf(&b, "id: %s\n", ev.ID)
	}
	if len(ev.Data) > 0 {
		for _, line := range bytes.Split(ev.Data, []byte{'\n'}) {
			fmt.Fprintf(&b, "data: %s\n", line)
		}
	}
	b.WriteByte('\n')
	return b.Bytes()
}

// Wire openai into the dispatcher.
func init() {
	streamingProviders["openai"] = func(s *streamingTransform) error { return s.runOpenAI() }
	_ = strings.Contains
}

var streamingProviders = map[string]func(*streamingTransform) error{}
