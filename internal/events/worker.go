package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"hali/internal/config"
	"hali/internal/profiles"
)

type Worker struct {
	queue   *Queue
	wakeCh  chan struct{}
	stopCh  chan struct{}
	stopWg  sync.WaitGroup
	loadCfg func() (config.File, error)
}

func NewWorker(queueDir string, loadCfg func() (config.File, error)) *Worker {
	return &Worker{
		queue:   NewQueue(queueDir),
		wakeCh:  make(chan struct{}, 1),
		stopCh:  make(chan struct{}),
		loadCfg: loadCfg,
	}
}

func DefaultQueueDir() string {
	return filepath.Join(config.ServiceDataDir(), "events")
}

func (w *Worker) Run() {
	w.stopWg.Add(1)
	defer w.stopWg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	w.drain()
	for {
		select {
		case <-w.stopCh:
			return
		case <-ticker.C:
			w.drain()
		case <-w.wakeCh:
			w.drain()
		}
	}
}

func (w *Worker) Stop() {
	close(w.stopCh)
	w.stopWg.Wait()
}

func (w *Worker) Enqueue(event ModelPullEvent) error {
	if err := w.queue.Enqueue(event); err != nil {
		return err
	}
	select {
	case w.wakeCh <- struct{}{}:
	default:
	}
	return nil
}

func (w *Worker) drain() {
	queued, err := w.queue.List()
	if err != nil {
		slog.Warn("events_queue_list_failed", "error", err)
		return
	}
	if len(queued) == 0 {
		return
	}
	cfg, err := w.loadCfg()
	if err != nil {
		slog.Warn("events_config_load_failed", "error", err)
		return
	}
	if !cfg.TelemetryEnabledValue() {
		return
	}
	client := NewIngestClient(cfg.RegistryIngestURLValue())
	for _, queuedEvent := range queued {
		event, attrErr := w.withPublisherAttribution(queuedEvent.Event)
		if attrErr != nil {
			slog.Warn("event_publisher_attribution_failed", "error", attrErr)
			return
		}
		if err := w.sendWithRetry(client, event); err != nil {
			if isPermanentIngestError(err) {
				slog.Warn("event_delivery_failed_permanent", "error", err, "path", queuedEvent.Path)
				if delErr := w.queue.Delete(queuedEvent.Path); delErr != nil {
					slog.Warn("event_queue_delete_failed", "path", queuedEvent.Path, "error", delErr)
					return
				}
				continue
			}
			slog.Warn("event_delivery_failed", "error", err)
			return
		}
		if err := w.queue.Delete(queuedEvent.Path); err != nil {
			slog.Warn("event_queue_delete_failed", "path", queuedEvent.Path, "error", err)
			return
		}
	}
}

func (w *Worker) withPublisherAttribution(event ModelPullEvent) (ModelPullEvent, error) {
	pubkey, err := config.LoadOrCreateNodePublicKeyHex()
	if err != nil {
		return event, err
	}
	pubkey = strings.ToLower(strings.TrimSpace(pubkey))
	if profilePub := loadProfilePubKey(); profilePub != "" {
		if profilePub == pubkey {
			pubkey = profilePub
		} else {
			return event, fmt.Errorf("profile pubkey (%s...) does not match local signing key (%s...): run `hali profile create` in the daemon data-dir context", profilePub[:8], pubkey[:8])
		}
	}
	event.PublisherPubKey = pubkey
	payload := strings.Join([]string{
		strings.TrimSpace(event.ModelID),
		strings.TrimSpace(event.Revision),
		strings.ToLower(strings.TrimSpace(event.InfoHash)),
		strings.TrimSpace(event.Magnet),
		strings.TrimSpace(event.SourceURL),
		strings.TrimSpace(event.LocalHash),
		event.Timestamp.UTC().Format(time.RFC3339Nano),
		event.PublisherPubKey,
	}, "\n")
	sig, err := config.SignNodePayloadHex([]byte(payload))
	if err != nil {
		return event, err
	}
	event.PublisherSig = strings.ToLower(strings.TrimSpace(sig))
	return event, nil
}

func loadProfilePubKey() string {
	path := filepath.Join(config.ServiceDataDir(), "profile.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var sp profiles.SignedProfile
	if err := json.Unmarshal(data, &sp); err == nil {
		if pub := strings.ToLower(strings.TrimSpace(sp.Profile.PubKey)); pub != "" {
			return pub
		}
	}
	// Backward compatibility: older installs may store plain profile JSON.
	var p profiles.Profile
	if err := json.Unmarshal(data, &p); err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(p.PubKey))
}

func (w *Worker) sendWithRetry(client *IngestClient, event ModelPullEvent) error {
	backoff := time.Second
	for attempt := 0; attempt < 5; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := client.Send(ctx, event)
		cancel()
		if err == nil {
			return nil
		}
		if !isRetryableIngestError(err) {
			return err
		}
		select {
		case <-w.stopCh:
			return err
		case <-time.After(backoff):
		}
		if backoff < 16*time.Second {
			backoff *= 2
		}
	}
	return client.Send(context.Background(), event)
}
