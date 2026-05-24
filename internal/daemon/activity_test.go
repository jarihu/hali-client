package daemon

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestActivityBufferEmpty(t *testing.T) {
	var b ActivityBuffer
	if got := b.Snapshot(50); got != nil {
		t.Errorf("empty buffer Snapshot = %v, want nil", got)
	}
}

func TestActivityBufferOrderedOldestFirst(t *testing.T) {
	var b ActivityBuffer
	for i := range 5 {
		b.Append(Event{Kind: "test", Message: fmt.Sprintf("msg%d", i)})
	}
	events := b.Snapshot(5)
	if len(events) != 5 {
		t.Fatalf("len = %d, want 5", len(events))
	}
	for i, e := range events {
		want := fmt.Sprintf("msg%d", i)
		if e.Message != want {
			t.Errorf("events[%d].Message = %q, want %q", i, e.Message, want)
		}
	}
}

func TestActivityBufferLastNTruncates(t *testing.T) {
	var b ActivityBuffer
	for i := range 10 {
		b.Append(Event{Kind: "test", Message: fmt.Sprintf("msg%d", i)})
	}
	events := b.Snapshot(3)
	if len(events) != 3 {
		t.Fatalf("len = %d, want 3", len(events))
	}
	// Should be the 3 most recent: msg7, msg8, msg9
	for i, e := range events {
		want := fmt.Sprintf("msg%d", 7+i)
		if e.Message != want {
			t.Errorf("events[%d].Message = %q, want %q", i, e.Message, want)
		}
	}
}

func TestActivityBufferWrapAround(t *testing.T) {
	var b ActivityBuffer
	total := activityCap + 50
	for i := range total {
		b.Append(Event{Kind: "test", Message: fmt.Sprintf("msg%d", i)})
	}
	// Buffer holds the most recent activityCap entries.
	events := b.Snapshot(activityCap)
	if len(events) != activityCap {
		t.Fatalf("len = %d, want %d", len(events), activityCap)
	}
	// First entry should be msg50 (total-cap = 50).
	if events[0].Message != "msg50" {
		t.Errorf("events[0].Message = %q, want msg50", events[0].Message)
	}
	if events[activityCap-1].Message != fmt.Sprintf("msg%d", total-1) {
		t.Errorf("last event wrong: %q", events[activityCap-1].Message)
	}
}

func TestActivityBufferSetsVersion(t *testing.T) {
	var b ActivityBuffer
	b.Append(Event{Kind: "x", Message: "y"})
	events := b.Snapshot(1)
	if events[0].Version != 1 {
		t.Errorf("Version = %d, want 1", events[0].Version)
	}
}

func TestActivityBufferSetsTimestamp(t *testing.T) {
	var b ActivityBuffer
	before := time.Now()
	b.Append(Event{Kind: "x", Message: "y"})
	after := time.Now()
	events := b.Snapshot(1)
	ts := events[0].Timestamp
	if ts.Before(before) || ts.After(after) {
		t.Errorf("Timestamp %v outside [%v, %v]", ts, before, after)
	}
}

func TestActivityBufferConcurrent(t *testing.T) {
	var b ActivityBuffer
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				b.Append(Event{Kind: "race", Message: "x"})
			}
		}()
	}
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				b.Snapshot(10)
			}
		}()
	}
	wg.Wait()
}
