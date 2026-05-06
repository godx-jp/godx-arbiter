package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// AnthropicLLM is the production LLM driver. It wraps the official
// Anthropic Go SDK.
//
// API key resolution: the SDK's NewClient honors ANTHROPIC_API_KEY by
// default; we expose explicit construction for tests.
type AnthropicLLM struct {
	client anthropic.Client
}

// NewAnthropicLLM constructs the driver. Returns an error if no API
// key is reachable (env var unset and none supplied). The error is
// surfaced so callers can fall back to ADR-005 fail-open semantics.
func NewAnthropicLLM(opts ...option.RequestOption) (*AnthropicLLM, error) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" && os.Getenv("ANTHROPIC_AUTH_TOKEN") == "" {
		// Allow override via opts in tests / sandboxes.
		if len(opts) == 0 {
			return nil, errors.New("anthropic: no ANTHROPIC_API_KEY in env")
		}
	}
	c := anthropic.NewClient(opts...)
	return &AnthropicLLM{client: c}, nil
}

// Send implements LLM. It translates the agent's normalized request
// into Anthropic's MessageNewParams shape and back.
func (a *AnthropicLLM) Send(ctx context.Context, req LLMRequest) (*LLMReply, error) {
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: req.MaxTokens,
		System:    []anthropic.TextBlockParam{{Text: req.System}},
		Messages:  toAnthropicMessages(req.Turns),
		Tools:     toAnthropicTools(req.Tools),
	}
	resp, err := a.client.Messages.New(ctx, params)
	if err != nil {
		return nil, err
	}
	return fromAnthropicResponse(resp), nil
}

func toAnthropicMessages(turns []Turn) []anthropic.MessageParam {
	out := make([]anthropic.MessageParam, 0, len(turns))
	for _, t := range turns {
		blocks := make([]anthropic.ContentBlockParamUnion, 0, len(t.Blocks))
		for _, b := range t.Blocks {
			switch b.Type {
			case BlockText:
				blocks = append(blocks, anthropic.NewTextBlock(b.Text))
			case BlockToolUse:
				blocks = append(blocks, anthropic.ContentBlockParamUnion{
					OfToolUse: &anthropic.ToolUseBlockParam{
						ID:    b.ToolUseID,
						Name:  b.ToolName,
						Input: b.ToolInput,
					},
				})
			case BlockToolResult:
				blocks = append(blocks, anthropic.NewToolResultBlock(b.ToolUseID, b.Text, b.IsError))
			}
		}
		switch t.Role {
		case "assistant":
			out = append(out, anthropic.NewAssistantMessage(blocks...))
		default:
			out = append(out, anthropic.NewUserMessage(blocks...))
		}
	}
	return out
}

func toAnthropicTools(defs []ToolDef) []anthropic.ToolUnionParam {
	out := make([]anthropic.ToolUnionParam, 0, len(defs))
	for _, d := range defs {
		schema, ok := d.InputSchema["properties"].(map[string]any)
		if !ok {
			schema = map[string]any{}
		}
		out = append(out, anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        d.Name,
				Description: anthropic.String(d.Description),
				InputSchema: anthropic.ToolInputSchemaParam{Properties: schema},
			},
		})
	}
	return out
}

func fromAnthropicResponse(resp *anthropic.Message) *LLMReply {
	r := &LLMReply{Stop: string(resp.StopReason)}
	r.Tokens = &TokenCounts{Input: int(resp.Usage.InputTokens), Output: int(resp.Usage.OutputTokens)}
	for _, c := range resp.Content {
		switch c.Type {
		case "text":
			r.Blocks = append(r.Blocks, Block{Type: BlockText, Text: c.Text})
		case "tool_use":
			input := c.Input
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			r.Blocks = append(r.Blocks, Block{
				Type:      BlockToolUse,
				ToolUseID: c.ID,
				ToolName:  c.Name,
				ToolInput: input,
			})
		}
	}
	return r
}

// describe is a small diagnostic that summarizes the SDK response — used
// for debug logging only, currently unwired but kept for future
// observability work.
func describe(resp *anthropic.Message) string {
	return fmt.Sprintf("model=%s stop=%s blocks=%d in=%d out=%d",
		resp.Model, resp.StopReason, len(resp.Content),
		resp.Usage.InputTokens, resp.Usage.OutputTokens)
}

func init() { _ = describe }
