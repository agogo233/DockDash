package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"dockdash/internal/config"
	"dockdash/internal/db"
	"dockdash/internal/session"
)

func newUpdatesFixture(t *testing.T, appriseURL string) *crudFixture {
	t.Helper()
	authTestEnv(t)
	t.Setenv("APPRISE_URL", appriseURL)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(database, "../../drizzle"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	store, err := session.New(database, cfg.SessionSecret, cfg.SessionMaxAge, false)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(cfg, database, store).Handler())
	t.Cleanup(server.Close)
	return &crudFixture{server: server}
}

func TestNotificationTestRequiresConfig(t *testing.T) {
	fixture := newUpdatesFixture(t, "")
	fixture.request(t, "POST", "/api/notifications/test", "", http.StatusBadRequest)
}

func TestNotificationTestSuccess(t *testing.T) {
	calls := 0
	apprise := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls++
		writer.WriteHeader(http.StatusOK)
	}))
	defer apprise.Close()
	fixture := newUpdatesFixture(t, apprise.URL)
	result := fixture.request(t, "POST", "/api/notifications/test", "", http.StatusOK)
	if result["success"] != true || calls != 1 {
		t.Fatalf("unexpected result: %#v, calls=%d", result, calls)
	}
}

func TestAppUpdateDevVersion(t *testing.T) {
	fixture := newUpdatesFixture(t, "")
	result := fixture.request(t, "GET", "/api/app-update", "", http.StatusOK)
	if result["hasUpdate"] != false {
		t.Fatalf("unexpected app update: %#v", result)
	}
}

func TestChangelogUnavailable(t *testing.T) {
	fixture := newUpdatesFixture(t, "")
	fixture.request(t, "GET", "/api/services/missing/changelog", "", http.StatusNotFound)
	created := fixture.request(t, "POST", "/api/services",
		`{"name":"Web","host":"127.0.0.1"}`, http.StatusCreated)
	id, ok := created["id"].(string)
	if !ok || id == "" {
		t.Fatalf("missing service id: %#v", created)
	}
	result := fixture.request(t, "GET", "/api/services/"+id+"/changelog", "", http.StatusOK)
	if result["available"] != false {
		t.Fatalf("unexpected changelog: %#v", result)
	}
}
