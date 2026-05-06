// Package proxy implements the local LLM proxy described in
// docs/MULTI_CLI.md (Mode B). It exposes Anthropic-, OpenAI-, and
// Gemini-compatible endpoints, forwards to the real provider, and
// applies arbiter's decide pipeline + (Step 13) model routing on the
// way through.
//
// What's wired today (Step 11 baseline):
//
//   - HTTP server on a configurable address
//   - Health endpoints
//   - 1:1 passthrough routes for /v1/messages, /v1/chat/completions,
//     /v1beta/models/<m>:generateContent
//   - Hook for tool-gating + routing in subsequent steps
//
// Steps 12 (tool gating) and 13 (routing + translation + budget) layer
// on top of this skeleton without changing its surface.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// UpstreamConfig points each provider at its real API base.
type UpstreamConfig struct {
	Anthropic string // default: https://api.anthropic.com
	OpenAI    string // default: https://api.openai.com
	Gemini    string // default: https://generativelanguage.googleapis.com
}

// DefaultUpstream returns the public production endpoints.
func DefaultUpstream() UpstreamConfig {
	return UpstreamConfig{
		Anthropic: "https://api.anthropic.com",
		OpenAI:    "https://api.openai.com",
		Gemini:    "https://generativelanguage.googleapis.com",
	}
}

// Server is the LLM proxy.
type Server struct {
	addr     string
	upstream UpstreamConfig
	client   *http.Client
	hooks    Hooks

	mu       sync.Mutex
	hs       *http.Server
	bound    string
	readyCh  chan struct{}
	readyErr error
}

// Hooks lets higher layers (Steps 12 + 13) intercept proxy traffic
// without the proxy package having to depend on them. Each hook may be
// nil; the proxy treats nil as "no-op" (passthrough).
type Hooks struct {
	// PreForward inspects + may rewrite the outbound request body and
	// header. The third return value carries additional headers the
	// proxy will set on the response back to the client (e.g. routing
	// diagnostics like X-Arbiter-Routed-To). Used in Step 13.
	PreForward func(provider string, body []byte, header http.Header) ([]byte, http.Header, http.Header, error)

	// PostResponse inspects + may rewrite the upstream response body
	// (Step 12 tool gating, Step 13 token logging).
	PostResponse func(provider string, requestBody, responseBody []byte) ([]byte, error)
}

// New creates a proxy server with default upstream + an internal HTTP
// client tuned for streaming-friendly behavior.
func New(addr string) *Server {
	return &Server{
		addr:     addr,
		upstream: DefaultUpstream(),
		client:   &http.Client{Timeout: 5 * time.Minute},
		readyCh:  make(chan struct{}),
	}
}

// WithUpstream overrides the upstream endpoints (for tests / staging).
func (s *Server) WithUpstream(u UpstreamConfig) *Server {
	if u.Anthropic != "" {
		s.upstream.Anthropic = u.Anthropic
	}
	if u.OpenAI != "" {
		s.upstream.OpenAI = u.OpenAI
	}
	if u.Gemini != "" {
		s.upstream.Gemini = u.Gemini
	}
	return s
}

// WithHooks installs interception callbacks (Steps 12, 13).
func (s *Server) WithHooks(h Hooks) *Server { s.hooks = h; return s }

// ListenAndServe binds, serves, and shuts down on ctx cancellation.
func (s *Server) ListenAndServe(ctx context.Context) error {
	mux := http.NewServeMux()
	s.routes(mux)

	hs := &http.Server{
		Addr:              s.addr,
		Handler:           mux,
		ReadHeaderTimeout: 30 * time.Second,
	}
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		s.markReady(err)
		return err
	}

	s.mu.Lock()
	s.hs = hs
	s.bound = ln.Addr().String()
	s.mu.Unlock()
	s.markReady(nil)

	errCh := make(chan error, 1)
	go func() { errCh <- hs.Serve(ln) }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hs.Shutdown(shutdownCtx)
		return ctx.Err()
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) markReady(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readyErr == nil {
		s.readyErr = err
	}
	select {
	case <-s.readyCh:
	default:
		close(s.readyCh)
	}
}

// Ready blocks until the listener has bound (or failed to bind).
// Returns the bind error, if any.
func (s *Server) Ready(ctx context.Context) error {
	select {
	case <-s.readyCh:
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.readyErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Addr returns the actually-bound address (useful when listening on :0).
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bound != "" {
		return s.bound
	}
	return s.addr
}

func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("/v1/messages", s.proxy("anthropic", s.upstream.Anthropic))
	mux.HandleFunc("/v1/chat/completions", s.proxy("openai", s.upstream.OpenAI))
	mux.HandleFunc("/v1/responses", s.proxy("openai", s.upstream.OpenAI))
	// Gemini paths include the model name; use a path prefix.
	mux.HandleFunc("/v1beta/", s.proxy("gemini", s.upstream.Gemini))
}

// proxy returns a handler that forwards a single request to upstream.
//
// PreForward / PostResponse hooks fire on body content; streaming
// responses bypass PostResponse and stream straight through (Step 12
// will revisit this with chunk buffering).
func (s *Server) proxy(provider, upstream string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if upstream == "" {
			http.Error(w, "no upstream configured for "+provider, http.StatusBadGateway)
			return
		}
		u, err := url.Parse(upstream)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Preserve subpath + query.
		u.Path = singleJoiningSlash(u.Path, r.URL.Path)
		u.RawQuery = r.URL.RawQuery

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = r.Body.Close()

		header := r.Header.Clone()
		var extraRespHeaders http.Header
		if s.hooks.PreForward != nil {
			newBody, newHeader, extra, err := s.hooks.PreForward(provider, body, header)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if newBody != nil {
				body = newBody
			}
			if newHeader != nil {
				header = newHeader
			}
			extraRespHeaders = extra
		}

		req, err := http.NewRequestWithContext(r.Context(), r.Method, u.String(), bytesReader(body))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		req.Header = header
		// Strip hop-by-hop headers that confuse Go's transport.
		req.Header.Del("Connection")
		req.Header.Del("Upgrade")
		req.Header.Del("Host")

		resp, err := s.client.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		ct := resp.Header.Get("Content-Type")
		isStream := isStreamingContentType(ct)
		if isStream || s.hooks.PostResponse == nil {
			copyHeader(w.Header(), resp.Header)
			mergeHeaders(w.Header(), extraRespHeaders)
			w.WriteHeader(resp.StatusCode)
			_, _ = io.Copy(flushingWriter{w}, resp.Body)
			return
		}

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		newBody, err := s.hooks.PostResponse(provider, body, respBody)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		if newBody == nil {
			newBody = respBody
		}
		copyHeader(w.Header(), resp.Header)
		mergeHeaders(w.Header(), extraRespHeaders)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(newBody)))
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(newBody)
	}
}

func mergeHeaders(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

func bytesReader(b []byte) io.Reader {
	if len(b) == 0 {
		return http.NoBody
	}
	return readerFromBytes(b)
}

// readerFromBytes avoids importing bytes for one constructor. Returns
// an io.Reader over b that's good for a single read pass.
func readerFromBytes(b []byte) io.Reader { return &bytesReaderImpl{b: b} }

type bytesReaderImpl struct {
	b []byte
	o int
}

func (r *bytesReaderImpl) Read(p []byte) (int, error) {
	if r.o >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.o:])
	r.o += n
	return n, nil
}

func copyHeader(dst, src http.Header) {
	hopByHop := map[string]struct{}{
		"Connection": {}, "Keep-Alive": {}, "Proxy-Authenticate": {},
		"Proxy-Authorization": {}, "Te": {}, "Trailer": {},
		"Transfer-Encoding": {}, "Upgrade": {},
	}
	for k, vv := range src {
		if _, drop := hopByHop[k]; drop {
			continue
		}
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

func singleJoiningSlash(a, b string) string {
	aslash := len(a) > 0 && a[len(a)-1] == '/'
	bslash := len(b) > 0 && b[0] == '/'
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}

func isStreamingContentType(ct string) bool {
	for _, marker := range []string{"text/event-stream", "application/x-ndjson", "application/stream+json"} {
		if len(ct) >= len(marker) && containsFold(ct, marker) {
			return true
		}
	}
	return false
}

func containsFold(s, sub string) bool {
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			a := s[i+j]
			b := sub[j]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// flushingWriter wraps an http.ResponseWriter so each Write is flushed
// immediately. SSE / NDJSON streams need this — without it, Go's
// response buffer can swallow per-chunk latency.
type flushingWriter struct{ w http.ResponseWriter }

func (fw flushingWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if f, ok := fw.w.(http.Flusher); ok {
		f.Flush()
	}
	return n, err
}
