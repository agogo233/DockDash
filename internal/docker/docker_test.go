package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dockdash/internal/config"
)

func mockDockerServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/info", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"Containers":2,"ContainersRunning":1,"ContainersPaused":0,"ContainersStopped":1,"ServerVersion":"27.0"}`))
	})
	mux.HandleFunc("/containers/json", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"Id":"c1","Names":["/web"],"Image":"nginx:1.25","ImageID":"img1","State":"running","Ports":[{"IP":"0.0.0.0","PrivatePort":80,"PublicPort":8080}]}]`))
	})
	mux.HandleFunc("/containers/c1/json", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"Image":"img1","Config":{"Tty":false},"NetworkSettings":{"Networks":{"bridge":{}}}}`))
	})
	mux.HandleFunc("/images/img1/json", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"RepoDigests":["nginx@sha256:abc"]}`))
	})
	mux.HandleFunc("/containers/c1/stats", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"cpu_stats":{"cpu_usage":{"total_usage":200},"system_cpu_usage":1000},"precpu_stats":{"cpu_usage":{"total_usage":100},"system_cpu_usage":0},"memory_stats":{"usage":1000,"limit":2000,"stats":{"inactive_file":200}},"networks":{"eth0":{"rx_bytes":10,"tx_bytes":20}},"blkio_stats":{"io_service_bytes_recursive":[{"op":"read","value":5},{"op":"write","value":6}]}}`))
	})
	mux.HandleFunc("/containers/c1/start", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func TestHostHealthAndScan(t *testing.T) {
	server := mockDockerServer(t)
	manager := NewManager([]config.DockerHost{{Name: "local", Host: server.URL}})

	health := manager.HostHealth(context.Background())
	if len(health) != 1 || !health[0].Connected {
		t.Fatalf("unexpected health: %+v", health)
	}
	if health[0].Containers == nil || *health[0].Containers != 2 || health[0].ServerVersion != "27.0" {
		t.Fatalf("unexpected health details: %+v", health[0])
	}

	services, err := manager.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 {
		t.Fatalf("expected one service, got %d", len(services))
	}
	service := services[0]
	if service.Name != "web" || service.Source != "docker" || service.Status != "up" {
		t.Fatalf("unexpected service: %+v", service)
	}
	if len(service.Ports) != 1 || service.Ports[0] != 8080 {
		t.Fatalf("unexpected ports: %v", service.Ports)
	}
	if service.Metadata["imageTag"] != "1.25" || service.Metadata["imageDigest"] != "sha256:abc" {
		t.Fatalf("unexpected metadata: %v", service.Metadata)
	}
	if manager.SourceName(service) != "local" {
		t.Fatalf("unexpected source name")
	}
}

func TestStatsAndAction(t *testing.T) {
	server := mockDockerServer(t)
	manager := NewManager([]config.DockerHost{{Name: "local", Host: server.URL}})
	services, err := manager.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	stats, err := manager.Stats(context.Background(), services[0])
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(stats)
	if stats.CPUPercent != 10 || stats.MemoryPercent != 40 || stats.NetworkRx != 10 || stats.BlockWrite != 6 {
		t.Fatalf("unexpected stats: %s", data)
	}
	if err := manager.Action(context.Background(), services[0], "start"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Action(context.Background(), services[0], "bogus"); err == nil {
		t.Fatal("expected invalid action error")
	}
}
