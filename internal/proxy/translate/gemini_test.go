package translate

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnthropicToGemini_TextRoles(t *testing.T) {
	req := AnthropicReq{
		System: json.RawMessage(`"you are helpful"`),
		Messages: []AnthropicMessage{
			{Role: "user", Content: json.RawMessage(`"hi"`)},
			{Role: "assistant", Content: json.RawMessage(`"hello"`)},
		},
	}
	out, _, err := AnthropicToGemini(req)
	if err != nil {
		t.Fatal(err)
	}
	if out.SystemInstruction == nil || out.SystemInstruction.Parts[0].Text != "you are helpful" {
		t.Errorf("system not preserved: %+v", out.SystemInstruction)
	}
	if len(out.Contents) != 2 {
		t.Fatalf("contents = %d", len(out.Contents))
	}
	if out.Contents[0].Role != "user" || out.Contents[0].Parts[0].Text != "hi" {
		t.Errorf("user content = %+v", out.Contents[0])
	}
	if out.Contents[1].Role != "model" || out.Contents[1].Parts[0].Text != "hello" {
		t.Errorf("assistant content = %+v", out.Contents[1])
	}
}

func TestAnthropicToGemini_ToolCalls(t *testing.T) {
	content := `[
		{"type":"text","text":"thinking"},
		{"type":"tool_use","id":"abc","name":"search","input":{"q":"go"}}
	]`
	req := AnthropicReq{
		Messages: []AnthropicMessage{{Role: "assistant", Content: json.RawMessage(content)}},
		Tools: []AnthropicTool{{Name: "search", Description: "search", InputSchema: map[string]any{"q": "string"}}},
	}
	out, _, err := AnthropicToGemini(req)
	if err != nil {
		t.Fatal(err)
	}
	parts := out.Contents[0].Parts
	if len(parts) != 2 {
		t.Fatalf("parts = %d", len(parts))
	}
	if parts[1].FunctionCall == nil || parts[1].FunctionCall.Name != "search" {
		t.Errorf("function call = %+v", parts[1].FunctionCall)
	}
	if len(out.Tools) != 1 || out.Tools[0].FunctionDeclarations[0].Name != "search" {
		t.Errorf("tools = %+v", out.Tools)
	}
}

func TestRoundTrip_Anthropic_Gemini_TextSurvives(t *testing.T) {
	original := AnthropicReq{
		System: json.RawMessage(`"sys"`),
		Messages: []AnthropicMessage{
			{Role: "user", Content: json.RawMessage(`"q"`)},
			{Role: "assistant", Content: json.RawMessage(`"a"`)},
		},
	}
	mid, _, err := AnthropicToGemini(original)
	if err != nil {
		t.Fatal(err)
	}
	back, _, err := GeminiToAnthropic(mid)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Messages) != 2 {
		t.Fatalf("messages = %d", len(back.Messages))
	}
	var blocks []map[string]any
	_ = json.Unmarshal(back.Messages[0].Content, &blocks)
	if blocks[0]["text"] != "q" {
		t.Errorf("user text lost: %v", blocks)
	}
}

func TestOpenAIToGemini_Chained(t *testing.T) {
	req := OpenAIReq{
		Messages: []OpenAIMessage{
			{Role: "system", Content: json.RawMessage(`"sys"`)},
			{Role: "user", Content: json.RawMessage(`"hello"`)},
		},
	}
	out, _, err := OpenAIToGemini(req)
	if err != nil {
		t.Fatal(err)
	}
	if out.SystemInstruction == nil || out.SystemInstruction.Parts[0].Text != "sys" {
		t.Errorf("system lost: %+v", out.SystemInstruction)
	}
	if !strings.Contains(out.Contents[0].Parts[0].Text, "hello") {
		t.Errorf("user lost: %+v", out.Contents)
	}
}

func TestAnthropicToGemini_DropsThinking(t *testing.T) {
	content := `[
		{"type":"thinking","thinking":"hmm","signature":"x"},
		{"type":"text","text":"answer"}
	]`
	req := AnthropicReq{Messages: []AnthropicMessage{{Role: "assistant", Content: json.RawMessage(content)}}}
	out, warnings, _ := AnthropicToGemini(req)
	if len(out.Contents[0].Parts) != 1 {
		t.Errorf("thinking should be dropped, got %d parts", len(out.Contents[0].Parts))
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "thinking") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected warning about thinking block; got %v", warnings)
	}
}
