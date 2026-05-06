package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeUpstream returns a server that echoes the request body and
// records the path it was called on.
func fakeUpstream(t *testing.T) (*httptest.Server, *string) {
	t.Helper()
	var lastPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &lastPath
}

func TestServer_PassthroughAnthropic(t *testing.T) {
	upstream, lastPath := fakeUpstream(t)
	s := New("127.0.0.1:0").WithUpstream(UpstreamConfig{Anthropic: upstream.URL})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go s.ListenAndServe(ctx)
	if err := s.Ready(ctx); err != nil {
		t.Fatal(err)
	}

	body := strings.NewReader(`{"model":"claude-sonnet-4-6","messages":[]}`)
	req, _ := http.NewRequest("POST", "http://"+s.Addr()+"/v1/messages", body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(out), "claude-sonnet-4-6") {
		t.Errorf("body not echoed: %s", out)
	}
	if *lastPath != "/v1/messages" {
		t.Errorf("upstream path = %q", *lastPath)
	}
}

func TestServer_PreForwardRewrite(t *testing.T) {
	upstream, _ := fakeUpstream(t)
	s := New("127.0.0.1:0").WithUpstream(UpstreamConfig{OpenAI: upstream.URL}).WithHooks(Hooks{
		PreForward: func(provider string, body []byte, h http.Header) ([]byte, http.Header, http.Header, error) {
			return []byte(`{"rewritten":true}`), h, nil, nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.ListenAndServe(ctx)
	if err := s.Ready(ctx); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Post("http://"+s.Addr()+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(out), `"rewritten":true`) {
		t.Errorf("PreForward did not rewrite: %s", out)
	}
}

func TestServer_PostResponseRewrite(t *testing.T) {
	upstream, _ := fakeUpstream(t)
	s := New("127.0.0.1:0").WithUpstream(UpstreamConfig{Anthropic: upstream.URL}).WithHooks(Hooks{
		PostResponse: func(provider string, _, respBody []byte) ([]byte, error) {
			return []byte(`{"injected":true}`), nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.ListenAndServe(ctx)
	if err := s.Ready(ctx); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Post("http://"+s.Addr()+"/v1/messages", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(out), `"injected":true`) {
		t.Errorf("PostResponse did not rewrite: %s", out)
	}
}

