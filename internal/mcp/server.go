// Package mcp implements a minimal stdio MCP server for arbiter's
// decision-support tools.
//
// We only support the subset of MCP that's load-bearing for tool use:
//
//   - initialize / notifications/initialized
//   - tools/list
//   - tools/call
//   - ping
//
// Resources, prompts, and the full server-info dance are not needed
// here. If a client wants a feature outside this set, we return JSON-RPC
// "method not found" and the client falls through to its default
// (typically: ignore or skip the capability).
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/godx-team/godx-arbiter/internal/tools"
)

// Server is a stdio JSON-RPC 2.0 MCP server backed by a tool registry.
type Server struct {
	version  string
	registry *tools.Registry
}

// NewServer returns a server that exposes the tools in registry.
func NewServer(version string, registry *tools.Registry) *Server {
	return &Server{version: version, registry: registry}
}

// jsonrpcRequest is the wire shape of a JSON-RPC 2.0 call.
type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // null for notifications
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonrpcResponse is the wire shape of a JSON-RPC 2.0 response.
type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// JSON-RPC error codes per the MCP spec / JSON-RPC 2.0.
const (
	ErrParseError     = -32700
	ErrInvalidRequest = -32600
	ErrMethodNotFound = -32601
	ErrInvalidParams  = -32602
	ErrInternalError  = -32603
)

// ServeStdio reads newline-delimited JSON-RPC messages from r and
// writes responses to w until r reaches EOF or ctx is cancelled.
func (s *Server) ServeStdio(ctx context.Context, r io.Reader, w io.Writer) error {
	br := bufio.NewReader(r)
	var writeMu sync.Mutex
	writeJSON := func(v any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		return enc.Encode(v)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line, err := br.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			if len(line) == 0 {
				return nil
			}
		} else if err != nil {
			return err
		}
		if len(line) == 0 {
			continue
		}
		var req jsonrpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			_ = writeJSON(errResp(nil, ErrParseError, err.Error()))
			continue
		}
		if req.JSONRPC == "" {
			req.JSONRPC = "2.0"
		}
		resp := s.handle(ctx, req)
		// Notifications (no ID) get no response.
		if len(req.ID) == 0 || string(req.ID) == "null" {
			continue
		}
		if err := writeJSON(resp); err != nil {
			return err
		}
	}
}

func errResp(id json.RawMessage, code int, msg string) jsonrpcResponse {
	return jsonrpcResponse{JSONRPC: "2.0", ID: id, Error: &jsonrpcError{Code: code, Message: msg}}
}

func (s *Server) handle(ctx context.Context, req jsonrpcRequest) jsonrpcResponse {
	switch req.Method {
	case "initialize":
		return jsonrpcResponse{JSONRPC: "2.0", ID: req.ID, Result: s.initialize()}
	case "notifications/initialized":
		// fire-and-forget — no response needed.
		return jsonrpcResponse{JSONRPC: "2.0", ID: req.ID}
	case "ping":
		return jsonrpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}
	case "tools/list":
		return jsonrpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": s.toolDescriptors()}}
	case "tools/call":
		return s.handleToolCall(ctx, req)
	default:
		return errResp(req.ID, ErrMethodNotFound, "method not found: "+req.Method)
	}
}

func (s *Server) initialize() map[string]any {
	return map[string]any{
		"protocolVersion": "2024-11-05",
		"serverInfo": map[string]any{
			"name":    "godx-arbiter",
			"version": s.version,
		},
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
	}
}

func (s *Server) toolDescriptors() []map[string]any {
	out := []map[string]any{}
	for _, t := range s.registry.All() {
		out = append(out, map[string]any{
			"name":        t.Name(),
			"description": t.Description(),
			"inputSchema": t.InputSchema(),
		})
	}
	return out
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (s *Server) handleToolCall(ctx context.Context, req jsonrpcRequest) jsonrpcResponse {
	var p toolCallParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return errResp(req.ID, ErrInvalidParams, err.Error())
	}
	out, err := s.registry.Execute(ctx, p.Name, p.Arguments)
	if err != nil {
		// Per MCP spec: tool errors are reported in-band so the model
		// can react to them, with isError set on the result.
		return jsonrpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": err.Error()},
				},
				"isError": true,
			},
		}
	}
	return jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": string(out)},
			},
		},
	}
}

// errMissing is a placeholder kept for future error variants.
var errMissing = fmt.Errorf("mcp: missing field")

func init() { _ = errMissing }
