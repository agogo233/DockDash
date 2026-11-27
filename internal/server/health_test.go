package server

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestHistoryEndpoints(t *testing.T) {
	fixture := newUpdatesFixture(t, "")
	fixture.request(t, "GET", "/api/services/missing/health-history", "", http.StatusNotFound)
	fixture.request(t, "GET", "/api/services/missing/resource-history", "", http.StatusNotFound)
	created := fixture.request(t, "POST", "/api/services",
		`{"name":"Web","host":"127.0.0.1"}`, http.StatusCreated)
	id, ok := created["id"].(string)
	if !ok || id == "" {
		t.Fatalf("missing service id: %#v", created)
	}
	health := requestAnyList(t, fixture, "/api/services/"+id+"/health-history?days=1&buckets=4")
	if len(health) != 4 {
		t.Fatalf("unexpected health history: %#v", health)
	}
	resources := requestAnyList(t, fixture, "/api/services/"+id+"/resource-history?days=1&buckets=4")
	if len(resources) != 4 {
		t.Fatalf("unexpected resource history: %#v", resources)
	}
}

func requestAnyList(t *testing.T, fixture *crudFixture, path string) []any {
	t.Helper()
	response, err := http.Get(fixture.server.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: expected 200, got %d", path, response.StatusCode)
	}
	content, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var payload []any
	if err := json.Unmarshal(content, &payload); err != nil {
		t.Fatalf("invalid JSON from %s: %s", path, content)
	}
	return payload
}
