package agent

import (
	"context"
	"errors"
)

// MockLLM is a deterministic LLM stub for tests. It returns scripted
// replies one per Send call. When the script is exhausted, Send returns
// ErrMockExhausted.
type MockLLM struct {
	Replies []LLMReply
	Calls   []LLMRequest
	Err     error
	pos     int
}

// ErrMockExhausted indicates the script ran out.
var ErrMockExhausted = errors.New("mock LLM script exhausted")

// Send implements LLM.
func (m *MockLLM) Send(_ context.Context, req LLMRequest) (*LLMReply, error) {
	m.Calls = append(m.Calls, req)
	if m.Err != nil {
		return nil, m.Err
	}
	if m.pos >= len(m.Replies) {
		return nil, ErrMockExhausted
	}
	r := m.Replies[m.pos]
	m.pos++
	return &r, nil
}

// Reset rewinds the script.
func (m *MockLLM) Reset() { m.pos = 0; m.Calls = nil }
