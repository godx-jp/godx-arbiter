package notify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeChannel struct {
	name      string
	available bool
	reply     string
	err       error
}

func (f *fakeChannel) Name() string    { return f.name }
func (f *fakeChannel) Available() bool { return f.available }
func (f *fakeChannel) Ask(_ context.Context, _ EscalateRequest) (Reply, error) {
	if f.err != nil {
		return Reply{}, f.err
	}
	return Reply{Reply: f.reply}, nil
}

func TestDispatch_FirstAvailableWins(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeChannel{name: "off", available: false, reply: "approve"})
	r.Register(&fakeChannel{name: "on", available: true, reply: "deny"})
	got, err := r.Dispatch(context.Background(), EscalateRequest{
		Channels: []string{"off", "on"},
		Question: "?",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Reply != "deny" {
		t.Errorf("reply = %q", got.Reply)
	}
	if got.Channel != "on" {
		t.Errorf("channel = %q", got.Channel)
	}
}

func TestDispatch_AllUnavailableTimeout(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeChannel{name: "off1", available: false})
	r.Register(&fakeChannel{name: "off2", available: false})
	got, _ := r.Dispatch(context.Background(), EscalateRequest{Channels: []string{"off1", "off2"}})
	if !got.Timeout {
		t.Errorf("expected timeout, got %+v", got)
	}
}

func TestDispatch_ChannelErrorTriesNext(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeChannel{name: "broken", available: true, err: errors.New("boom")})
	r.Register(&fakeChannel{name: "ok", available: true, reply: "approve"})
	got, err := r.Dispatch(context.Background(), EscalateRequest{Channels: []string{"broken", "ok"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Reply != "approve" {
		t.Errorf("reply = %q", got.Reply)
	}
}

func TestWebhookChannel_RoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["question"] != "ok?" {
			t.Errorf("unexpected question: %v", body["question"])
		}
		_, _ = w.Write([]byte(`{"reply":"deny","user":"alice"}`))
	}))
	defer srv.Close()

	w := &WebhookChannel{url: srv.URL, client: srv.Client()}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got, err := w.Ask(ctx, EscalateRequest{Question: "ok?"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Reply != "deny" || got.User != "alice" {
		t.Errorf("got %+v", got)
	}
}
