package cmd

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestHTTPPostJSONRetriesTransientStatus(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	resp, err := httpPostJSON(server.URL, []byte(`{"ok":true}`))
	if err != nil {
		t.Fatalf("httpPostJSON: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

func TestHTTPPostJSONStopsAfterHardLimit(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	resp, err := httpPostJSON(server.URL, []byte(`{"ok":true}`))
	if err == nil {
		t.Fatal("httpPostJSON expected error for persistent 502 responses")
	}
	if resp != nil {
		resp.Body.Close()
		t.Fatal("httpPostJSON response should be nil on exhausted retryable errors")
	}
	if got := atomic.LoadInt32(&attempts); got != profilePostMaxAttempts {
		t.Fatalf("attempts = %d, want %d", got, profilePostMaxAttempts)
	}
}

func TestHTTPPostJSONDoesNotRetryPermanentClientError(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	resp, err := httpPostJSON(server.URL, []byte(`{"ok":true}`))
	if err != nil {
		t.Fatalf("httpPostJSON unexpected transport error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
}
