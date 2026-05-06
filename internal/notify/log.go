package notify

import (
	"context"
	"fmt"
	"os"
	"time"
)

// LogChannel is the universal fallback: write the question to stderr +
// a JSONL log file, then immediately return a "timeout" reply so the
// caller's on_timeout policy decides. Non-interactive by design — used
// when no other channel is reachable.
type LogChannel struct{}

// NewLogChannel constructs the channel.
func NewLogChannel() *LogChannel { return &LogChannel{} }

// Name implements Channel.
func (LogChannel) Name() string { return "log" }

// Available implements Channel — always true.
func (LogChannel) Available() bool { return true }

// Ask implements Channel.
func (LogChannel) Ask(ctx context.Context, req EscalateRequest) (Reply, error) {
	fmt.Fprintf(os.Stderr, "[arbiter notify/log] escalation: %s — context=%v — options=%v\n",
		req.Question, req.Context, req.Options)
	// Don't block: log channel is fire-and-forget. Caller treats this as
	// a timeout and applies the on_timeout fallback.
	return Reply{Timeout: true, Channel: "log", ElapsedMs: 0, Reply: ""}, nil
}

// drainCtx is currently unused but kept for symmetry with other
// channels that may consume ctx in the future.
var _ = func() time.Duration { return time.Second }
var _ = context.Background
