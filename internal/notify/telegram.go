package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"
)

// TelegramChannel sends a question via the Telegram Bot API and
// listens for an inline-keyboard callback or a reply message. The
// channel is configured via env vars:
//
//	GODX_ARBITER_TELEGRAM_TOKEN   bot HTTP API token
//	GODX_ARBITER_TELEGRAM_CHAT_ID destination chat
//
// We poll getUpdates rather than wiring a webhook, since most users run
// arbiter on a workstation that doesn't have a public URL.
type TelegramChannel struct {
	token  string
	chatID string
	client *http.Client

	mu        sync.Mutex
	lastUpdate int64
}

// NewTelegramChannel constructs the channel from env vars.
func NewTelegramChannel() *TelegramChannel {
	return &TelegramChannel{
		token:  os.Getenv("GODX_ARBITER_TELEGRAM_TOKEN"),
		chatID: os.Getenv("GODX_ARBITER_TELEGRAM_CHAT_ID"),
		client: &http.Client{Timeout: 65 * time.Second},
	}
}

// Name implements Channel.
func (TelegramChannel) Name() string { return "telegram" }

// Available reports whether token + chat_id are configured.
func (t *TelegramChannel) Available() bool { return t.token != "" && t.chatID != "" }

// Ask implements Channel: send a message with inline keyboard, then
// long-poll getUpdates for a callback_query whose data is one of the
// option strings.
func (t *TelegramChannel) Ask(ctx context.Context, req EscalateRequest) (Reply, error) {
	if !t.Available() {
		return Reply{}, errors.New("telegram: not configured")
	}
	start := time.Now()
	msgID, err := t.sendQuestion(ctx, req)
	if err != nil {
		return Reply{}, err
	}
	for {
		select {
		case <-ctx.Done():
			return Reply{Timeout: true}, ctx.Err()
		default:
		}
		reply, err := t.poll(ctx, req.Options)
		if err != nil {
			return Reply{}, err
		}
		if reply.Reply != "" {
			reply.Channel = "telegram"
			reply.ElapsedMs = time.Since(start).Milliseconds()
			_ = t.acknowledge(ctx, msgID, reply.Reply)
			return reply, nil
		}
	}
}

func (t *TelegramChannel) sendQuestion(ctx context.Context, req EscalateRequest) (int64, error) {
	keyboard := buildInlineKeyboard(req.Options)
	body := map[string]any{
		"chat_id":      t.chatID,
		"text":         formatQuestion(req),
		"parse_mode":   "Markdown",
		"reply_markup": map[string]any{"inline_keyboard": keyboard},
	}
	res, err := t.post(ctx, "sendMessage", body)
	if err != nil {
		return 0, err
	}
	var parsed struct {
		Result struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	if err := json.Unmarshal(res, &parsed); err != nil {
		return 0, err
	}
	return parsed.Result.MessageID, nil
}

func (t *TelegramChannel) poll(ctx context.Context, options []string) (Reply, error) {
	t.mu.Lock()
	offset := t.lastUpdate + 1
	t.mu.Unlock()
	body := map[string]any{
		"timeout":         50,
		"offset":          offset,
		"allowed_updates": []string{"message", "callback_query"},
	}
	raw, err := t.post(ctx, "getUpdates", body)
	if err != nil {
		return Reply{}, err
	}
	var resp struct {
		Result []struct {
			UpdateID      int64 `json:"update_id"`
			CallbackQuery *struct {
				ID   string `json:"id"`
				From struct {
					Username string `json:"username"`
				} `json:"from"`
				Data    string `json:"data"`
				Message struct {
					Chat struct {
						ID json.Number `json:"id"`
					} `json:"chat"`
				} `json:"message"`
			} `json:"callback_query"`
			Message *struct {
				Chat struct {
					ID json.Number `json:"id"`
				} `json:"chat"`
				From struct {
					Username string `json:"username"`
				} `json:"from"`
				Text string `json:"text"`
			} `json:"message"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return Reply{}, err
	}
	for _, u := range resp.Result {
		t.mu.Lock()
		if u.UpdateID > t.lastUpdate {
			t.lastUpdate = u.UpdateID
		}
		t.mu.Unlock()
		if u.CallbackQuery != nil && t.matchChat(u.CallbackQuery.Message.Chat.ID) {
			if normalizeOption(u.CallbackQuery.Data, options) != "" {
				return Reply{Reply: normalizeOption(u.CallbackQuery.Data, options), User: u.CallbackQuery.From.Username}, nil
			}
		}
		if u.Message != nil && t.matchChat(u.Message.Chat.ID) {
			if opt := normalizeOption(u.Message.Text, options); opt != "" {
				return Reply{Reply: opt, User: u.Message.From.Username}, nil
			}
		}
	}
	return Reply{}, nil
}

func (t *TelegramChannel) acknowledge(ctx context.Context, msgID int64, choice string) error {
	body := map[string]any{
		"chat_id":    t.chatID,
		"message_id": msgID,
		"text":       fmt.Sprintf("✓ noted — %s", choice),
	}
	_, err := t.post(ctx, "editMessageText", body)
	return err
}

func (t *TelegramChannel) post(ctx context.Context, method string, body map[string]any) ([]byte, error) {
	raw, _ := json.Marshal(body)
	u := fmt.Sprintf("https://api.telegram.org/bot%s/%s", url.PathEscape(t.token), method)
	req, err := http.NewRequestWithContext(ctx, "POST", u, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("telegram %s: %s — %s", method, resp.Status, string(out))
	}
	return out, nil
}

func (t *TelegramChannel) matchChat(id json.Number) bool {
	if want, err := strconv.ParseInt(t.chatID, 10, 64); err == nil {
		got, _ := id.Int64()
		return got == want
	}
	return string(id) == t.chatID
}

func buildInlineKeyboard(options []string) [][]map[string]any {
	row := make([]map[string]any, 0, len(options))
	for _, opt := range options {
		row = append(row, map[string]any{"text": opt, "callback_data": opt})
	}
	return [][]map[string]any{row}
}

func formatQuestion(req EscalateRequest) string {
	var ctxLines string
	for k, v := range req.Context {
		ctxLines += fmt.Sprintf("\n• *%s*: `%v`", k, v)
	}
	return fmt.Sprintf("*godx-arbiter*\n%s%s", req.Question, ctxLines)
}

func normalizeOption(s string, options []string) string {
	for _, opt := range options {
		if opt == s {
			return opt
		}
	}
	return ""
}
