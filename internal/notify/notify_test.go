package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dockdash/internal/config"
)

func testConfig(url string) *config.Config {
	return &config.Config{
		AppriseURL:  url,
		AppriseURLs: []string{"discord://id"},
		AppriseTags: []string{"ops"},
		Locale:      "en",
	}
}

func TestNotifySuccess(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := New(testConfig(server.URL))
	if err := client.Notify(context.Background(), "Title", "Body", TypeWarning); err != nil {
		t.Fatalf("notify: %v", err)
	}
	if body["title"] != "DockDash — Title" || body["body"] != "Body" || body["type"] != "warning" {
		t.Fatalf("unexpected payload: %#v", body)
	}
	urls, ok := body["urls"].([]any)
	if !ok || len(urls) != 1 {
		t.Fatalf("unexpected urls: %#v", body["urls"])
	}
	tags, ok := body["tag"].([]any)
	if !ok || len(tags) != 1 {
		t.Fatalf("unexpected tags: %#v", body["tag"])
	}
}

func TestNotifyRetry(t *testing.T) {
	original := retryDelays
	retryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	defer func() { retryDelays = original }()

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts < 3 {
			writer.WriteHeader(http.StatusFailedDependency)
			return
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := New(testConfig(server.URL))
	if err := client.Notify(context.Background(), "Title", "Body", TypeInfo); err != nil {
		t.Fatalf("notify: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestNotifyNoRetryOnClientError(t *testing.T) {
	original := retryDelays
	retryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	defer func() { retryDelays = original }()

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		attempts++
		writer.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	client := New(testConfig(server.URL))
	if err := client.Notify(context.Background(), "Title", "Body", TypeInfo); err == nil {
		t.Fatal("expected error")
	}
	if attempts != 1 {
		t.Fatalf("expected 1 attempt, got %d", attempts)
	}
}

func TestNotifyNotConfigured(t *testing.T) {
	client := New(&config.Config{})
	if client.Configured() {
		t.Fatal("expected not configured")
	}
	if err := client.Notify(context.Background(), "Title", "Body", TypeInfo); err != nil {
		t.Fatalf("notify: %v", err)
	}
}

func TestMessage(t *testing.T) {
	if got := Message("zh-CN", "testTitle", nil); got != "测试通知" {
		t.Fatalf("unexpected message: %s", got)
	}
	values := map[string]string{"name": "nginx", "currentVersion": "1.0", "latestVersion": "2.0"}
	if got := Message("en", "updateEntry", values); got != "nginx: 1.0 → 2.0" {
		t.Fatalf("unexpected message: %s", got)
	}
}
