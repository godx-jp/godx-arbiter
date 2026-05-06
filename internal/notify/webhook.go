package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// WebhookChannel POSTs the question to a user-configured URL and
// expects a JSON body back with `{"reply": "approve|deny"}`.
//
// Configuration:
//
//	GODX_ARBITER_WEBHOOK_URL    target URL (must accept POST application/json)
//	GODX_ARBITER_WEBHOOK_TOKEN  optional bearer token
type WebhookChannel struct {
	url    string
	token  string
	client *http.Client
}

// NewWebhookChannel reads env vars and returns the channel.
func NewWebhookChannel() *WebhookChannel {
	return &WebhookChannel{
		url:    os.Getenv("GODX_ARBITER_WEBHOOK_URL"),
		token:  os.Getenv("GODX_ARBITER_WEBHOOK_TOKEN"),
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// Name implements Channel.
func (WebhookChannel) Name() string { return "webhook" }

// Available implements Channel.
func (w *WebhookChannel) Available() bool { return w.url != "" }

// Ask implements Channel.
func (w *WebhookChannel) Ask(ctx context.Context, req EscalateRequest) (Reply, error) {
	if w.url == "" {
		return Reply{}, errors.New("webhook: GODX_ARBITER_WEBHOOK_URL not set")
	}
	body, _ := json.Marshal(map[string]any{
		"session_id": req.SessionID,
		"project":    req.ProjectRoot,
		"question":   req.Question,
		"options":    req.Options,
		"context":    req.Context,
	})
	hreq, err := http.NewRequestWithContext(ctx, "POST", w.url, bytes.NewReader(body))
	if err != nil {
		return Reply{}, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	if w.token != "" {
		hreq.Header.Set("Authorization", "Bearer "+w.token)
	}
	start := time.Now()
	resp, err := w.client.Do(hreq)
	if err != nil {
		return Reply{}, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return Reply{}, err
	}
	if resp.StatusCode/100 != 2 {
		return Reply{}, fmt.Errorf("webhook: %s — %s", resp.Status, strings.TrimSpace(string(out)))
	}
	var parsed struct {
		Reply string `json:"reply"`
		User  string `json:"user"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return Reply{}, fmt.Errorf("webhook reply parse: %w", err)
	}
	if parsed.Reply == "" {
		return Reply{Timeout: true}, nil
	}
	return Reply{
		Reply:     parsed.Reply,
		User:      parsed.User,
		Channel:   "webhook",
		ElapsedMs: time.Since(start).Milliseconds(),
	}, nil
}
