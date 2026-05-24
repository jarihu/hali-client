package events

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Queue struct {
	dir string
	mu  sync.Mutex
}

type QueuedEvent struct {
	Path  string
	Event ModelPullEvent
}

func NewQueue(dir string) *Queue {
	return &Queue{dir: dir}
}

func (q *Queue) Enqueue(event ModelPullEvent) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if err := os.MkdirAll(q.dir, 0755); err != nil {
		return fmt.Errorf("create event queue dir: %w", err)
	}
	path := filepath.Join(q.dir, q.nextFilename())
	tmpPath := path + ".tmp"
	data, err := json.MarshalIndent(event, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	if err := os.WriteFile(tmpPath, append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("write queued event: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("commit queued event: %w", err)
	}
	return nil
}

func (q *Queue) List() ([]QueuedEvent, error) {
	if err := os.MkdirAll(q.dir, 0755); err != nil {
		return nil, fmt.Errorf("create event queue dir: %w", err)
	}
	entries, err := os.ReadDir(q.dir)
	if err != nil {
		return nil, fmt.Errorf("read event queue dir: %w", err)
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		paths = append(paths, filepath.Join(q.dir, entry.Name()))
	}
	sort.Strings(paths)
	queued := make([]QueuedEvent, 0, len(paths))
	for _, path := range paths {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, fmt.Errorf("read queued event %s: %w", path, readErr)
		}
		var event ModelPullEvent
		if unmarshalErr := json.Unmarshal(data, &event); unmarshalErr != nil {
			return nil, fmt.Errorf("parse queued event %s: %w", path, unmarshalErr)
		}
		queued = append(queued, QueuedEvent{Path: path, Event: event})
	}
	return queued, nil
}

func (q *Queue) Delete(path string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (q *Queue) Update(path string, event ModelPullEvent) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	data, err := json.MarshalIndent(event, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("write queued event: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("commit queued event: %w", err)
	}
	return nil
}

func (q *Queue) nextFilename() string {
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return fmt.Sprintf("%d.json", time.Now().UnixNano())
	}
	return fmt.Sprintf("%d-%s.json", time.Now().UnixNano(), hex.EncodeToString(suffix[:]))
}
