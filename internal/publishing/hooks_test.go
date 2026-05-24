package publishing

import (
	"context"
	"sync"
	"testing"
	"time"
)

func resetHooks() {
	mu.Lock()
	hooks = nil
	mu.Unlock()
}

type recordingHook struct {
	mu     sync.Mutex
	events []TorrentPublishedEvent
}

func (h *recordingHook) OnTorrentPublished(_ context.Context, e TorrentPublishedEvent) {
	h.mu.Lock()
	h.events = append(h.events, e)
	h.mu.Unlock()
}

func (h *recordingHook) len() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.events)
}

func TestEmitNoHooks(t *testing.T) {
	resetHooks()
	// Should not panic with zero hooks.
	Emit(t.Context(), TorrentPublishedEvent{InfoHash: "aabbcc", ContentDir: "/tmp"})
}

func TestEmitTriggersHook(t *testing.T) {
	resetHooks()
	h := &recordingHook{}
	Register(h)

	e := TorrentPublishedEvent{InfoHash: "aabbcc", ContentDir: "/tmp/models"}
	Emit(t.Context(), e)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h.len() == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if h.len() != 1 {
		t.Fatalf("expected 1 hook invocation, got %d", h.len())
	}
}

func TestEmitAsync(t *testing.T) {
	resetHooks()

	// Hook that blocks until released.
	released := make(chan struct{})
	started := make(chan struct{})
	Register(&blockingHook{started: started, released: released})

	done := make(chan struct{})
	go func() {
		Emit(t.Context(), TorrentPublishedEvent{InfoHash: "aabb", ContentDir: "/tmp"})
		close(done)
	}()

	select {
	case <-done:
		// Good — Emit returned without waiting for the hook to finish.
	case <-time.After(1 * time.Second):
		t.Fatal("Emit blocked on hook execution")
	}
	close(released) // unblock hook goroutine
}

type blockingHook struct {
	started  chan struct{}
	released chan struct{}
}

func (h *blockingHook) OnTorrentPublished(_ context.Context, _ TorrentPublishedEvent) {
	select {
	case h.started <- struct{}{}:
	default:
	}
	<-h.released
}

func TestEmitMultipleHooks(t *testing.T) {
	resetHooks()
	h1 := &recordingHook{}
	h2 := &recordingHook{}
	Register(h1)
	Register(h2)

	Emit(t.Context(), TorrentPublishedEvent{InfoHash: "deadbeef01234567890123456789012345678901", ContentDir: "/x"})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h1.len() == 1 && h2.len() == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if h1.len() != 1 || h2.len() != 1 {
		t.Fatalf("expected both hooks to be invoked once, got h1=%d h2=%d", h1.len(), h2.len())
	}
}
