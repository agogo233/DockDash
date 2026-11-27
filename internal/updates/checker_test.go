package updates

import (
	"context"
	"strings"
	"testing"

	"dockdash/internal/db"
	"dockdash/internal/notify"
	"dockdash/internal/registry"
	"dockdash/internal/repo"
)

type fakeRegistry struct {
	tags       []string
	digest     string
	onTags     func()
	onDigest   func()
	seenPrefix string
}

func (f *fakeRegistry) ParseImageRef(image string) registry.ImageRef {
	return registry.New("").ParseImageRef(image)
}

func (f *fakeRegistry) GetRepositoryTags(_ context.Context, _ registry.ImageRef, prefix string) []string {
	f.seenPrefix = prefix
	if f.onTags != nil {
		f.onTags()
	}
	return f.tags
}

func (f *fakeRegistry) GetManifestDigest(_ context.Context, _ registry.ImageRef) string {
	if f.onDigest != nil {
		f.onDigest()
	}
	return f.digest
}

type fakeNotifier struct {
	configured bool
	titles     []string
	bodies     []string
}

func (f *fakeNotifier) Configured() bool { return f.configured }

func (f *fakeNotifier) Notify(_ context.Context, title, body string, _ notify.NotificationType) error {
	f.titles = append(f.titles, title)
	f.bodies = append(f.bodies, body)
	return nil
}

type fakeResolver struct{ url string }

func (f *fakeResolver) ReleaseURL(_ context.Context, _, _ string) string { return f.url }

func newCheckerFixture(t *testing.T) (*repo.Repo, *fakeRegistry, *fakeNotifier) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(database, "../../drizzle"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	databaseRepo := repo.New(database)
	return databaseRepo, &fakeRegistry{}, &fakeNotifier{configured: true}
}

func saveContainer(t *testing.T, databaseRepo *repo.Repo, metadata map[string]any) {
	t.Helper()
	now := repo.Now()
	service := repo.Service{
		ID: "svc-1", Name: "nginx", Host: "localhost", Source: "docker", Status: "up",
		Metadata: metadata, Labels: []string{}, Ports: []int{}, CreatedAt: now, UpdatedAt: now,
	}
	if err := databaseRepo.SaveService(service, nil); err != nil {
		t.Fatal(err)
	}
}

func TestCheckAllSemverUpdate(t *testing.T) {
	databaseRepo, fake, notifier := newCheckerFixture(t)
	saveContainer(t, databaseRepo, map[string]any{
		"image": "nginx", "imageTag": "1.2.3", "imageDigest": "sha256:old",
	})
	fake.tags = []string{"v1.2.3", "1.2.4", "cuda-2.0.0"}
	checker := NewChecker(databaseRepo, fake, notifier, &fakeResolver{url: "https://example.test/release"}, "en")
	if err := checker.CheckAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	service, err := databaseRepo.GetService("svc-1")
	if err != nil {
		t.Fatal(err)
	}
	if service.Metadata["hasUpdate"] != true || service.Metadata["latestVersion"] != "1.2.4" {
		t.Fatalf("unexpected metadata: %#v", service.Metadata)
	}
	if service.Metadata["updateCheckedAt"] == nil {
		t.Fatal("missing updateCheckedAt")
	}
	if len(notifier.titles) != 1 || !strings.Contains(notifier.bodies[0], "nginx: 1.2.3 → 1.2.4") {
		t.Fatalf("unexpected notification: %#v %#v", notifier.titles, notifier.bodies)
	}
	if !strings.Contains(notifier.bodies[0], "https://example.test/release") {
		t.Fatalf("missing release url: %s", notifier.bodies[0])
	}
}

func TestCheckAllDigestUpdate(t *testing.T) {
	databaseRepo, fake, notifier := newCheckerFixture(t)
	saveContainer(t, databaseRepo, map[string]any{
		"image": "nginx", "imageTag": "latest", "imageDigest": "sha256:abcdef1234567890",
	})
	fake.digest = "sha256:1234567890abcdef"
	checker := NewChecker(databaseRepo, fake, notifier, nil, "en")
	if err := checker.CheckAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	service, err := databaseRepo.GetService("svc-1")
	if err != nil {
		t.Fatal(err)
	}
	if service.Metadata["hasUpdate"] != true {
		t.Fatalf("unexpected metadata: %#v", service.Metadata)
	}
	if len(notifier.bodies) != 1 || !strings.Contains(notifier.bodies[0], "abcdef123456…") {
		t.Fatalf("unexpected notification: %#v", notifier.bodies)
	}
}

func TestCheckAllNoRepeatNotification(t *testing.T) {
	databaseRepo, fake, notifier := newCheckerFixture(t)
	saveContainer(t, databaseRepo, map[string]any{
		"image": "nginx", "imageTag": "1.2.3", "imageDigest": "sha256:old", "hasUpdate": true,
		"latestVersion": "1.2.4",
	})
	fake.tags = []string{"1.2.3", "1.2.4"}
	checker := NewChecker(databaseRepo, fake, notifier, nil, "en")
	if err := checker.CheckAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(notifier.titles) != 0 {
		t.Fatalf("unexpected notification: %#v", notifier.titles)
	}
}

func TestCheckAllSkipsStaleSnapshot(t *testing.T) {
	databaseRepo, fake, notifier := newCheckerFixture(t)
	saveContainer(t, databaseRepo, map[string]any{
		"image": "nginx", "imageTag": "latest", "imageDigest": "sha256:old",
	})
	fake.digest = "sha256:new"
	fake.onDigest = func() {
		if err := databaseRepo.UpdateServiceMetadata("svc-1", map[string]any{"imageDigest": "sha256:changed"}); err != nil {
			t.Errorf("update metadata: %v", err)
		}
	}
	checker := NewChecker(databaseRepo, fake, notifier, nil, "en")
	if err := checker.CheckAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(notifier.titles) != 0 {
		t.Fatalf("stale check notified: %#v", notifier.titles)
	}
	service, err := databaseRepo.GetService("svc-1")
	if err != nil {
		t.Fatal(err)
	}
	if service.Metadata["hasUpdate"] != nil {
		t.Fatalf("stale check overwrote metadata: %#v", service.Metadata)
	}
}
