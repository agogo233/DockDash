package server

import "testing"

func TestDiscoveryRoutes(t *testing.T) {
	fixture := newCRUDFixture(t)
	health := fixture.requestList(t, "GET", "/api/kubernetes/health", "", 200)
	if len(health) != 0 {
		t.Fatalf("expected no Kubernetes contexts, got %#v", health)
	}
	fixture.request(t, "GET", "/api/network/scan/stream?cidrs=not-a-cidr", "", 400)
}
