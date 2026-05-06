package budget

import (
	"errors"
	"testing"
	"time"
)

func TestState_SoftThenHard(t *testing.T) {
	s := NewState(Limits{SessionSoftTokens: 100, SessionHardTokens: 200})
	s.now = func() time.Time { return time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC) }

	st, err := s.Charge("a", 50, 0, 0)
	if err != nil || st.OverSoft || st.OverHard {
		t.Errorf("first charge: %+v err=%v", st, err)
	}
	st, err = s.Charge("a", 60, 0, 0)
	if err != nil || !st.OverSoft || st.OverHard {
		t.Errorf("over-soft charge: %+v err=%v", st, err)
	}
	st, err = s.Charge("a", 110, 0, 0)
	if !errors.Is(err, ErrHardLimit) {
		t.Errorf("expected ErrHardLimit, got %v", err)
	}
	if !st.OverHard {
		t.Errorf("OverHard not set: %+v", st)
	}
}

func TestState_DailyCostResets(t *testing.T) {
	day1 := time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC)
	day2 := day1.Add(48 * time.Hour)
	current := day1
	s := NewState(Limits{DailySoftUSD: 1.0})
	s.now = func() time.Time { return current }

	if _, err := s.Charge("a", 0, 0, 0.6); err != nil {
		t.Fatal(err)
	}
	if st := s.Inspect("a"); st.OverSoft {
		t.Errorf("should not yet be over soft")
	}
	if _, err := s.Charge("a", 0, 0, 0.5); err != nil {
		t.Fatal(err)
	}
	if st := s.Inspect("a"); !st.OverSoft {
		t.Errorf("should be over soft after $1.10")
	}
	current = day2
	if _, err := s.Charge("a", 0, 0, 0.1); err != nil {
		t.Fatal(err)
	}
	if st := s.Inspect("a"); st.OverSoft {
		t.Errorf("daily bucket should have reset; got %+v", st)
	}
}
