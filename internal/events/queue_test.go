package events

import (
	"testing"
	"time"
)

func TestQueueRoundTrip(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)
	event := ModelPullEvent{
		ModelID:   "m:7b:instruct:q4_k_m",
		Revision:  "abc123",
		InfoHash:  "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		Magnet:    "magnet:?xt=urn:btih:deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		SourceURL: "https://example.invalid/model.gguf",
		LocalHash: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Timestamp: time.Unix(123, 0).UTC(),
	}

	if err := q.Enqueue(event); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	queued, err := q.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(queued) != 1 {
		t.Fatalf("queued = %d, want 1", len(queued))
	}
	if queued[0].Event != event {
		t.Fatalf("queued event mismatch: %+v vs %+v", queued[0].Event, event)
	}
	if err := q.Delete(queued[0].Path); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	queued, err = q.List()
	if err != nil {
		t.Fatalf("List after delete: %v", err)
	}
	if len(queued) != 0 {
		t.Fatalf("queued after delete = %d, want 0", len(queued))
	}
}
