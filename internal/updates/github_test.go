package updates

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newGitHubFixture(t *testing.T, handler http.HandlerFunc) *GitHub {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := NewGitHub("token")
	client.baseURL = server.URL
	return client
}

func TestAppUpdateCache(t *testing.T) {
	requests := 0
	client := newGitHubFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if request.URL.Path != "/repos/owner/repo/releases/latest" {
			t.Errorf("unexpected path: %s", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("missing auth header")
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"tag_name":"v1.5.0","published_at":"2026-01-01T00:00:00Z","body":"notes","html_url":"https://example.test/v1.5.0"}`))
	})
	ctx := context.Background()
	first := client.AppUpdate(ctx, "owner/repo", "1.2.3", time.Hour)
	if first == nil || !first.HasUpdate || first.Release == nil || first.Release.Version != "v1.5.0" {
		t.Fatalf("unexpected update: %#v", first)
	}
	second := client.AppUpdate(ctx, "owner/repo", "1.2.3", time.Hour)
	if second == nil || !second.HasUpdate {
		t.Fatalf("unexpected cached update: %#v", second)
	}
	if requests != 1 {
		t.Fatalf("expected one request, got %d", requests)
	}
	if client.AppUpdate(ctx, "owner/repo", "dev", time.Hour) != nil {
		t.Fatal("dev version should short-circuit")
	}
}

func TestFetchChangelogRangeAndCache(t *testing.T) {
	requests := 0
	client := newGitHubFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if request.URL.Path != "/repos/owner/repo/releases" {
			t.Errorf("unexpected path: %s", request.URL.Path)
		}
		if request.URL.Query().Get("per_page") != "100" {
			t.Errorf("unexpected per_page: %s", request.URL.RawQuery)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`[
			{"tag_name":"v1.5.0","published_at":"2026-03-01T00:00:00Z","body":"f","html_url":"https://example.test/1.5.0","draft":false},
			{"tag_name":"1.4.0","published_at":"2026-02-01T00:00:00Z","body":"e","html_url":"https://example.test/1.4.0","draft":false},
			{"tag_name":"1.2.0","published_at":"2026-01-01T00:00:00Z","body":"old","html_url":"https://example.test/1.2.0","draft":false}
		]`))
	})
	ctx := context.Background()
	result := client.FetchChangelog(ctx, "owner/repo", "1.2.3", "v1.5.0")
	if !result.Available || len(result.Releases) != 2 {
		t.Fatalf("unexpected changelog: %#v", result)
	}
	if result.Releases[0].Version != "v1.5.0" || result.Releases[1].Version != "1.4.0" {
		t.Fatalf("unexpected releases: %#v", result.Releases)
	}
	if result.Release == nil || result.Release.Version != "v1.5.0" {
		t.Fatalf("unexpected target release: %#v", result.Release)
	}
	if again := client.FetchChangelog(ctx, "owner/repo", "1.2.3", "v1.5.0"); !again.Available {
		t.Fatalf("cached changelog unavailable: %#v", again)
	}
	if requests != 1 {
		t.Fatalf("expected one request, got %d", requests)
	}
}

func TestReleaseURLTagVariants(t *testing.T) {
	client := newGitHubFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/repos/owner/repo/releases/tags/v1.2.4" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"tag_name":"1.2.4","html_url":"https://example.test/1.2.4"}`))
	})
	if got := client.ReleaseURL(context.Background(), "owner/repo", "1.2.4"); got != "https://example.test/1.2.4" {
		t.Fatalf("unexpected release url: %s", got)
	}
}

func TestResolveRepository(t *testing.T) {
	cases := map[string]string{
		"ghcr.io/owner/repo:1.0":     "owner/repo",
		"docker.io/owner/repo":       "owner/repo",
		"index.docker.io/owner/repo": "owner/repo",
		"nginx":                      "",
		"registry.example.com/a/b/c": "",
	}
	for image, expected := range cases {
		if got := ResolveRepository(image); got != expected {
			t.Errorf("%s: expected %q, got %q", image, expected, got)
		}
	}
}
