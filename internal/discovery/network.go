package discovery

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/netip"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"dockdash/internal/repo"
)

type commandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

type Scanner struct {
	run commandRunner
}

type pingHost struct {
	ip       string
	hostname string
}

var (
	pingHostPattern = regexp.MustCompile(`(?m)^Host:\s+(\S+)\s+\(([^)]*)\)\s+Status:\s+Up`)
	portLinePattern = regexp.MustCompile(`(?m)^Host:.*Ports:\s+([^\t]+)`)
	titlePattern    = regexp.MustCompile(`(?i)<title>([^<]+)`)
)

func NewScanner() *Scanner {
	return &Scanner{run: defaultRunner}
}

func newScannerWithRunner(run commandRunner) *Scanner {
	return &Scanner{run: run}
}

func defaultRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

func ValidateCIDR(value string) error {
	prefix, err := netip.ParsePrefix(value)
	if err != nil || !prefix.Addr().Is4() {
		return fmt.Errorf("%s: invalid IPv4 CIDR", value)
	}
	return nil
}

func (s *Scanner) Scan(ctx context.Context, cidrs []string, deep bool, emit func(repo.Service)) error {
	limit := 15
	if deep {
		limit = 5
	}
	semaphore := make(chan struct{}, limit)
	var mu sync.Mutex
	safeEmit := func(service repo.Service) {
		mu.Lock()
		defer mu.Unlock()
		emit(service)
	}

	for _, cidr := range cidrs {
		if err := ctx.Err(); err != nil {
			return err
		}
		output, err := s.run(ctx, "nmap", "-sn", "-T4", cidr, "-oG", "-")
		if err != nil {
			return fmt.Errorf("nmap ping sweep failed for %s: %w", cidr, err)
		}
		hosts := parsePingSweepHosts(string(output))
		var wait sync.WaitGroup
		for _, host := range hosts {
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				wait.Wait()
				return ctx.Err()
			}
			wait.Add(1)
			go func(host pingHost) {
				defer wait.Done()
				defer func() { <-semaphore }()
				if err := s.scanHost(ctx, host, deep, safeEmit); err != nil && ctx.Err() == nil {
					log.Printf("network scan failed for %s: %v", host.ip, err)
				}
			}(host)
		}
		wait.Wait()
	}
	return nil
}

func (s *Scanner) scanHost(ctx context.Context, host pingHost, deep bool, emit func(repo.Service)) error {
	args := []string{"-sT", "-T4"}
	if deep {
		args = append(args, "-p-")
	}
	args = append(args, "--open", host.ip, "-oG", "-")
	output, err := s.run(ctx, "nmap", args...)
	if err != nil {
		return err
	}
	ports := parseNmapOpenPorts(string(output))
	if len(ports) == 0 {
		return nil
	}
	sort.Ints(ports)

	protocol := networkProtocol(ports[0])
	primary := ports[0]
	for _, port := range ports {
		candidate := networkProtocol(port)
		if isHTTPProtocol(candidate) {
			protocol = candidate
			primary = port
			break
		}
	}

	name := host.hostname
	if name == "" {
		name = detectServiceName(ctx, host.ip, primary, protocol)
	}
	if name == "" {
		name = host.ip
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	emit(repo.Service{
		ID:        "net-" + repo.NewID(),
		Name:      name,
		Host:      host.ip,
		Protocol:  &protocol,
		Ports:     ports,
		CheckPort: &primary,
		Source:    "network",
		Status:    "up",
		Metadata:  map[string]any{},
		Labels:    []string{},
		CreatedAt: now,
		UpdatedAt: now,
	})
	return nil
}

func parsePingSweepHosts(output string) []pingHost {
	matches := pingHostPattern.FindAllStringSubmatch(output, -1)
	hosts := make([]pingHost, 0, len(matches))
	for _, match := range matches {
		hosts = append(hosts, pingHost{ip: match[1], hostname: strings.TrimSpace(match[2])})
	}
	return hosts
}

func parseNmapOpenPorts(output string) []int {
	match := portLinePattern.FindStringSubmatch(output)
	if match == nil {
		return []int{}
	}
	ports := []int{}
	for _, entry := range strings.Split(strings.TrimSpace(match[1]), ",") {
		parts := strings.Split(strings.TrimSpace(entry), "/")
		if len(parts) < 2 || parts[1] != "open" {
			continue
		}
		port, err := strconv.Atoi(parts[0])
		if err == nil && port > 0 && port <= 65535 {
			ports = append(ports, port)
		}
	}
	return ports
}

func detectServiceName(ctx context.Context, ip string, port int, protocol string) string {
	if isHTTPProtocol(protocol) {
		baseURL := protocol + "://" + ip + ":" + strconv.Itoa(port)
		if title := fetchTitle(ctx, baseURL, 2*time.Second); title != "" {
			return title
		}
		for _, path := range []string{"/health", "/healthz", "/status", "/api/health"} {
			if fetchHealthy(ctx, baseURL+path, time.Second) {
				return ip
			}
		}
		return ip
	}
	if name, ok := portNames[port]; ok {
		return name
	}
	return ""
}

func fetchTitle(ctx context.Context, url string, timeout time.Duration) string {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	request.Header.Set("User-Agent", "DockDash/1.0")
	client := &http.Client{Timeout: timeout, CheckRedirect: noRedirect}
	response, err := client.Do(request)
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<16))
	if err != nil {
		return ""
	}
	match := titlePattern.FindSubmatch(body)
	if match == nil {
		return ""
	}
	return strings.TrimSpace(string(match[1]))
}

func fetchHealthy(ctx context.Context, url string, timeout time.Duration) bool {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	request.Header.Set("User-Agent", "DockDash/1.0")
	client := &http.Client{Timeout: timeout, CheckRedirect: noRedirect}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<16))
	return err == nil && len(strings.TrimSpace(string(body))) > 0
}

func noRedirect(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
