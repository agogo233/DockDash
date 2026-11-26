package session

import (
	"database/sql"
	"net/http/httptest"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestCookieRoundTrip(t *testing.T) {
	database, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	defer database.Close()
	store, err := New(database, "test-secret", 3600000, false)
	if err != nil {
		t.Fatal(err)
	}
	sess := store.newSession()
	sess.Values["authenticated"] = true
	recorder := httptest.NewRecorder()
	if err := store.Save(recorder, sess); err != nil {
		t.Fatal(err)
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one cookie, got %d", len(cookies))
	}
	request := httptest.NewRequest("GET", "/", nil)
	request.AddCookie(cookies[0])
	loaded := store.Get(request)
	if loaded == nil {
		t.Fatal("session cookie was not read back")
	}
	if loaded.ID != sess.ID || loaded.Values["authenticated"] != true {
		t.Fatalf("session mismatch: %+v", loaded)
	}
}
