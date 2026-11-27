package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"dockdash/internal/config"
	"dockdash/internal/db"
	"dockdash/internal/notify"
	"dockdash/internal/repo"
)

type fakeScanner struct {
	services []repo.Service
	err      error
}

func (f *fakeScanner) Scan(context.Context) ([]repo.Service, error) {
	return f.services, f.err
}

func newHealthFixture(t *testing.T, appriseURL string) (*repo.Repo, *notify.Client) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(database, "../../drizzle"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return repo.New(database), notify.New(&config.Config{AppriseURL: appriseURL, Locale: "en"})
}

func saveService(t *testing.T, database *repo.Repo, service repo.Service) {
	t.Helper()
	if service.ID == "" {
		service.ID = repo.NewID()
	}
	if service.CreatedAt == "" {
		service.CreatedAt = repo.Now()
		service.UpdatedAt = service.CreatedAt
	}
	if service.Metadata == nil {
		service.Metadata = map[string]any{}
	}
	if service.Labels == nil {
		service.Labels = []string{}
	}
	if service.Ports == nil {
		service.Ports = []int{}
	}
	if err := database.SaveService(service, nil); err != nil {
		t.Fatal(err)
	}
}

func TestCheckAllHTTPService(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil {
		t.Fatal(err)
	}
	database, notifier := newHealthFixture(t, "")
	saveService(t, database, repo.Service{
		Name: "web", Host: parsed.Hostname(), Source: "network", Status: "down",
		CheckPort: &port, Protocol: ptrString("http"),
	})
	checker := NewChecker(database, nil, notifier, "en", true)
	if err := checker.CheckAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	services, err := database.GetServices()
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 || services[0].Status != StatusUp {
		t.Fatalf("unexpected status: %#v", services)
	}
	history := database.HealthHistory(services[0].ID, 1, 10)
	if history[9] != StatusUp {
		t.Fatalf("missing history: %#v", history)
	}
}

func TestCheckAllDockerDownNotifies(t *testing.T) {
	var notified string
	apprise := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		buffer := make([]byte, 1024)
		read, _ := request.Body.Read(buffer)
		notified = string(buffer[:read])
		writer.WriteHeader(http.StatusOK)
	}))
	defer apprise.Close()
	database, notifier := newHealthFixture(t, apprise.URL)
	now := repo.Now()
	saveService(t, database, repo.Service{
		Name: "app", Host: "localhost", Source: "docker", Status: "up",
		Metadata: map[string]any{"dockerHostId": "host-1", "containerName": "app", "imageTag": "1.0"},
	})
	other := repo.Service{
		ID: "docker-other", Name: "other", Host: "localhost", Source: "docker", Status: "up",
		Metadata: map[string]any{"dockerHostId": "host-1", "containerName": "other", "imageTag": "1.0"},
		Labels:   []string{}, Ports: []int{}, CreatedAt: now, UpdatedAt: now,
	}
	checker := NewChecker(database, &fakeScanner{services: []repo.Service{other}}, notifier, "en", true)
	if err := checker.CheckAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	services, err := database.GetServices()
	if err != nil {
		t.Fatal(err)
	}
	if services[0].Status != StatusDown {
		t.Fatalf("unexpected status: %s", services[0].Status)
	}
	if !strings.Contains(notified, "Service Down") {
		t.Fatalf("unexpected notification: %s", notified)
	}
}

func ptrString(value string) *string { return &value }
