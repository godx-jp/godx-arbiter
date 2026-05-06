package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/godx-team/godx-arbiter/internal/config"
	"github.com/godx-team/godx-arbiter/internal/proxy/route"
)

func TestWiring_GatesAnthropicToolUse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id": "msg",
			"model": "claude-haiku-4-5-20251001",
			"content": [
				{"type":"text","text":"running it"},
				{"type":"tool_use","id":"u1","name":"Bash","input":{"command":"rm -rf /etc/foo"}}
			],
			"usage": {"input_tokens":10, "output_tokens":4}
		}`)
	}))
	defer upstream.Close()

	pol := &config.Policy{
		Version: 1, Default: "agent",
		Deny: []config.PolicyRule{
			{Tool: "Bash", Pattern: `rm -rf /etc`, Reason: "system path", Compiled: mustCompileRegexp(`rm -rf /etc`)},
		},
	}
	wiring := &Wiring{Routing: &route.Table{}, Policy: pol, CLI: "claude-code"}
	srv := New("127.0.0.1:0").WithUpstream(UpstreamConfig{Anthropic: upstream.URL}).WithHooks(wiring.Hooks())
	wiring.Budget = nil
	defer func() { wiring.Budget = nil }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.ListenAndServe(ctx)
	if err := srv.Ready(ctx); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Post("http://"+srv.Addr()+"/v1/messages", "application/json", strings.NewReader(`{"model":"claude-haiku-4-5-20251001","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "tool refused by godx-arbiter") {
		t.Errorf("expected refusal, got: %s", body)
	}
}

func TestWiring_RoutesModel(t *testing.T) {
	var upstreamGotModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var m struct{ Model string }
		_ = json.Unmarshal(body, &m)
		upstreamGotModel = m.Model
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"x","model":"claude-haiku-4-5-20251001","content":[],"usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer upstream.Close()

	rt := route.ParseSection(`## Model routing

Default models per CLI when no rule below matches:
- Claude Code: claude-sonnet-4-6

Rules (top to bottom; first match wins):

- task: read-only-summarization
  model: claude-haiku-4-5-20251001
`)
	wiring := &Wiring{Routing: rt, CLI: "claude-code"}
	wiring.Budget = nil

	srv := New("127.0.0.1:0").WithUpstream(UpstreamConfig{Anthropic: upstream.URL}).WithHooks(wiring.Hooks())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.ListenAndServe(ctx)
	if err := srv.Ready(ctx); err != nil {
		t.Fatal(err)
	}

	body := `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"please summarize this log"}]}`
	resp, err := http.Post("http://"+srv.Addr()+"/v1/messages", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if upstreamGotModel != "claude-haiku-4-5-20251001" {
		t.Errorf("upstream model = %q (expected haiku)", upstreamGotModel)
	}
	if resp.Header.Get("X-Arbiter-Routed-To") != "claude-haiku-4-5-20251001" {
		t.Errorf("routing header missing: %v", resp.Header)
	}
}

// mustCompileRegexp keeps the test data terse — production code uses
// config.LoadPolicy which compiles via regexp.Compile too.
func mustCompileRegexp(s string) *regexp.Regexp {
	return regexp.MustCompile(s)
}
