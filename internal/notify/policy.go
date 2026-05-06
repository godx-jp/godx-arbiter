package notify

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Policy filters and dedup'es escalation requests before they hit the
// channel layer. The layer is intentionally separate from the channels
// themselves so backends stay simple.
type Policy struct {
	// QuietHours, when set, suppresses noisy channels (Telegram by
	// default) during the configured window. Format: "HH:MM-HH:MM" in
	// the local timezone. Crossing midnight is supported (e.g.
	// "22:00-07:00").
	QuietHours string

	// QuietSuppress is the list of channel names skipped during quiet
	// hours. Channels not in the list still fire. Default:
	// {"telegram"}.
	QuietSuppress []string

	// DedupWindow collapses identical questions seen within this window
	// into a single notification (rules.md "Notification policy"
	// example: 60s). Zero disables dedup.
	DedupWindow time.Duration

	now func() time.Time

	mu       sync.Mutex
	recent   map[string]time.Time // hashKey → first-seen
}

// NewPolicy builds a Policy from rules.md-shaped inputs.
func NewPolicy(quietHours string, dedupWindow time.Duration) *Policy {
	return &Policy{
		QuietHours:    quietHours,
		QuietSuppress: []string{"telegram"},
		DedupWindow:   dedupWindow,
		now:           time.Now,
		recent:        map[string]time.Time{},
	}
}

// FilterChannels returns the subset of channels that should fire for
// this request, applying quiet-hours suppression. The order is
// preserved so caller can keep its first-match-wins dispatch.
func (p *Policy) FilterChannels(channels []string) []string {
	if p == nil || p.QuietHours == "" || !inQuietHours(p.QuietHours, p.now()) {
		return channels
	}
	out := make([]string, 0, len(channels))
	suppress := map[string]bool{}
	for _, s := range p.QuietSuppress {
		suppress[s] = true
	}
	for _, c := range channels {
		if suppress[c] {
			continue
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		// Always leave at least one channel — desktop/log — so the
		// user isn't silenced entirely on a critical question.
		return []string{"desktop", "log"}
	}
	return out
}

// IsDuplicate reports whether req has been seen within DedupWindow.
// Recording the question is implicit: returning false also marks it
// seen, so the caller is expected to drop dups, not record them
// separately.
func (p *Policy) IsDuplicate(req EscalateRequest) bool {
	if p == nil || p.DedupWindow <= 0 {
		return false
	}
	key := dedupKey(req)
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	// GC stale entries opportunistically — keeps the map bounded.
	for k, t := range p.recent {
		if now.Sub(t) > p.DedupWindow {
			delete(p.recent, k)
		}
	}
	if t, ok := p.recent[key]; ok && now.Sub(t) <= p.DedupWindow {
		return true
	}
	p.recent[key] = now
	return false
}

// dedupKey hashes question + project + tool. Channels are intentionally
// excluded so a Telegram and desktop question for the same proposal
// counts as one.
func dedupKey(req EscalateRequest) string {
	h := sha256.New()
	h.Write([]byte(req.ProjectRoot))
	h.Write([]byte{0})
	h.Write([]byte(req.Question))
	for _, opt := range req.Options {
		h.Write([]byte{0})
		h.Write([]byte(opt))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// inQuietHours reports whether the given local time falls in the
// HH:MM-HH:MM window. A start later than end means the window crosses
// midnight (e.g. 22:00-07:00).
func inQuietHours(window string, now time.Time) bool {
	start, end, ok := parseHHMMRange(window)
	if !ok {
		return false
	}
	cur := minutesOfDay(now)
	if start <= end {
		return cur >= start && cur < end
	}
	// crosses midnight
	return cur >= start || cur < end
}

func parseHHMMRange(s string) (start, end int, ok bool) {
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	a, ok1 := parseHHMM(strings.TrimSpace(parts[0]))
	b, ok2 := parseHHMM(strings.TrimSpace(parts[1]))
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	return a, b, true
}

func parseHHMM(s string) (int, bool) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, false
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

func minutesOfDay(t time.Time) int { return t.Hour()*60 + t.Minute() }
