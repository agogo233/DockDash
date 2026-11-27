package monitor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dockdash/internal/config"
	"dockdash/internal/db"
	"dockdash/internal/docker"
	"dockdash/internal/notify"
	"dockdash/internal/repo"
)

type fakeStats struct {
	stats docker.ContainerStats
}

func (f *fakeStats) Stats(context.Context, repo.Service) (docker.ContainerStats, error) {
	return f.stats, nil
}

func TestCollectRecordsHistoryAndNotifiesSpike(t *testing.T) {
	var notification string
	apprise := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		buffer := make([]byte, 2048)
		read, _ := request.Body.Read(buffer)
		notification = string(buffer[:read])
		writer.WriteHeader(http.StatusOK)
	}))
	defer apprise.Close()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.Migrate(database, "../../drizzle"); err != nil {
		t.Fatal(err)
	}
	databaseRepo := repo.New(database)
	now := repo.Now()
	service := repo.Service{
		ID: "svc-1", Name: "app", Host: "localhost", Source: "docker", Status: "up",
		Metadata: map[string]any{}, Labels: []string{}, Ports: []int{}, CreatedAt: now, UpdatedAt: now,
	}
	if err := databaseRepo.SaveService(service, nil); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		AppriseURL: apprise.URL, Locale: "en", ResourceMonitorEnabled: true, HealthHistoryEnabled: true,
		CPUSpikeThreshold: 50, MemorySpikeThreshold: 90, SpikeDurationThreshold: 0,
	}
	monitor := New(databaseRepo, &fakeStats{stats: docker.ContainerStats{CPUPercent: 75, MemoryPercent: 40}},
		notify.New(cfg), cfg)
	if err := monitor.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	sample, ok := monitor.Latest(service.ID)
	if !ok || sample.CPUPercent != 75 {
		t.Fatalf("unexpected latest sample: %#v", sample)
	}
	history := databaseRepo.ResourceHistory(service.ID, 1, 10)
	if history[9] == nil || history[9].CPUPercent != 75 {
		t.Fatalf("missing history: %#v", history)
	}
	if !strings.Contains(notification, "CPU Spike") {
		t.Fatalf("unexpected notification: %s", notification)
	}
}
