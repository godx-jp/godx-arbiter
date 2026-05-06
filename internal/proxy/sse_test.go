package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/godx-team/godx-arbiter/internal/config"
	"github.com/godx-team/godx-arbiter/internal/proxy/route"
)

const dangerousToolUseStream = "" +
	"event: message_start\n" +
	"data: {\"type\":\"message_start\",\"message\":{\"id\":\"m\"}}\n\n" +
	"event: content_block_start\n" +
	"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
	"event: content_block_delta\n" +
	"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"running\"}}\n\n" +
	"event: content_block_stop\n" +
	"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
	"event: content_block_start\n" +
	"data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"u1\",\"name\":\"Bash\",\"input\":{}}}\n\n" +
	"event: content_block_delta\n" +
	"data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"command\\\":\\\"rm -rf \"}}\n\n" +
	"event: content_block_delta\n" +
	"data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"/etc/foo\\\"}\"}}\n\n" +
	"event: content_block_stop\n" +
	"data: {\"type\":\"content_block_stop\",\"index\":1}\n\n" +
	"event: message_stop\n" +
	"data: {\"type\":\"message_stop\"}\n\n"

func TestStreamingTransform_RewritesDeniedToolUse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, dangerousToolUseStream)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer upstream.Close()

	pol := &config.Policy{
		Version: 1, Default: "agent",
		Deny: []config.PolicyRule{
			{Tool: "Bash", Pattern: `rm -rf /etc`, Reason: "system path",
				Compiled: regexp.MustCompile(`rm -rf /etc`)},
		},
	}
	wiring := &Wiring{Routing: &route.Table{}, Policy: pol, CLI: "claude-code"}
	srv := New("127.0.0.1:0").WithUpstream(UpstreamConfig{Anthropic: upstream.URL}).WithHooks(wiring.Hooks())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.ListenAndServe(ctx)
	if err := srv.Ready(ctx); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Post("http://"+srv.Addr()+"/v1/messages", "application/json", strings.NewReader(`{"model":"claude-haiku-4-5-20251001","stream":true,"messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	body := string(out)

	if strings.Contains(body, `"type":"tool_use"`) {
		t.Errorf("tool_use survived gating:\n%s", body)
	}
	if !strings.Contains(body, "tool refused by godx-arbiter") {
		t.Errorf("refusal text missing:\n%s", body)
	}
	// Earlier text block should still be present untouched.
	if !strings.Contains(body, `"text":"running"`) {
		t.Errorf("non-tool content lost:\n%s", body)
	}
}

func TestStreamingTransform_AllowedToolUsePassesThrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, dangerousToolUseStream)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer upstream.Close()

	// Empty policy = no deny rules. tool_use should pass unchanged.
	pol := &config.Policy{Version: 1, Default: "agent"}
	wiring := &Wiring{Routing: &route.Table{}, Policy: pol, CLI: "claude-code"}
	srv := New("127.0.0.1:0").WithUpstream(UpstreamConfig{Anthropic: upstream.URL}).WithHooks(wiring.Hooks())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.ListenAndServe(ctx)
	if err := srv.Ready(ctx); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Post("http://"+srv.Addr()+"/v1/messages", "application/json", strings.NewReader(`{"model":"claude-haiku-4-5-20251001","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(out), `"type":"tool_use"`) {
		t.Errorf("tool_use should have passed through:\n%s", out)
	}
}

func TestReadSSE_Basic(t *testing.T) {
	const sample = "" +
		": ping\n\n" +
		"event: foo\n" +
		"data: hello\n" +
		"data: world\n\n"
	br := newBufReader(sample)
	ev, err := readSSE(br)
	if err != nil {
		t.Fatal(err)
	}
	if !ev.Comment {
		t.Errorf("expected comment, got %+v", ev)
	}
	ev, err = readSSE(br)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Event != "foo" {
		t.Errorf("event = %q", ev.Event)
	}
	if string(ev.Data) != "hello\nworld" {
		t.Errorf("data = %q", string(ev.Data))
	}
}
