package discovery

import (
	"context"
	"strings"
	"testing"

	"dockdash/internal/repo"
)

func TestValidateCIDR(t *testing.T) {
	for _, value := range []string{"10.0.0.0/24", "10.0.0.1/24", "192.168.1.0/16"} {
		if err := ValidateCIDR(value); err != nil {
			t.Fatalf("%s should be valid: %v", value, err)
		}
	}
	for _, value := range []string{"10.0.0.0/33", "10.0.0.01/24", "2001:db8::/32", "nope"} {
		if err := ValidateCIDR(value); err == nil {
			t.Fatalf("%s should be invalid", value)
		}
	}
}

func TestParsePingSweepHosts(t *testing.T) {
	output := "Host: 10.0.0.1 (router.local)\tStatus: Up\nHost: 10.0.0.2 ()\tStatus: Up\nHost: 10.0.0.3 ()\tStatus: Down\n"
	hosts := parsePingSweepHosts(output)
	if len(hosts) != 2 || hosts[0].ip != "10.0.0.1" || hosts[0].hostname != "router.local" || hosts[1].hostname != "" {
		t.Fatalf("unexpected hosts: %#v", hosts)
	}
}

func TestParseNmapOpenPorts(t *testing.T) {
	output := "Host: 10.0.0.1 ()\tPorts: 22/open/tcp//ssh///, 80/open/tcp//http///, 443/closed/tcp//https///\n"
	ports := parseNmapOpenPorts(output)
	if len(ports) != 2 || ports[0] != 22 || ports[1] != 80 {
		t.Fatalf("unexpected ports: %#v", ports)
	}
}

func TestScanEmitsNetworkService(t *testing.T) {
	scanner := newScannerWithRunner(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "-sn" {
			return []byte("Host: 10.0.0.5 (router.local)\tStatus: Up\n"), nil
		}
		return []byte("Host: 10.0.0.5 ()\tPorts: 443/open/tcp//https///, 80/open/tcp//http///\n"), nil
	})
	services := []repo.Service{}
	if err := scanner.Scan(context.Background(), []string{"10.0.0.0/24"}, false, func(service repo.Service) {
		services = append(services, service)
	}); err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 {
		t.Fatalf("expected one service, got %d", len(services))
	}
	service := services[0]
	if service.Source != "network" || service.Name != "router.local" || service.Host != "10.0.0.5" {
		t.Fatalf("unexpected service: %#v", service)
	}
	if service.Protocol == nil || *service.Protocol != "http" || service.CheckPort == nil || *service.CheckPort != 80 {
		t.Fatalf("unexpected endpoint: %#v", service)
	}
	if len(service.Ports) != 2 || service.Ports[0] != 80 || service.Ports[1] != 443 {
		t.Fatalf("unexpected ports: %#v", service.Ports)
	}
	if !strings.HasPrefix(service.ID, "net-") {
		t.Fatalf("unexpected id: %s", service.ID)
	}
}
