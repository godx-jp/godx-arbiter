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

const openaiDangerousStream = "" +
	"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n" +
	"data: {\"choices\":[{\"delta\":{\"content\":\"running\"}}]}\n\n" +
	"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_x\",\"type\":\"function\",\"function\":{\"name\":\"Bash\",\"arguments\":\"\"}}]}}]}\n\n" +
	"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"command\\\":\\\"rm -rf \"}}]}}]}\n\n" +
	"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"/etc/foo\\\"}\"}}]}}]}\n\n" +
	"data: {\"choices\":[{\"finish_reason\":\"tool_calls\"}]}\n\n" +
	"data: [DONE]\n\n"

func TestStreamingTransform_OpenAI_DenyRewritesArguments(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, openaiDangerousStream)
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
	wiring := &Wiring{Routing: &route.Table{}, Policy: pol, CLI: "codex"}
	srv := New("127.0.0.1:0").WithUpstream(UpstreamConfig{OpenAI: upstream.URL}).WithHooks(wiring.Hooks())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.ListenAndServe(ctx)
	if err := srv.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post("http://"+srv.Addr()+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"gpt-5","stream":true,"messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	body := string(out)
	if strings.Contains(body, `"command\":\"rm -rf /etc/foo`) {
		t.Errorf("dangerous arguments survived gating:\n%s", body)
	}
	// arguments is JSON-encoded inside arguments string; backslash-escaped quotes:
	if !strings.Contains(body, `arbiter_refused`) {
		t.Errorf("refusal payload missing:\n%s", body)
	}
	if !strings.Contains(body, `"content":"running"`) {
		t.Errorf("non-tool content lost:\n%s", body)
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Errorf("[DONE] sentinel lost:\n%s", body)
	}
}

func TestStreamingTransform_OpenAI_AllowedToolPasses(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, openaiDangerousStream)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer upstream.Close()

	pol := &config.Policy{Version: 1, Default: "agent"} // no deny rules
	wiring := &Wiring{Routing: &route.Table{}, Policy: pol, CLI: "codex"}
	srv := New("127.0.0.1:0").WithUpstream(UpstreamConfig{OpenAI: upstream.URL}).WithHooks(wiring.Hooks())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.ListenAndServe(ctx)
	if err := srv.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post("http://"+srv.Addr()+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"gpt-5","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	body := string(out)
	if !strings.Contains(body, "rm -rf") {
		t.Errorf("expected pass-through; tool_call lost:\n%s", body)
	}
}
