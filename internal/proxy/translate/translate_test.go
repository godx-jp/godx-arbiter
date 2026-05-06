package translate

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnthropicToOpenAI_TextOnly(t *testing.T) {
	req := AnthropicReq{
		Model:     "claude-sonnet-4-6",
		MaxTokens: 1024,
		System:    json.RawMessage(`"You are helpful."`),
		Messages: []AnthropicMessage{
			{Role: "user", Content: json.RawMessage(`"Hi"`)},
			{Role: "assistant", Content: json.RawMessage(`"Hello!"`)},
		},
	}
	out, _, err := AnthropicToOpenAI(req)
	if err != nil {
		t.Fatal(err)
	}
	if out.Messages[0].Role != "system" {
		t.Errorf("first message role = %q", out.Messages[0].Role)
	}
	var sys string
	_ = json.Unmarshal(out.Messages[0].Content, &sys)
	if sys != "You are helpful." {
		t.Errorf("system = %q", sys)
	}
	if len(out.Messages) != 3 {
		t.Errorf("messages = %d, want 3", len(out.Messages))
	}
}

func TestAnthropicToOpenAI_ToolUse(t *testing.T) {
	content := `[
		{"type":"text","text":"thinking..."},
		{"type":"tool_use","id":"abc","name":"search","input":{"q":"go"}}
	]`
	req := AnthropicReq{
		Model: "claude-sonnet-4-6",
		Messages: []AnthropicMessage{
			{Role: "assistant", Content: json.RawMessage(content)},
		},
		Tools: []AnthropicTool{{Name: "search", Description: "search the web", InputSchema: map[string]any{"q": "string"}}},
	}
	out, _, err := AnthropicToOpenAI(req)
	if err != nil {
		t.Fatal(err)
	}
	m := out.Messages[0]
	if len(m.ToolCalls) != 1 {
		t.Fatalf("tool_calls = %d", len(m.ToolCalls))
	}
	if m.ToolCalls[0].Function.Name != "search" {
		t.Errorf("name = %q", m.ToolCalls[0].Function.Name)
	}
	if !strings.Contains(m.ToolCalls[0].Function.Arguments, `"q":"go"`) {
		t.Errorf("arguments = %q", m.ToolCalls[0].Function.Arguments)
	}
	if len(out.Tools) != 1 {
		t.Errorf("tools = %d", len(out.Tools))
	}
}

func TestAnthropicToOpenAI_ToolResult(t *testing.T) {
	content := `[{"type":"tool_result","tool_use_id":"abc","content":"42"}]`
	req := AnthropicReq{
		Messages: []AnthropicMessage{
			{Role: "user", Content: json.RawMessage(content)},
		},
	}
	out, _, _ := AnthropicToOpenAI(req)
	if len(out.Messages) == 0 {
		t.Fatal("no messages")
	}
	last := out.Messages[len(out.Messages)-1]
	if last.Role != "tool" {
		t.Errorf("role = %q", last.Role)
	}
	if last.ToolCallID != "abc" {
		t.Errorf("tool_call_id = %q", last.ToolCallID)
	}
}

func TestRoundTrip_TextSurvives(t *testing.T) {
	original := AnthropicReq{
		Model: "claude-sonnet-4-6",
		Messages: []AnthropicMessage{
			{Role: "user", Content: json.RawMessage(`"Tell me a joke"`)},
		},
	}
	openai, _, err := AnthropicToOpenAI(original)
	if err != nil {
		t.Fatal(err)
	}
	back, _, err := OpenAIToAnthropic(openai)
	if err != nil {
		t.Fatal(err)
	}
	if back.Model != original.Model {
		t.Errorf("model lost in round trip: %q vs %q", back.Model, original.Model)
	}
	if len(back.Messages) != 1 {
		t.Fatalf("messages = %d", len(back.Messages))
	}
	var blocks []map[string]any
	_ = json.Unmarshal(back.Messages[0].Content, &blocks)
	if len(blocks) == 0 || blocks[0]["text"] != "Tell me a joke" {
		t.Errorf("text not preserved: %v", blocks)
	}
}

func TestEstimateCost_Known(t *testing.T) {
	got := EstimateCost("claude-haiku-4-5-20251001", 1_000_000, 1_000_000)
	if got != 6.0 {
		t.Errorf("got %v, want 6.0", got)
	}
}

func TestEstimateCost_Unknown(t *testing.T) {
	if got := EstimateCost("totally-fake-model", 1000, 500); got != 0 {
		t.Errorf("unknown model should be 0, got %v", got)
	}
}
