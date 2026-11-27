package server

import (
	"net/http"
	"testing"
)

func TestCertificateEndpoints(t *testing.T) {
	fixture := newUpdatesFixture(t, "")
	created := fixture.request(t, "POST", "/api/services",
		`{"name":"Secure","host":"127.0.0.1","protocol":"https","checkPort":1}`, http.StatusCreated)
	id, ok := created["id"].(string)
	if !ok || id == "" {
		t.Fatalf("missing service id: %#v", created)
	}
	all := fixture.requestList(t, "GET", "/api/tls-certificates", "", http.StatusOK)
	if len(all) != 1 {
		t.Fatalf("unexpected certificates: %#v", all)
	}
	one := fixture.request(t, "GET", "/api/services/"+id+"/tls-certificate", "", http.StatusOK)
	if one["serviceId"] != id {
		t.Fatalf("unexpected certificate: %#v", one)
	}
	plain := fixture.request(t, "POST", "/api/services",
		`{"name":"Plain","host":"127.0.0.1"}`, http.StatusCreated)
	plainID, _ := plain["id"].(string)
	fixture.request(t, "GET", "/api/services/"+plainID+"/tls-certificate", "", http.StatusNotFound)
}
