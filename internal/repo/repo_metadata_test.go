package repo_test

import (
	"database/sql"
	"errors"
	"testing"

	"dockdash/internal/db"
	"dockdash/internal/repo"
)

func newRepo(t *testing.T) *repo.Repo {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(database, "../../drizzle"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return repo.New(database)
}

func saveService(t *testing.T, database *repo.Repo) repo.Service {
	t.Helper()
	now := repo.Now()
	service := repo.Service{
		ID: "svc-1", Name: "nginx", Host: "localhost", Source: "docker", Status: "up",
		Metadata: map[string]any{
			"image": "nginx", "imageTag": "1.25", "imageDigest": "sha256:abc", "hasUpdate": true,
		},
		Labels: []string{}, Ports: []int{}, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.SaveService(service, nil); err != nil {
		t.Fatal(err)
	}
	return service
}

func TestUpdateServiceMetadataMerges(t *testing.T) {
	database := newRepo(t)
	saveService(t, database)
	if err := database.UpdateServiceMetadata("svc-1", map[string]any{"hasUpdate": false, "latestVersion": "1.26"}); err != nil {
		t.Fatal(err)
	}
	service, err := database.GetService("svc-1")
	if err != nil {
		t.Fatal(err)
	}
	if service.Metadata["hasUpdate"] != false || service.Metadata["latestVersion"] != "1.26" {
		t.Fatalf("unexpected metadata: %#v", service.Metadata)
	}
	if service.Metadata["image"] != "nginx" || service.Metadata["imageDigest"] != "sha256:abc" {
		t.Fatalf("metadata lost: %#v", service.Metadata)
	}
}

func TestUpdateServiceMetadataIfUnchanged(t *testing.T) {
	database := newRepo(t)
	saveService(t, database)
	updated, err := database.UpdateServiceMetadataIfUnchanged("svc-1", "nginx", "1.25", "sha256:abc",
		map[string]any{"hasUpdate": false, "updateCheckedAt": "now"})
	if err != nil || !updated {
		t.Fatalf("update: updated=%v err=%v", updated, err)
	}
	updated, err = database.UpdateServiceMetadataIfUnchanged("svc-1", "nginx", "1.25", "sha256:old",
		map[string]any{"hasUpdate": true})
	if err != nil || updated {
		t.Fatalf("stale update: updated=%v err=%v", updated, err)
	}
	service, err := database.GetService("svc-1")
	if err != nil {
		t.Fatal(err)
	}
	if service.Metadata["hasUpdate"] != false {
		t.Fatalf("stale check overwrote metadata: %#v", service.Metadata)
	}
}

func TestUpdateServiceMetadataMissing(t *testing.T) {
	database := newRepo(t)
	if err := database.UpdateServiceMetadata("missing", map[string]any{"hasUpdate": true}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected no rows error, got %v", err)
	}
}
