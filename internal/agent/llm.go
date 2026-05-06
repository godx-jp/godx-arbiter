package agent

import (
	"context"
	"encoding/json"
)

// LLM is the minimal model surface the agent needs. The Anthropic
// adapter implements it for production; tests use a deterministic mock
// (mock.go).
type LLM interface {
	Send(ctx context.Context, req LLMRequest) (*LLMReply, error)
}

// LLMRequest is one model call.
type LLMRequest struct {
	System    string
	Turns     []Turn
	Tools     []ToolDef
	Model     string
	MaxTokens int64
}

// LLMReply is the model's response, normalized.
type LLMReply struct {
	Blocks []Block
	Tokens *TokenCounts
	Stop   string // "end_turn" | "tool_use" | "max_tokens" | ...
}

// TokenCounts is what we record in the eventlog + usage ledger.
type TokenCounts struct {
	Input  int
	Output int
}

// Turn is a single role + blocks pair sent to the model.
type Turn struct {
	Role   string // "user" | "assistant"
	Blocks []Block
}

// BlockType discriminates Block variants.
type BlockType string

const (
	BlockText       BlockType = "text"
	BlockToolUse    BlockType = "tool_use"
	BlockToolResult BlockType = "tool_result"
)

// Block carries the union of text + tool_use + tool_result fields. The
// LLM driver picks the right variant by Type.
type Block struct {
	Type BlockType

	// BlockText
	Text string

	// BlockToolUse
	ToolUseID string
	ToolName  string
	ToolInput json.RawMessage

	// BlockToolResult
	IsError bool
}

// ToolDef is what we hand the model so it can issue tool_use calls.
type ToolDef struct {
	Name        string
	Description string
	InputSchema map[string]any
}
