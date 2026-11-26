package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"dockdash/internal/config"
	"dockdash/internal/db"
	"dockdash/internal/session"
)

func authTestEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		"AUTH_USERNAME", "AUTH_PASSWORD", "AUTH_PASSWORD_HASH",
		"OIDC_ISSUER", "OIDC_CLIENT_ID", "OIDC_CLIENT_SECRET", "OIDC_REDIRECT_URI", "OIDC_SCOPES",
		"SESSION_SECRET", "DB_PATH", "MIGRATIONS_DIR",
	}
	for _, key := range keys {
		name := key
		old, existed := os.LookupEnv(name)
		t.Cleanup(func() {
			if existed {
				_ = os.Setenv(name, old)
			} else {
				_ = os.Unsetenv(name)
			}
		})
		_ = os.Unsetenv(name)
	}
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	store, err := session.New(database, cfg.SessionSecret, cfg.SessionMaxAge, false)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(cfg, database, store).Handler())
	t.Cleanup(server.Close)
	return server
}

func postJSON(t *testing.T, server *httptest.Server, path, body string) (*http.Response, []byte) {
	t.Helper()
	response, err := http.Post(server.URL+path, "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	bodyBytes, _ := io.ReadAll(response.Body)
	return response, bodyBytes
}

func TestPasswordLoginRoundTrip(t *testing.T) {
	authTestEnv(t)
	t.Setenv("AUTH_USERNAME", "admin")
	t.Setenv("AUTH_PASSWORD", "secret")
	server := newTestServer(t)

	response, body := postJSON(t, server, "/auth/password/login", `{"username":"admin","password":"secret"}`)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login failed: %d %s", response.StatusCode, body)
	}
	if !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("unexpected login body: %s", body)
	}
	cookies := response.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected session cookie, got %d", len(cookies))
	}

	request, err := http.NewRequest("GET", server.URL+"/auth/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(cookies[0])
	me, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer me.Body.Close()
	meBody, _ := io.ReadAll(me.Body)
	if me.StatusCode != http.StatusOK || !strings.Contains(string(meBody), `"sub":"local"`) {
		t.Fatalf("unexpected /auth/me: %d %s", me.StatusCode, meBody)
	}

	logoutRequest, err := http.NewRequest("POST", server.URL+"/auth/logout", bytes.NewBufferString("{}"))
	if err != nil {
		t.Fatal(err)
	}
	logoutRequest.AddCookie(cookies[0])
	logout, err := http.DefaultClient.Do(logoutRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer logout.Body.Close()
	if logout.StatusCode != http.StatusOK {
		t.Fatalf("logout failed: %d", logout.StatusCode)
	}

	request, err = http.NewRequest("GET", server.URL+"/auth/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(cookies[0])
	after, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer after.Body.Close()
	afterBody, _ := io.ReadAll(after.Body)
	if !strings.Contains(string(afterBody), `"user":null`) {
		t.Fatalf("session not destroyed: %s", afterBody)
	}
}

func TestPasswordLoginRejectsInvalidInput(t *testing.T) {
	authTestEnv(t)
	t.Setenv("AUTH_USERNAME", "admin")
	t.Setenv("AUTH_PASSWORD", "secret")
	server := newTestServer(t)

	response, body := postJSON(t, server, "/auth/password/login", `{"username":"admin","password":"wrong"}`)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d %s", response.StatusCode, body)
	}
	response, body = postJSON(t, server, "/auth/password/login", `{"username":"admin"}`)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d %s", response.StatusCode, body)
	}
	response, body = postJSON(t, server, "/auth/password/login", `{"username":"admin","password":"secret","extra":1}`)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown field, got %d %s", response.StatusCode, body)
	}
}

func TestPasswordLoginDisabled(t *testing.T) {
	authTestEnv(t)
	server := newTestServer(t)
	response, body := postJSON(t, server, "/auth/password/login", `{"username":"admin","password":"secret"}`)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d %s", response.StatusCode, body)
	}
}

func TestPasswordLoginSameOriginGuard(t *testing.T) {
	authTestEnv(t)
	t.Setenv("AUTH_USERNAME", "admin")
	t.Setenv("AUTH_PASSWORD", "secret")
	server := newTestServer(t)
	request, err := http.NewRequest("POST", server.URL+"/auth/password/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "Cross-site request blocked") {
		t.Fatalf("expected CSRF rejection, got %d %s", response.StatusCode, body)
	}
}

func TestPasswordLoginRateLimit(t *testing.T) {
	authTestEnv(t)
	server := newTestServer(t)
	for i := 0; i < 6; i++ {
		response, body := postJSON(t, server, "/auth/password/login", `{"username":"admin","password":"secret"}`)
		if i < 5 && response.StatusCode != http.StatusForbidden {
			t.Fatalf("request %d: expected 403, got %d %s", i, response.StatusCode, body)
		}
		if i == 5 && response.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("sixth request: expected 429, got %d %s", response.StatusCode, body)
		}
	}
}

func TestConfigEndpoint(t *testing.T) {
	authTestEnv(t)
	server := newTestServer(t)
	response, err := http.Get(server.URL + "/api/config")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload["port"] != nil {
		t.Fatal("internal-only settings must not be exposed")
	}
	for _, key := range []string{"version", "dockerHosts", "networkCidrs", "healthCheckInterval", "terminalEnabled"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("missing config key %s", key)
		}
	}
	if hosts, ok := payload["dockerHosts"].([]any); !ok || hosts == nil {
		t.Fatalf("dockerHosts must be an array, got %T", payload["dockerHosts"])
	}
}
