package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/godx-team/godx-arbiter/internal/tools"
)

func TestAgent_DirectApprove(t *testing.T) {
	mock := &MockLLM{Replies: []LLMReply{
		{Blocks: []Block{{Type: BlockText, Text: "Looks safe.\nARBITER_DECISION: approve"}}},
	}}
	a := New(mock)
	d := a.Decide(context.Background(), Config{
		Model:         "haiku",
		MaxIterations: 5,
		Timeout:       time.Second,
		Tools:         tools.DefaultRegistry(),
	}, Action{ToolName: "Read", ToolInput: json.RawMessage(`{"file_path":"foo"}`)})
	if d.Outcome != "approve" {
		t.Errorf("outcome = %q", d.Outcome)
	}
}

func TestAgent_DirectDenyWithReason(t *testing.T) {
	mock := &MockLLM{Replies: []LLMReply{
		{Blocks: []Block{{Type: BlockText, Text: "ARBITER_DECISION: deny — secret-bearing file"}}},
	}}
	d := New(mock).Decide(context.Background(), Config{MaxIterations: 5, Tools: tools.DefaultRegistry()},
		Action{ToolName: "Edit", ToolInput: json.RawMessage(`{"file_path":".env"}`)})
	if d.Outcome != "deny" {
		t.Errorf("outcome = %q", d.Outcome)
	}
	if d.Reason != "secret-bearing file" {
		t.Errorf("reason = %q", d.Reason)
	}
}

func TestAgent_AskQuestion(t *testing.T) {
	mock := &MockLLM{Replies: []LLMReply{
		{Blocks: []Block{{Type: BlockText, Text: "ARBITER_DECISION: ask — Approve removing node_modules?"}}},
	}}
	d := New(mock).Decide(context.Background(), Config{MaxIterations: 5, Tools: tools.DefaultRegistry()},
		Action{ToolName: "Bash"})
	if d.Outcome != "ask" {
		t.Errorf("outcome = %q", d.Outcome)
	}
	if d.Question == "" {
		t.Error("question is empty")
	}
}

func TestAgent_ToolLoop(t *testing.T) {
	mock := &MockLLM{Replies: []LLMReply{
		{Blocks: []Block{
			{Type: BlockToolUse, ToolUseID: "u1", ToolName: "analyze_risk", ToolInput: json.RawMessage(`{"tool":"Bash","input":{"command":"rm -rf /etc/foo"}}`)},
		}},
		{Blocks: []Block{{Type: BlockText, Text: "Tool says catastrophic.\nARBITER_DECISION: deny — rm -rf /etc/foo is system-critical"}}},
	}}
	d := New(mock).Decide(context.Background(), Config{MaxIterations: 5, Tools: tools.DefaultRegistry()},
		Action{ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"rm -rf /etc/foo"}`)})
	if d.Outcome != "deny" {
		t.Errorf("outcome = %q", d.Outcome)
	}
	if len(d.Trace.ToolCalls) != 1 {
		t.Errorf("tool calls recorded = %d, want 1", len(d.Trace.ToolCalls))
	}
	if d.Trace.ToolCalls[0].Name != "analyze_risk" {
		t.Errorf("tool name = %q", d.Trace.ToolCalls[0].Name)
	}
}

func TestAgent_LLMError_FailOpen(t *testing.T) {
	mock := &MockLLM{Err: errors.New("network down")}
	d := New(mock).Decide(context.Background(), Config{MaxIterations: 5, Timeout: 100 * time.Millisecond, OnError: "approve"},
		Action{ToolName: "Read"})
	if d.Outcome != "approve" {
		t.Errorf("outcome = %q (want approve per on_error: approve)", d.Outcome)
	}
}

func TestAgent_LLMError_FailClosed(t *testing.T) {
	mock := &MockLLM{Err: errors.New("network down")}
	d := New(mock).Decide(context.Background(), Config{MaxIterations: 5, Timeout: 100 * time.Millisecond, OnError: "deny"},
		Action{ToolName: "Read"})
	if d.Outcome != "deny" {
		t.Errorf("outcome = %q (want deny per on_error: deny)", d.Outcome)
	}
}

func TestAgent_MaxIterations(t *testing.T) {
	// Always return a tool_use → loop hits max iterations.
	mock := &MockLLM{}
	for i := 0; i < 12; i++ {
		mock.Replies = append(mock.Replies, LLMReply{Blocks: []Block{
			{Type: BlockToolUse, ToolUseID: "x", ToolName: "analyze_risk", ToolInput: json.RawMessage(`{"tool":"Bash"}`)},
		}})
	}
	d := New(mock).Decide(context.Background(), Config{MaxIterations: 3, Tools: tools.DefaultRegistry(), OnTimeout: "deny"},
		Action{ToolName: "Bash"})
	if d.Outcome != "deny" {
		t.Errorf("outcome = %q", d.Outcome)
	}
	if d.Trace.Iters != 3 {
		t.Errorf("iters = %d", d.Trace.Iters)
	}
}

func TestAgent_JSONDecisionFallback(t *testing.T) {
	mock := &MockLLM{Replies: []LLMReply{
		{Blocks: []Block{{Type: BlockText, Text: `{"decision":"deny","reason":"json mode"}`}}},
	}}
	d := New(mock).Decide(context.Background(), Config{MaxIterations: 3},
		Action{ToolName: "Edit"})
	if d.Outcome != "deny" || d.Reason != "json mode" {
		t.Errorf("got %+v", d)
	}
}

func TestAgent_NoMarker_DefaultsToDeny(t *testing.T) {
	mock := &MockLLM{Replies: []LLMReply{
		{Blocks: []Block{{Type: BlockText, Text: "I think this is fine but I forgot the marker."}}},
	}}
	d := New(mock).Decide(context.Background(), Config{MaxIterations: 3},
		Action{ToolName: "Edit"})
	if d.Outcome != "deny" {
		t.Errorf("outcome = %q", d.Outcome)
	}
}
