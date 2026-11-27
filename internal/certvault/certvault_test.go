package certvault

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"dockdash/internal/certtypes"
	"dockdash/internal/repo"
)

func TestStatusesMatchDomains(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("missing auth header")
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`[
			{"domains":["*.example.com"],"current_version":{"fingerprint_sha256":"AABB"}}
		]`))
	}))
	defer server.Close()
	client := New(server.URL, "secret")
	client.http = server.Client()
	protocol := "https"
	services := map[string]repo.Service{
		"svc-1": {ID: "svc-1", Host: "app.example.com", Protocol: &protocol},
		"svc-2": {ID: "svc-2", Host: "other.test", Protocol: &protocol},
	}
	fingerprint := "aa:bb"
	certificates := []certtypes.Certificate{
		{ServiceID: "svc-1", FingerprintSHA256: &fingerprint},
		{ServiceID: "svc-2", FingerprintSHA256: &fingerprint},
	}
	statuses, available := client.Statuses(context.Background(), certificates, services)
	if !available || statuses["svc-1"] != "in-use" || statuses["svc-2"] != "" {
		t.Fatalf("unexpected statuses: %#v", statuses)
	}
}

func TestNotConfigured(t *testing.T) {
	client := New("", "")
	if client.Configured() {
		t.Fatal("expected not configured")
	}
	if got, available := client.Statuses(context.Background(), nil, nil); available || len(got) != 0 {
		t.Fatalf("unexpected statuses: %#v", got)
	}
}
