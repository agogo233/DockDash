package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"dockdash/internal/config"
	"dockdash/internal/db"
	"dockdash/internal/session"
)

type crudFixture struct {
	server *httptest.Server
}

func newCRUDFixture(t *testing.T) *crudFixture {
	t.Helper()
	authTestEnv(t)
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

func (f *crudFixture) request(t *testing.T, method, path, body string, expected int) map[string]any {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	request, err := http.NewRequest(method, f.server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	content, _ := io.ReadAll(response.Body)
	if response.StatusCode != expected {
		t.Fatalf("%s %s: expected %d, got %d: %s", method, path, expected, response.StatusCode, content)
	}
	if len(content) == 0 {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal(content, &payload); err != nil {
		t.Fatalf("invalid JSON from %s: %s", path, content)
	}
	return payload
}

func (f *crudFixture) requestList(t *testing.T, method, path, body string, expected int) []map[string]any {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	request, err := http.NewRequest(method, f.server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	content, _ := io.ReadAll(response.Body)
	if response.StatusCode != expected {
		t.Fatalf("%s %s: expected %d, got %d: %s", method, path, expected, response.StatusCode, content)
	}
	var payload []map[string]any
	if err := json.Unmarshal(content, &payload); err != nil {
		t.Fatalf("invalid JSON from %s: %s", path, content)
	}
	return payload
}

func (f *crudFixture) requestStrings(t *testing.T, path string, expected int) []string {
	t.Helper()
	response, err := http.Get(f.server.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	content, _ := io.ReadAll(response.Body)
	if response.StatusCode != expected {
		t.Fatalf("GET %s: expected %d, got %d: %s", path, expected, response.StatusCode, content)
	}
	var payload []string
	if err := json.Unmarshal(content, &payload); err != nil {
		t.Fatalf("invalid JSON from %s: %s", path, content)
	}
	return payload
}

func TestCRUDServiceAndDashboardFlow(t *testing.T) {
	fixture := newCRUDFixture(t)
	created := fixture.request(t, "POST", "/api/services",
		`{"name":"Web","host":"127.0.0.1","ports":[80],"checkPort":80,"source":"network","labels":["Prod"]}`, 201)
	id, ok := created["id"].(string)
	if !ok || id == "" {
		t.Fatalf("missing service id: %v", created)
	}
	if created["status"] != "unknown" || created["source"] != "network" {
		t.Fatalf("unexpected service: %v", created)
	}

	fixture.request(t, "PUT", "/api/services/"+id, `{"name":"Web 2"}`, 200)
	fixture.request(t, "POST", "/api/services/labels", `{"serviceIds":["`+id+`"],"labels":["Important"]}`, 200)
	labels := fixture.requestStrings(t, "/api/labels", 200)
	if len(labels) != 2 {
		t.Fatalf("expected two labels, got %v", labels)
	}

	fixture.request(t, "POST", "/api/services/"+id+"/dashboard", "", 200)
	positions := fixture.request(t, "POST", "/api/positions",
		`{"positions":[{"serviceId":"`+id+`","x":10,"y":20}]}`, 200)
	if _, ok := positions["positions"]; !ok {
		t.Fatalf("missing positions: %v", positions)
	}
	dashboard := fixture.request(t, "GET", "/api/dashboard", "", 200)
	services, _ := dashboard["services"].([]any)
	if len(services) != 1 {
		t.Fatalf("expected one dashboard service, got %v", services)
	}

	second := fixture.request(t, "POST", "/api/services", `{"name":"DB","host":"127.0.0.2"}`, 201)
	secondID, _ := second["id"].(string)
	link := fixture.request(t, "POST", "/api/links",
		`{"sourceId":"`+id+`","targetId":"`+secondID+`","type":"dependency","label":"uses"}`, 201)
	linkID, _ := link["id"].(string)
	fixture.request(t, "PUT", "/api/links/"+linkID, `{"label":"depends on"}`, 200)
	fixture.request(t, "DELETE", "/api/links/"+linkID, "", 200)
	fixture.request(t, "DELETE", "/api/services/"+id, "", 200)
	remaining := fixture.requestList(t, "GET", "/api/services", "", 200)
	if len(remaining) != 1 {
		t.Fatalf("expected one remaining service, got %v", remaining)
	}
}

func TestCRUDValidation(t *testing.T) {
	fixture := newCRUDFixture(t)
	fixture.request(t, "POST", "/api/services", `{"name":"","host":"x"}`, 400)
	fixture.request(t, "POST", "/api/services", `{"name":"x","host":"y","ports":[0]}`, 400)
	fixture.request(t, "POST", "/api/services", `{"name":"x","host":"y","source":"bogus"}`, 400)
	fixture.request(t, "POST", "/api/services", `{"name":"x","host":"y","unknown":1}`, 400)
	fixture.request(t, "POST", "/api/links", `{"sourceId":"a","targetId":"a"}`, 400)
	fixture.request(t, "GET", "/api/services/missing", "", 404)
}
