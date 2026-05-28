package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"hali/internal/config"
)

const defaultTimeout = 10 * time.Second

type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewWithBaseURL returns a registry client pinned to an explicit base URL.
func NewWithBaseURL(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"), httpClient: httpClient}
}

// New returns a registry Client whose base URL is derived from the configured
// ingest URL by stripping the /ingest path suffix.
func New() *Client {
	ingestURL := config.DefaultRegistryIngestURL
	if cfg, err := config.Load(); err == nil {
		ingestURL = cfg.RegistryIngestURLValue()
	}
	return &Client{
		baseURL:    baseFromIngest(ingestURL),
		httpClient: &http.Client{Timeout: defaultTimeout},
	}
}

func baseFromIngest(ingestURL string) string {
	trimmed := strings.TrimSpace(ingestURL)
	u, err := url.Parse(trimmed)
	if err != nil {
		return trimmed
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if idx := strings.LastIndex(u.Path, "/"); idx >= 0 {
		u.Path = u.Path[:idx]
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// PlanFile is a single file entry in a DownloadPlan.
type PlanFile struct {
	Name         string  `json:"name"`
	Type         string  `json:"type"`
	Size         int64   `json:"size"`
	IsSharded    bool    `json:"is_sharded"`
	ShardGroupID *string `json:"shard_group_id"`
}

// ShardGroup is a set of shard files that form a single logical model variant.
type ShardGroup struct {
	ID    string     `json:"id"`
	Files []PlanFile `json:"files"`
}

// DownloadPlan is the response from GET /repo/{owner}/{repo}/download-plan.
type DownloadPlan struct {
	Owner       string       `json:"owner"`
	Repo        string       `json:"repo"`
	Mode        string       `json:"mode"`
	Files       []PlanFile   `json:"files"`
	Grouped     bool         `json:"grouped"`
	ShardGroups []ShardGroup `json:"shard_groups"`
}

// DownloadPlan fetches the download plan for the given owner/repo from the registry.
func (c *Client) DownloadPlan(ctx context.Context, owner, repo string) (*DownloadPlan, error) {
	u := fmt.Sprintf("%s/repo/%s/%s/download-plan", c.baseURL, owner, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry returned HTTP %d", resp.StatusCode)
	}
	var plan DownloadPlan
	if err := json.NewDecoder(resp.Body).Decode(&plan); err != nil {
		return nil, fmt.Errorf("decoding plan: %w", err)
	}
	return &plan, nil
}
