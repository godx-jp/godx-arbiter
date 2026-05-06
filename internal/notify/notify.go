// Package notify dispatches escalation questions to the user via one
// or more channels (Telegram, desktop, webhook, log).
//
// The dispatcher tries channels in the order given in rules.md
// `notify_channels`. The first channel to receive a reply wins; the
// rest are cancelled. On timeout across all channels, the dispatcher
// returns Reply{Timeout: true} and the caller applies the rules.md
// `on_timeout` policy.
package notify

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// EscalateRequest is the structured question handed to the dispatcher.
type EscalateRequest struct {
	SessionID   string
	ProjectRoot string
	Channels    []string // ordered preference; first reply wins
	Question    string
	Options     []string // e.g. ["approve", "deny"]
	Timeout     time.Duration
	Context     map[string]any // tool, command, etc. — surfaced to the user
}

// Reply is the user's response.
type Reply struct {
	Channel   string
	Reply     string
	Timeout   bool
	ElapsedMs int64
	User      string
}

// Channel is the abstraction every notification backend implements.
type Channel interface {
	Name() string
	Available() bool
	Ask(ctx context.Context, req EscalateRequest) (Reply, error)
}

// Registry holds the configured channels.
type Registry struct {
	mu       sync.RWMutex
	channels map[string]Channel
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{channels: map[string]Channel{}} }

// Register adds (or replaces) a channel.
func (r *Registry) Register(c Channel) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.channels[c.Name()] = c
}

// Get returns the channel registered under name.
func (r *Registry) Get(name string) (Channel, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.channels[name]
	return c, ok
}

// All returns every channel.
func (r *Registry) All() []Channel {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Channel, 0, len(r.channels))
	for _, c := range r.channels {
		out = append(out, c)
	}
	return out
}

// DefaultRegistry assembles the standard channels with environment-based
// configuration. Channels that aren't reachable are still Register'd
// but report Available()==false; the dispatcher skips them.
func DefaultRegistry() *Registry {
	r := NewRegistry()
	r.Register(NewLogChannel())
	r.Register(NewDesktopChannel())
	r.Register(NewTelegramChannel())
	r.Register(NewWebhookChannel())
	return r
}

// Default is the package-global registry used by Escalate.
var Default = DefaultRegistry()

// Escalate is a convenience that dispatches via the default registry
// in the order req.Channels lists, falling back to log when nothing
// else is available.
func Escalate(ctx context.Context, req EscalateRequest) (Reply, error) {
	return Default.Dispatch(ctx, req)
}

// Dispatch tries channels in order. Returns the first non-error reply
// or Reply{Timeout:true} on overall timeout. Implementation note: it
// runs channels sequentially rather than racing them — most users
// configure 1-2 channels, and racing introduces user-confusion (which
// channel did I reply to?). Race semantics can be added later if real
// usage demands it.
func (r *Registry) Dispatch(ctx context.Context, req EscalateRequest) (Reply, error) {
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}
	if len(req.Channels) == 0 {
		req.Channels = []string{"desktop", "log"}
	}
	if len(req.Options) == 0 {
		req.Options = []string{"approve", "deny"}
	}

	var lastErr error
	for _, name := range req.Channels {
		c, ok := r.Get(name)
		if !ok {
			lastErr = fmt.Errorf("notify: unknown channel %q", name)
			continue
		}
		if !c.Available() {
			continue
		}
		reply, err := c.Ask(ctx, req)
		if err != nil {
			lastErr = err
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				return Reply{Timeout: true, Channel: name}, nil
			}
			continue
		}
		reply.Channel = c.Name()
		return reply, nil
	}
	if lastErr != nil {
		return Reply{Timeout: true}, lastErr
	}
	return Reply{Timeout: true}, errors.New("notify: no channels available")
}
