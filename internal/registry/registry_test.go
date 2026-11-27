package registry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestParseImageRef(t *testing.T) {
	client := New("")
	cases := map[string]ImageRef{
		"nginx":                          {Registry: dockerHubURL, Repository: "library/nginx", Tag: "latest"},
		"user/app:1.2.3":                 {Registry: dockerHubURL, Repository: "user/app", Tag: "1.2.3"},
		"ghcr.io/user/app:v1@sha256:abc": {Registry: ghcrURL, Repository: "user/app", Tag: "v1"},
		"registry.local:5000/a/b:2":      {Registry: "registry.local:5000", Repository: "a/b", Tag: "2"},
	}
	for image, expected := range cases {
		actual := client.ParseImageRef(image)
		if actual != expected {
			t.Fatalf("%s: %+v != %+v", image, actual, expected)
		}
	}
}

func TestTagComparison(t *testing.T) {
	current := ExtractSemVer("v1.2.3")
	older := ExtractSemVer("1.2.2")
	newer := ExtractSemVer("v1.3.0")
	if !SameFamily(current, newer) || !SameFamily(current, older) {
		t.Fatal("v prefix must be treated as equivalent")
	}
	if CompareSemVer(newer, current) <= 0 || CompareSemVer(older, current) >= 0 {
		t.Fatal("unexpected version ordering")
	}
	if !ContainsUpdate([]string{"1.2.2", "v1.4.0"}, "v1.2.3") {
		t.Fatal("expected update")
	}
	if ContainsUpdate([]string{"cuda-2.0.0", "v1.2.3-alpine"}, "v1.2.3") {
		t.Fatal("different suffix or family must not count as update")
	}
}

func TestScanTagPagesPaginationAndEarlyExit(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/tags", func(w http.ResponseWriter, request *http.Request) {
		page := request.URL.Query().Get("page")
		if page == "" || page == "1" {
			_, _ = w.Write([]byte(`{"count":2,"results":[{"name":"1.0.0"},{"name":"1.1.0"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"count":2,"results":[{"name":"2.0.0"}]}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client := New("")
	parse := func(data []byte, _ http.Header, location *url.URL) (parsedPage, error) {
		var payload struct {
			Count   int
			Results []struct{ Name string }
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return parsedPage{}, err
		}
		tags := []string{}
		for _, entry := range payload.Results {
			tags = append(tags, entry.Name)
		}
		next := ""
		if location.Query().Get("page") == "" {
			next = location.Scheme + "://" + location.Host + location.Path + "?page=2"
		}
		return parsedPage{Tags: tags, Next: next}, nil
	}
	result := client.scanTagPages(context.Background(), server.URL+"/tags", nil, parse, "")
	if !result.Complete || len(result.Tags) != 3 || result.Failed {
		t.Fatalf("unexpected result: %+v", result)
	}
	early := client.scanTagPages(context.Background(), server.URL+"/tags", nil, parse, "1.0.0")
	if early.Complete || !ContainsUpdate(early.Tags, "1.0.0") {
		t.Fatalf("early exit failed: %+v", early)
	}
}

func TestGetManifestDigestWithToken(t *testing.T) {
	var serverURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="`+serverURL+`/token",service="test"`)
		w.WriteHeader(http.StatusUnauthorized)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("scope") == "" {
			t.Error("missing scope")
		}
		_, _ = w.Write([]byte(`{"token":"abc"}`))
	})
	mux.HandleFunc("/v2/library/nginx/manifests/latest", func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer abc" {
			t.Error("missing token header")
		}
		w.Header().Set("Docker-Content-Digest", "sha256:digest")
		w.WriteHeader(http.StatusOK)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	serverURL = server.URL

	client := New("")
	ref := client.ParseImageRef("nginx")
	ref.Registry = server.URL
	if digest := client.GetManifestDigest(context.Background(), ref); digest != "sha256:digest" {
		t.Fatalf("unexpected digest %q", digest)
	}
}
