package notify

import (
	"context"
	"testing"
	"time"
)

func atTime(h, m int) time.Time {
	return time.Date(2026, 5, 5, h, m, 0, 0, time.Local)
}

func TestInQuietHours_Linear(t *testing.T) {
	cases := []struct {
		window string
		when   time.Time
		want   bool
	}{
		{"22:00-07:00", atTime(23, 30), true},
		{"22:00-07:00", atTime(2, 0), true},
		{"22:00-07:00", atTime(7, 0), false},
		{"22:00-07:00", atTime(8, 0), false},
		{"09:00-17:00", atTime(12, 30), true},
		{"09:00-17:00", atTime(8, 0), false},
		{"00:00-07:00", atTime(6, 59), true},
		{"00:00-07:00", atTime(7, 0), false},
		{"bad", atTime(12, 0), false},
	}
	for _, c := range cases {
		t.Run(c.window+"@"+c.when.Format("15:04"), func(t *testing.T) {
			if got := inQuietHours(c.window, c.when); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestPolicy_FilterChannels(t *testing.T) {
	p := NewPolicy("22:00-07:00", 0)
	p.now = func() time.Time { return atTime(23, 0) }
	got := p.FilterChannels([]string{"telegram", "desktop", "log"})
	if len(got) != 2 || got[0] != "desktop" || got[1] != "log" {
		t.Errorf("got %v", got)
	}
}

func TestPolicy_FilterChannels_OutsideWindowKeepsAll(t *testing.T) {
	p := NewPolicy("22:00-07:00", 0)
	p.now = func() time.Time { return atTime(10, 0) }
	got := p.FilterChannels([]string{"telegram", "desktop"})
	if len(got) != 2 {
		t.Errorf("got %v", got)
	}
}

func TestPolicy_Dedup(t *testing.T) {
	p := NewPolicy("", 60*time.Second)
	now := atTime(10, 0)
	p.now = func() time.Time { return now }
	req := EscalateRequest{Question: "approve rm?", ProjectRoot: "/p", Options: []string{"approve", "deny"}}
	if p.IsDuplicate(req) {
		t.Error("first call should not be duplicate")
	}
	if !p.IsDuplicate(req) {
		t.Error("second call within window should be duplicate")
	}
	now = now.Add(2 * time.Minute)
	if p.IsDuplicate(req) {
		t.Error("after window, should not be duplicate")
	}
}

func TestDispatch_DedupedShortCircuits(t *testing.T) {
	r := NewRegistry().WithPolicy(NewPolicy("", 60*time.Second))
	r.Register(&fakeChannel{name: "ok", available: true, reply: "approve"})
	req := EscalateRequest{Question: "x", Channels: []string{"ok"}}
	if _, err := r.Dispatch(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	got, err := r.Dispatch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Timeout || got.Channel != "deduped" {
		t.Errorf("expected deduped timeout, got %+v", got)
	}
}
