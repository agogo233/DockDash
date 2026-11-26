package docker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"dockdash/internal/config"
	"dockdash/internal/repo"
)

const clientTimeout = 5 * time.Second

type Host struct {
	ID   string
	Name string
	Endpoint string
	client *hostClient
}

type hostClient struct {
	base   string
	client *http.Client
}

type ContainerStats struct {
	CPUPercent    float64 `json:"cpuPercent"`
	MemoryUsed    float64 `json:"memoryUsed"`
	MemoryLimit   float64 `json:"memoryLimit"`
	MemoryPercent float64 `json:"memoryPercent"`
	NetworkRx     float64 `json:"networkRx"`
	NetworkTx     float64 `json:"networkTx"`
	BlockRead     float64 `json:"blockRead"`
	BlockWrite    float64 `json:"blockWrite"`
}

type HostHealth struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Host              string `json:"host"`
	Connected         bool   `json:"connected"`
	Containers        *int   `json:"containers,omitempty"`
	ContainersRunning *int   `json:"containersRunning,omitempty"`
	ContainersPaused  *int   `json:"containersPaused,omitempty"`
	ContainersStopped *int   `json:"containersStopped,omitempty"`
	ServerVersion     string `json:"serverVersion,omitempty"`
	Error             string `json:"error,omitempty"`
}

type containerItem struct {
	ID      string `json:"Id"`
	Names   []string `json:"Names"`
	Image   string `json:"Image"`
	ImageID string `json:"ImageID"`
	State   string `json:"State"`
	Ports   []struct {
		IP          string `json:"IP"`
		PrivatePort int    `json:"PrivatePort"`
		PublicPort  int    `json:"PublicPort"`
	} `json:"Ports"`
}

type containerInfo struct {
	Image string `json:"Image"`
	Config struct {
		Tty bool `json:"Tty"`
	} `json:"Config"`
	NetworkSettings struct {
		Networks map[string]any `json:"Networks"`
	} `json:"NetworkSettings"`
}

type imageInfo struct {
	RepoDigests []string `json:"RepoDigests"`
}

type infoResponse struct {
	Containers        int    `json:"Containers"`
	ContainersRunning int    `json:"ContainersRunning"`
	ContainersPaused  int    `json:"ContainersPaused"`
	ContainersStopped int    `json:"ContainersStopped"`
	ServerVersion     string `json:"ServerVersion"`
}

type statsResponse struct {
	CPUStats struct {
		CPUUsage struct {
			TotalUsage float64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage float64 `json:"system_cpu_usage"`
	} `json:"cpu_stats"`
	PreCPUStats struct {
		CPUUsage struct {
			TotalUsage float64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage float64 `json:"system_cpu_usage"`
	} `json:"precpu_stats"`
	MemoryStats struct {
		Usage float64 `json:"usage"`
		Limit float64 `json:"limit"`
		Stats map[string]float64 `json:"stats"`
	} `json:"memory_stats"`
	Networks map[string]struct {
		RxBytes float64 `json:"rx_bytes"`
		TxBytes float64 `json:"tx_bytes"`
	} `json:"networks"`
	BlkioStats struct {
		IOServiceBytesRecursive []struct {
			Op    string  `json:"op"`
			Value float64 `json:"value"`
		} `json:"io_service_bytes_recursive"`
	} `json:"blkio_stats"`
}

type Manager struct {
	hosts []Host
	byID  map[string]Host
}

func newHostClient(endpoint string) *hostClient {
	if strings.HasPrefix(endpoint, "unix://") {
		socket := strings.TrimPrefix(endpoint, "unix://")
		transport := &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: clientTimeout}).DialContext(ctx, "unix", socket)
			},
		}
		return &hostClient{base: "http://docker", client: &http.Client{Transport: transport, Timeout: clientTimeout}}
	}
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		return &hostClient{base: endpoint, client: &http.Client{Timeout: clientTimeout}}
	}
	address := endpoint
	if strings.HasPrefix(address, "tcp://") {
		address = strings.TrimPrefix(address, "tcp://")
	}
	if _, _, err := net.SplitHostPort(address); err != nil {
		address += ":2375"
	}
	return &hostClient{base: "http://" + address, client: &http.Client{Timeout: clientTimeout}}
}

func hostID(endpoint string) string {
	sum := sha256.Sum256([]byte(endpoint))
	return hex.EncodeToString(sum[:])[:16]
}

func NewManager(hosts []config.DockerHost) *Manager {
	manager := &Manager{byID: map[string]Host{}}
	for _, entry := range hosts {
		host := Host{ID: hostID(entry.Host), Name: entry.Name, Endpoint: entry.Host, client: newHostClient(entry.Host)}
		manager.hosts = append(manager.hosts, host)
		manager.byID[host.ID] = host
	}
	return manager
}

func (c *hostClient) request(ctx context.Context, method, path string, body io.Reader, out any) error {
	request, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	response, err := c.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadRequest {
		content, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return fmt.Errorf("docker API %s: %s", response.Status, strings.TrimSpace(string(content)))
	}
	if out != nil {
		return json.NewDecoder(response.Body).Decode(out)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	return nil
}

func (m *Manager) Hosts() []Host {
	return append([]Host(nil), m.hosts...)
}

func (m *Manager) SourceName(service repo.Service) string {
	id, ok := service.Metadata["dockerHostId"].(string)
	if !ok {
		return ""
	}
	if host, exists := m.byID[id]; exists {
		return host.Name
	}
	return ""
}

func (m *Manager) HostHealth(ctx context.Context) []HostHealth {
	results := make([]HostHealth, 0, len(m.hosts))
	for _, host := range m.hosts {
		result := HostHealth{ID: host.ID, Name: host.Name, Host: host.Endpoint, Connected: true}
		var info infoResponse
		if err := host.client.request(ctx, http.MethodGet, "/info", nil, &info); err != nil {
			result.Connected = false
			result.Error = err.Error()
		} else {
			containers, running, paused, stopped := info.Containers, info.ContainersRunning, info.ContainersPaused, info.ContainersStopped
			result.Containers = &containers
			result.ContainersRunning = &running
			result.ContainersPaused = &paused
			result.ContainersStopped = &stopped
			result.ServerVersion = info.ServerVersion
		}
		results = append(results, result)
	}
	return results
}

func detectProtocol(port int) string {
	switch port {
	case 21:
		return "ftp"
	case 22:
		return "ssh"
	case 25, 465, 587:
		return "smtp"
	case 53, 8600:
		return "dns"
	case 80, 2375, 3000, 5000, 8080, 8500, 9200, 9300, 15672:
		return "http"
	case 443, 2376, 6443, 8443:
		return "https"
	case 1883, 8883:
		return "mqtt"
	}
	return "tcp"
}

func normalizeContainerName(name string) string {
	return strings.TrimPrefix(name, "/")
}

func parseImage(image string) (string, string) {
	withoutDigest := strings.Split(image, "@")[0]
	segments := strings.Split(withoutDigest, "/")
	last := segments[len(segments)-1]
	if index := strings.LastIndex(last, ":"); index >= 0 {
		segments[len(segments)-1] = last[:index]
		return strings.Join(segments, "/"), last[index+1:]
	}
	return withoutDigest, "latest"
}

func (m *Manager) listContainers(ctx context.Context, host Host) ([]containerItem, error) {
	var containers []containerItem
	err := host.client.request(ctx, http.MethodGet, "/containers/json?all=1", nil, &containers)
	return containers, err
}

func (m *Manager) inspectContainer(ctx context.Context, host Host, id string) (containerInfo, error) {
	var info containerInfo
	err := host.client.request(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/json", nil, &info)
	return info, err
}

func (m *Manager) imageDigest(ctx context.Context, host Host, id string) string {
	if id == "" {
		return ""
	}
	var info imageInfo
	if err := host.client.request(ctx, http.MethodGet, "/images/"+url.PathEscape(id)+"/json", nil, &info); err != nil {
		return ""
	}
	if len(info.RepoDigests) == 0 {
		return ""
	}
	parts := strings.SplitN(info.RepoDigests[0], "@", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return ""
}

func (m *Manager) Scan(ctx context.Context) ([]repo.Service, error) {
	services := []repo.Service{}
	for _, host := range m.hosts {
		containers, err := m.listContainers(ctx, host)
		if err != nil {
			return services, err
		}
		now := repo.Now()
		for _, container := range containers {
			if container.ID == "" || len(container.Names) == 0 {
				continue
			}
			name := normalizeContainerName(container.Names[0])
			info, err := m.inspectContainer(ctx, host, container.ID)
			if err != nil {
				return services, err
			}
			seen := map[int]bool{}
			hostPorts := []int{}
			for _, port := range container.Ports {
				if port.PublicPort != 0 && !seen[port.PrivatePort] {
					seen[port.PrivatePort] = true
					hostPorts = append(hostPorts, port.PublicPort)
				}
			}
			sort.Ints(hostPorts)
			hostAddress := "localhost"
			for _, port := range container.Ports {
				if port.PublicPort != 0 {
					if port.IP != "" {
						hostAddress = port.IP
					}
					break
				}
			}
			networkNames := []string{}
			for key := range info.NetworkSettings.Networks {
				networkNames = append(networkNames, key)
			}
			sort.Strings(networkNames)
			image, tag := parseImage(container.Image)
			status := "unknown"
			if container.State == "running" {
				status = "up"
			} else if container.State == "exited" {
				status = "down"
			}
			protocol := "tcp"
			checkPort := (*int)(nil)
			if len(hostPorts) > 0 {
				protocol = detectProtocol(hostPorts[0])
				value := hostPorts[0]
				checkPort = &value
			}
			service := repo.Service{
				ID: "docker-" + repo.NewID(), Name: name, Host: hostAddress,
				Protocol: &protocol, Ports: hostPorts, CheckPort: checkPort,
				Source: "docker", Status: status,
				Metadata: map[string]any{
					"dockerHostId": host.ID,
					"containerId":  container.ID,
					"containerName": name,
					"image":         image,
					"imageTag":      tag,
					"imageDigest":   m.imageDigest(ctx, host, info.Image),
					"networkNames":  networkNames,
				},
				Labels: []string{}, CreatedAt: now, UpdatedAt: now,
			}
			services = append(services, service)
		}
	}
	return services, nil
}

func (m *Manager) serviceHost(service repo.Service) (Host, string, error) {
	if service.Source != "docker" {
		return Host{}, "", errors.New("Not a Docker service")
	}
	id, ok := service.Metadata["dockerHostId"].(string)
	if !ok {
		return Host{}, "", errors.New("Container metadata not available")
	}
	host, exists := m.byID[id]
	if !exists {
		return Host{}, "", errors.New("Docker host not configured")
	}
	containerID, ok := service.Metadata["containerId"].(string)
	if !ok || containerID == "" {
		return Host{}, "", errors.New("Container metadata not available")
	}
	return host, containerID, nil
}

func (m *Manager) Action(ctx context.Context, service repo.Service, action string) error {
	if action != "stop" && action != "start" && action != "restart" {
		return errors.New("Invalid action")
	}
	host, id, err := m.serviceHost(service)
	if err != nil {
		return err
	}
	return host.client.request(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/"+action, nil, nil)
}

func (m *Manager) Stats(ctx context.Context, service repo.Service) (ContainerStats, error) {
	host, id, err := m.serviceHost(service)
	if err != nil {
		return ContainerStats{}, err
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var raw statsResponse
	if err := host.client.request(timeoutCtx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/stats?stream=false", nil, &raw); err != nil {
		return ContainerStats{}, err
	}
	cpuDelta := raw.CPUStats.CPUUsage.TotalUsage - raw.PreCPUStats.CPUUsage.TotalUsage
	systemDelta := raw.CPUStats.SystemCPUUsage - raw.PreCPUStats.SystemCPUUsage
	cpuPercent := 0.0
	if systemDelta > 0 {
		cpuPercent = cpuDelta / systemDelta * 100
		if cpuPercent > 100 {
			cpuPercent = 100
		}
	}
	cache := raw.MemoryStats.Stats["inactive_file"]
	if cache == 0 {
		cache = raw.MemoryStats.Stats["cache"]
	}
	memoryUsed := raw.MemoryStats.Usage - cache
	memoryPercent := 0.0
	if raw.MemoryStats.Limit > 0 {
		memoryPercent = round1(memoryUsed / raw.MemoryStats.Limit * 100)
	}
	var networkRx, networkTx float64
	for _, network := range raw.Networks {
		networkRx += network.RxBytes
		networkTx += network.TxBytes
	}
	var blockRead, blockWrite float64
	for _, entry := range raw.BlkioStats.IOServiceBytesRecursive {
		switch strings.ToLower(entry.Op) {
		case "read":
			blockRead += entry.Value
		case "write":
			blockWrite += entry.Value
		}
	}
	return ContainerStats{
		CPUPercent: round1(cpuPercent), MemoryUsed: memoryUsed, MemoryLimit: raw.MemoryStats.Limit,
		MemoryPercent: memoryPercent, NetworkRx: networkRx, NetworkTx: networkTx,
		BlockRead: blockRead, BlockWrite: blockWrite,
	}, nil
}

func round1(value float64) float64 {
	return float64(int(value*10+0.5)) / 10
}
