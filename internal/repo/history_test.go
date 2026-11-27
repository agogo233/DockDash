package repo_test

import (
	"fmt"
	"testing"
	"time"

	"dockdash/internal/db"
	"dockdash/internal/repo"
)

func TestHistoryBuckets(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(database, "../../drizzle"); err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	databaseRepo := repo.New(database)
	now := repo.Now()
	service := repo.Service{
		ID: "svc-1", Name: "web", Host: "localhost", Source: "network", Status: "up",
		Metadata: map[string]any{}, Labels: []string{}, Ports: []int{}, CreatedAt: now, UpdatedAt: now,
	}
	if err := databaseRepo.SaveService(service, nil); err != nil {
		t.Fatal(err)
	}
	format := "2006-01-02T15:04:05.000Z"
	base := time.Now().UTC()
	for index, status := range []string{"up", "up", "down"} {
		checkedAt := base.Add(time.Duration(index-3) * time.Minute).Format(format)
		if _, err := database.Exec(`INSERT INTO service_health_history (id, service_id, status, checked_at) VALUES (?, ?, ?, ?)`,
			fmt.Sprintf("h-%d", index), service.ID, status, checkedAt); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`INSERT INTO service_resource_history (id, service_id, cpu_percent, memory_percent, checked_at) VALUES (?, ?, ?, ?, ?)`,
			fmt.Sprintf("r-%d", index), service.ID, float64(10+index*10), float64(20+index*10), checkedAt); err != nil {
			t.Fatal(err)
		}
	}
	history := databaseRepo.HealthHistory(service.ID, 1, 1440)
	if len(history) != 1440 {
		t.Fatalf("unexpected buckets: %d", len(history))
	}
	if history[1436] != "up" || history[1437] != "up" || history[1438] != "down" {
		t.Fatalf("unexpected health buckets: %#v", history[1436:])
	}
	resources := databaseRepo.ResourceHistory(service.ID, 1, 1440)
	if len(resources) != 1440 || resources[1436].CPUPercent != 10 || resources[1438].MemoryPercent != 40 {
		t.Fatalf("unexpected resource buckets: %#v", resources[1436:])
	}
}
