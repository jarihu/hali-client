package publishing

import (
	"context"
	"sync"
)

// TorrentPublishedEvent is emitted by the daemon after a torrent is finalized
// and queued for registry publication. Hooks must not block and must not affect
// the publish outcome.
type TorrentPublishedEvent struct {
	InfoHash   string
	ContentDir string
}

// Hook is a subscriber for publishing side-effects.
type Hook interface {
	OnTorrentPublished(ctx context.Context, e TorrentPublishedEvent)
}

var (
	mu    sync.Mutex
	hooks []Hook
)

// Register adds a hook to the global registry. Safe to call concurrently.
func Register(h Hook) {
	mu.Lock()
	hooks = append(hooks, h)
	mu.Unlock()
}

// Emit dispatches an event to all registered hooks. Each hook runs in its own
// goroutine so Emit never blocks the caller.
func Emit(ctx context.Context, e TorrentPublishedEvent) {
	mu.Lock()
	hs := make([]Hook, len(hooks))
	copy(hs, hooks)
	mu.Unlock()
	for _, h := range hs {
		go h.OnTorrentPublished(ctx, e)
	}
}
