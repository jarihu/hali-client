package daemon

import (
	"sync"
	"time"
)

const activityCap = 200

// Event is a single activity log entry.
type Event struct {
	Version   int       `json:"v"`
	Timestamp time.Time `json:"ts"`
	Kind      string    `json:"kind"`
	Message   string    `json:"msg"`
	ModelID   string    `json:"model_id,omitempty"`
}

// ActivityBuffer is a fixed-size circular buffer of Events.
// On overflow the oldest entry is silently overwritten.
type ActivityBuffer struct {
	mu    sync.Mutex
	buf   [activityCap]Event
	count int // monotonically increasing; buf[count%cap] is next write slot
}

func (b *ActivityBuffer) Append(e Event) {
	e.Version = 1
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now()
	}
	b.mu.Lock()
	b.buf[b.count%activityCap] = e
	b.count++
	b.mu.Unlock()
}

// Snapshot returns up to lastN events in oldest→newest order.
// It copies under the lock and returns without holding it.
func (b *ActivityBuffer) Snapshot(lastN int) []Event {
	b.mu.Lock()
	count := b.count
	buf := b.buf
	b.mu.Unlock()

	total := count
	if total > activityCap {
		total = activityCap
	}
	if lastN <= 0 || lastN > total {
		lastN = total
	}
	if lastN == 0 {
		return nil
	}

	result := make([]Event, lastN)
	for i := range lastN {
		p := count - lastN + i // insertion-order position
		result[i] = buf[p%activityCap]
	}
	return result
}
