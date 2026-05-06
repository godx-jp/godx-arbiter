package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/godx-team/godx-arbiter/internal/tools"
)

func TestServer_InitializeAndToolsList(t *testing.T) {
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	}, "\n") + "\n"

	var out bytes.Buffer
	srv := NewServer("test", tools.DefaultRegistry())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.ServeStdio(ctx, strings.NewReader(in), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d responses, want 2:\n%s", len(lines), out.String())
	}
	var initResp map[string]any
	_ = json.Unmarshal([]byte(lines[0]), &initResp)
	res := initResp["result"].(map[string]any)
	if res["protocolVersion"] != "2024-11-05" {
		t.Errorf("protocolVersion = %v", res["protocolVersion"])
	}

	var listResp map[string]any
	_ = json.Unmarshal([]byte(lines[1]), &listResp)
	tlist := listResp["result"].(map[string]any)["tools"].([]any)
	if len(tlist) < 4 {
		t.Errorf("got %d tools; want at least 4", len(tlist))
	}
}

func TestServer_ToolsCall_AnalyzeRisk(t *testing.T) {
	req := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"analyze_risk","arguments":{"tool":"Bash","input":{"command":"rm -rf /etc/foo"}}}}`
	var out bytes.Buffer
	srv := NewServer("test", tools.DefaultRegistry())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.ServeStdio(ctx, strings.NewReader(req+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "catastrophic") {
		t.Errorf("missing 'catastrophic' in output: %s", out.String())
	}
}

func TestServer_UnknownMethod(t *testing.T) {
	var out bytes.Buffer
	srv := NewServer("test", tools.DefaultRegistry())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = srv.ServeStdio(ctx, strings.NewReader(`{"jsonrpc":"2.0","id":9,"method":"resources/list"}`+"\n"), &out)
	if !strings.Contains(out.String(), "method not found") {
		t.Errorf("expected method not found error, got: %s", out.String())
	}
}
