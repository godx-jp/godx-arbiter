// Package tools holds the decision-support tools used by the slow-path
// agent and exposed over MCP. See docs/MCP_TOOLS.md.
//
// One canonical registry feeds both consumers — adding a tool here
// lights it up in the agent loop and the MCP server simultaneously.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

// Tool is the interface every decision-support tool implements.
type Tool interface {
	Name() string
	Description() string
	InputSchema() map[string]any
	Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error)
}

// Registry is a name-keyed table of tools. Concurrent-safe.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{tools: map[string]Tool{}} }

// Register adds a tool. Replaces any existing tool with the same name.
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
}

// Get returns the tool registered under name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// All returns the tool list, sorted by name for deterministic output.
func (r *Registry) All() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Execute runs the named tool with the given JSON input.
func (r *Registry) Execute(ctx context.Context, name string, input json.RawMessage) (json.RawMessage, error) {
	t, ok := r.Get(name)
	if !ok {
		return nil, fmt.Errorf("tools: unknown tool %q", name)
	}
	return t.Execute(ctx, input)
}

// DefaultRegistry assembles the standard set used by the agent + MCP.
//
// Tools that need external dependencies (notify channels, network) are
// wired in via context — keeps DefaultRegistry side-effect-free.
func DefaultRegistry() *Registry {
	r := NewRegistry()
	r.Register(NewAnalyzeRisk())
	r.Register(NewCheckRule())
	r.Register(NewLookupHistory())
	r.Register(NewReadFile())
	r.Register(NewListRecentActions())
	r.Register(NewGetProjectMeta())
	r.Register(NewEscalateToUser())
	r.Register(NewDelegateTo())
	return r
}
