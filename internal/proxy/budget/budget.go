// Package budget enforces token / cost limits configured in
// rules.md.  Soft limits trigger a warning + cheap-routing preference;
// hard limits trigger a deny + notification.
//
// Budgets are tracked using the same JSONL ledger that internal/usage
// summarizes — there is one source of truth for "how much have we
// spent today".
package budget

import (
	"errors"
	"sync"
	"time"

	"github.com/godx-team/godx-arbiter/internal/usage"
)

// Limits captures the per-project budget configuration.
type Limits struct {
	SessionSoftTokens int     // 0 means no limit
	SessionHardTokens int     // 0 means no limit
	DailySoftUSD      float64 // 0 means no limit
	DailyHardUSD      float64 // 0 means no limit
}

// State is the running counters consulted on every model call.
type State struct {
	mu              sync.RWMutex
	now             func() time.Time
	limits          Limits
	sessionTokens   map[string]int
	dailyCostBucket time.Time
	dailyCostUSD    float64
}

// NewState builds a State seeded with the given limits.
func NewState(limits Limits) *State {
	return &State{
		now:           time.Now,
		limits:        limits,
		sessionTokens: map[string]int{},
	}
}

// ErrHardLimit is returned by Charge when a request would exceed a hard
// limit. Callers are expected to deny and notify the user.
var ErrHardLimit = errors.New("budget: hard limit exceeded")

// State returned by Inspect is what callers use to decide whether to
// downgrade to a cheaper model.
type Status struct {
	SessionTokens int
	DailyUSD      float64
	OverSoft      bool
	OverHard      bool
	Reason        string
}

// Inspect reports the running status without recording a new charge.
func (s *State) Inspect(session string) Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := Status{
		SessionTokens: s.sessionTokens[session],
		DailyUSD:      s.dailyCostUSD,
	}
	if s.limits.SessionHardTokens > 0 && st.SessionTokens > s.limits.SessionHardTokens {
		st.OverHard, st.Reason = true, "session hard token limit"
	} else if s.limits.DailyHardUSD > 0 && st.DailyUSD > s.limits.DailyHardUSD {
		st.OverHard, st.Reason = true, "daily hard cost limit"
	} else if s.limits.SessionSoftTokens > 0 && st.SessionTokens > s.limits.SessionSoftTokens {
		st.OverSoft, st.Reason = true, "session soft token limit"
	} else if s.limits.DailySoftUSD > 0 && st.DailyUSD > s.limits.DailySoftUSD {
		st.OverSoft, st.Reason = true, "daily soft cost limit"
	}
	return st
}

// Charge records token + cost for a session and returns the post-charge
// status. ErrHardLimit is returned when the new charge crosses a hard
// limit; the charge is still recorded so downstream usage reports are
// accurate.
func (s *State) Charge(session string, inputTokens, outputTokens int, cost float64) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionTokens[session] += inputTokens + outputTokens
	day := s.now().Truncate(24 * time.Hour)
	if !day.Equal(s.dailyCostBucket) {
		s.dailyCostBucket = day
		s.dailyCostUSD = 0
	}
	s.dailyCostUSD += cost

	st := Status{SessionTokens: s.sessionTokens[session], DailyUSD: s.dailyCostUSD}
	switch {
	case s.limits.SessionHardTokens > 0 && st.SessionTokens > s.limits.SessionHardTokens:
		st.OverHard, st.Reason = true, "session hard token limit"
		return st, ErrHardLimit
	case s.limits.DailyHardUSD > 0 && st.DailyUSD > s.limits.DailyHardUSD:
		st.OverHard, st.Reason = true, "daily hard cost limit"
		return st, ErrHardLimit
	case s.limits.SessionSoftTokens > 0 && st.SessionTokens > s.limits.SessionSoftTokens:
		st.OverSoft, st.Reason = true, "session soft token limit"
	case s.limits.DailySoftUSD > 0 && st.DailyUSD > s.limits.DailySoftUSD:
		st.OverSoft, st.Reason = true, "daily soft cost limit"
	}
	return st, nil
}

// HydrateFromLedger populates the daily cost counter from the usage
// ledger so a freshly-started arbiter doesn't forget the day's history.
// Best-effort — errors fall back to a clean slate.
func (s *State) HydrateFromLedger() {
	now := s.now()
	day := now.Truncate(24 * time.Hour)
	cost, err := dailyCostUSD(day)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dailyCostBucket = day
	s.dailyCostUSD = cost
}

// dailyCostUSD scans the usage ledger for entries since the start of
// the given day and returns the summed cost.
func dailyCostUSD(dayStart time.Time) (float64, error) {
	report, err := usage.Report(usage.ReportOpts{Since: dayStart})
	if err != nil {
		return 0, err
	}
	// Report() returns a human-readable string. We re-walk the ledger
	// here to keep the numeric path numeric.
	return parseTotalCost(report), nil
}

// parseTotalCost extracts the trailing "$x.yyyy" from the totals line.
func parseTotalCost(report string) float64 {
	const marker = "Total:"
	idx := indexOf(report, marker)
	if idx < 0 {
		return 0
	}
	rest := report[idx+len(marker):]
	dollar := indexOf(rest, "$")
	if dollar < 0 {
		return 0
	}
	rest = rest[dollar+1:]
	end := 0
	for end < len(rest) && (rest[end] == '.' || (rest[end] >= '0' && rest[end] <= '9')) {
		end++
	}
	var v float64
	for i := 0; i < end; i++ {
		c := rest[i]
		if c == '.' {
			frac := 0.1
			for j := i + 1; j < end; j++ {
				v += float64(rest[j]-'0') * frac
				frac /= 10
			}
			return v
		}
		v = v*10 + float64(c-'0')
	}
	return v
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
