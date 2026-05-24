package events

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"hali/internal/config"
)

type IngestClient struct {
	ingestURL  string
	httpClient *http.Client
}

type IngestHTTPError struct {
	StatusCode int
	Status     string
	Body       string
}

func (e *IngestHTTPError) Error() string {
	if strings.TrimSpace(e.Body) == "" {
		return fmt.Sprintf("ingest returned %s", e.Status)
	}
	return fmt.Sprintf("ingest returned %s: %s", e.Status, strings.TrimSpace(e.Body))
}

func NewIngestClient(baseURL string) *IngestClient {
	ingestURL := normalizeIngestURL(baseURL)
	return &IngestClient{
		ingestURL:  ingestURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *IngestClient) Send(ctx context.Context, event ModelPullEvent) error {
	exists, err := c.exists(ctx, event.InfoHash)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	body, contentType, err := encodeMultipartIngest(event)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.ingestURL, body)
	if err != nil {
		return fmt.Errorf("build ingest request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &IngestHTTPError{StatusCode: resp.StatusCode, Status: resp.Status, Body: strings.TrimSpace(string(msg))}
	}
	return nil
}

func (c *IngestClient) exists(ctx context.Context, infohash string) (bool, error) {
	checkURL, err := c.ingestExistsURL(infohash)
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, checkURL, nil)
	if err != nil {
		return false, fmt.Errorf("build ingest exists request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
		return false, nil
	default:
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return false, &IngestHTTPError{StatusCode: resp.StatusCode, Status: resp.Status, Body: strings.TrimSpace(string(msg))}
	}
}

func (c *IngestClient) ingestExistsURL(infohash string) (string, error) {
	trimmedHash := strings.ToLower(strings.TrimSpace(infohash))
	if trimmedHash == "" {
		return "", fmt.Errorf("missing infohash for ingest dedupe check")
	}
	u, err := url.Parse(c.ingestURL)
	if err != nil {
		return "", fmt.Errorf("parse ingest url: %w", err)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + trimmedHash
	return u.String(), nil
}

func isRetryableIngestError(err error) bool {
	if err == nil {
		return false
	}
	var httpErr *IngestHTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == http.StatusTooManyRequests || httpErr.StatusCode >= 500
	}
	return true
}

func isPermanentIngestError(err error) bool {
	return err != nil && !isRetryableIngestError(err)
}

func normalizeIngestURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "/ingest"
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return normalizeIngestPathSuffix(strings.TrimRight(trimmed, "/"))
	}
	u.Path = normalizeIngestPathSuffix(strings.TrimRight(u.Path, "/"))
	return u.String()
}

func normalizeIngestPathSuffix(path string) string {
	if path == "" {
		return "/ingest"
	}
	normalized := path
	for strings.HasSuffix(strings.ToLower(normalized), "/ingest/ingest") {
		normalized = normalized[:len(normalized)-len("/ingest")]
		normalized = strings.TrimRight(normalized, "/")
	}
	if strings.HasSuffix(strings.ToLower(normalized), "/ingest") {
		return normalized
	}
	return normalized + "/ingest"
}

func encodeMultipartIngest(event ModelPullEvent) (io.Reader, string, error) {
	torrentPath := filepath.Join(config.ServiceDataDir(), "torrents", strings.ToLower(strings.TrimSpace(event.InfoHash))+".torrent")
	torrentFile, err := os.Open(torrentPath)
	if err != nil {
		return nil, "", fmt.Errorf("open torrent file for ingest: %w", err)
	}
	defer torrentFile.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	if err := writeEventField(writer, "model_id", event.ModelID); err != nil {
		return nil, "", err
	}
	if err := writeEventField(writer, "revision", event.Revision); err != nil {
		return nil, "", err
	}
	if err := writeEventField(writer, "infohash", event.InfoHash); err != nil {
		return nil, "", err
	}
	if err := writeEventField(writer, "publisher_pubkey", event.PublisherPubKey); err != nil {
		return nil, "", err
	}
	if err := writeEventField(writer, "publisher_sig", event.PublisherSig); err != nil {
		return nil, "", err
	}
	if err := writeEventField(writer, "magnet", event.Magnet); err != nil {
		return nil, "", err
	}
	if err := writeEventField(writer, "source_url", event.SourceURL); err != nil {
		return nil, "", err
	}
	if err := writeEventField(writer, "local_hash", event.LocalHash); err != nil {
		return nil, "", err
	}
	if err := writeEventField(writer, "timestamp", event.Timestamp.UTC().Format(time.RFC3339Nano)); err != nil {
		return nil, "", err
	}
	if event.ModelSizeBytes > 0 {
		if err := writeEventField(writer, "model_size_bytes", strconv.FormatInt(event.ModelSizeBytes, 10)); err != nil {
			return nil, "", err
		}
	}
	if quant := strings.TrimSpace(event.Quantization); quant != "" {
		if err := writeEventField(writer, "quantization", quant); err != nil {
			return nil, "", err
		}
	}

	part, err := writer.CreateFormFile("torrent", filepath.Base(torrentPath))
	if err != nil {
		return nil, "", fmt.Errorf("create torrent form part: %w", err)
	}
	if _, err := io.Copy(part, torrentFile); err != nil {
		return nil, "", fmt.Errorf("copy torrent form part: %w", err)
	}

	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("finalize multipart body: %w", err)
	}

	return bytes.NewReader(body.Bytes()), writer.FormDataContentType(), nil
}

func writeEventField(writer *multipart.Writer, key, value string) error {
	if err := writer.WriteField(key, strings.TrimSpace(value)); err != nil {
		return fmt.Errorf("write %s field: %w", key, err)
	}
	return nil
}
