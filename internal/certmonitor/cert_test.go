package certmonitor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"dockdash/internal/db"
	"dockdash/internal/repo"
)

func saveHTTPSService(t *testing.T, database *repo.Repo, host string, port int) repo.Service {
	t.Helper()
	protocol := "https"
	now := repo.Now()
	service := repo.Service{
		ID: "svc-1", Name: "secure", Host: host, Protocol: &protocol, CheckPort: &port,
		Source: "network", Status: "up", Metadata: map[string]any{}, Labels: []string{},
		Ports: []int{}, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.SaveService(service, nil); err != nil {
		t.Fatal(err)
	}
	return service
}

func TestResolveEndpoint(t *testing.T) {
	protocol := "https"
	other := "http"
	port := 8443
	cases := []struct {
		service repo.Service
		host    string
		port    int
		ok      bool
	}{
		{repo.Service{Host: "example.com", Protocol: &protocol, CheckPort: &port}, "example.com", 8443, true},
		{repo.Service{Host: "https://example.com:9443", Protocol: &protocol}, "example.com", 9443, true},
		{repo.Service{Host: "example.com", Protocol: &other}, "", 0, false},
	}
	for _, item := range cases {
		host, port, ok := ResolveEndpoint(item.service)
		if host != item.host || port != item.port || ok != item.ok {
			t.Errorf("unexpected endpoint: %s:%d ok=%v", host, port, ok)
		}
	}
}

func TestGetForServiceCaches(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.Migrate(database, "../../drizzle"); err != nil {
		t.Fatal(err)
	}
	databaseRepo := repo.New(database)
	saveHTTPSService(t, databaseRepo, parsed.Hostname(), port)
	service := New(databaseRepo)
	first, ok := service.GetForService("svc-1", false)
	if !ok || first.FingerprintSHA256 == nil || first.DaysRemaining == nil {
		t.Fatalf("unexpected certificate: %#v", first)
	}
	server.Close()
	second, ok := service.GetForService("svc-1", false)
	if !ok || second.FingerprintSHA256 == nil || *second.FingerprintSHA256 != *first.FingerprintSHA256 {
		t.Fatalf("cache miss: %#v", second)
	}
	if len(service.GetAll(context.Background(), false)) != 1 {
		t.Fatal("expected one cached certificate")
	}
}
