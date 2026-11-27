package certvault

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"dockdash/internal/certtypes"
	"dockdash/internal/repo"
)

const cacheTTL = time.Minute

type certificate struct {
	Domains        []string `json:"domains"`
	CurrentVersion *struct {
		Fingerprint string `json:"fingerprint_sha256"`
	} `json:"current_version"`
}

type Client struct {
	http      *http.Client
	baseURL   string
	token     string
	mu        sync.Mutex
	cache     []certificate
	expiresAt time.Time
}

func New(baseURL, token string) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL != "" && !strings.HasPrefix(baseURL, "https://") {
		baseURL = ""
	}
	return &Client{
		http:    &http.Client{Timeout: 5 * time.Second},
		baseURL: baseURL,
		token:   token,
	}
}

func (c *Client) Configured() bool { return c.baseURL != "" && c.token != "" }

func (c *Client) Statuses(ctx context.Context, certificates []certtypes.Certificate, services map[string]repo.Service) (map[string]string, bool) {
	result := map[string]string{}
	if !c.Configured() || len(certificates) == 0 {
		return result, false
	}
	vaultCertificates, err := c.fetch(ctx)
	if err != nil {
		return result, false
	}
	for _, live := range certificates {
		service, ok := services[live.ServiceID]
		if !ok || service.Protocol == nil || *service.Protocol != "https" {
			continue
		}
		hostname := normalizeHostname(service.Host)
		if hostname == "" || live.FingerprintSHA256 == nil {
			continue
		}
		fingerprint := strings.ToLower(strings.ReplaceAll(*live.FingerprintSHA256, ":", ""))
		matched := false
		status := "different"
		for _, candidate := range vaultCertificates {
			if !matchesAny(candidate.Domains, hostname) {
				continue
			}
			matched = true
			if candidate.CurrentVersion != nil &&
				strings.EqualFold(strings.ReplaceAll(candidate.CurrentVersion.Fingerprint, ":", ""), fingerprint) {
				status = "in-use"
				break
			}
		}
		if matched {
			result[live.ServiceID] = status
		}
	}
	return result, true
}

func (c *Client) fetch(ctx context.Context) ([]certificate, error) {
	c.mu.Lock()
	if time.Now().Before(c.expiresAt) {
		cached := append([]certificate{}, c.cache...)
		c.mu.Unlock()
		return cached, nil
	}
	c.mu.Unlock()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/certificates", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<16))
		return nil, errStatus(response.StatusCode)
	}
	var certificates []certificate
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&certificates); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.cache = certificates
	c.expiresAt = time.Now().Add(cacheTTL)
	c.mu.Unlock()
	return certificates, nil
}

type statusError int

func (s statusError) Error() string { return "HTTP " + strconv.Itoa(int(s)) }

func errStatus(status int) error { return statusError(status) }

func normalizeHostname(host string) string {
	value := strings.TrimSpace(host)
	if strings.Contains(value, "://") {
		parts := strings.SplitN(value, "://", 2)
		value = parts[1]
	}
	if index := strings.IndexAny(value, "/:"); index >= 0 {
		value = value[:index]
	}
	value = strings.TrimSuffix(strings.ToLower(value), ".")
	if value == "" || strings.Count(value, ".") != 3 {
		return value
	}
	for _, part := range strings.Split(value, ".") {
		if part == "" || len(part) > 3 {
			return value
		}
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 || number > 255 {
			return value
		}
	}
	return ""
}

func matchesAny(domains []string, hostname string) bool {
	for _, domain := range domains {
		domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
		if domain == hostname {
			return true
		}
		if strings.HasPrefix(domain, "*.") {
			suffix := domain[2:]
			if strings.HasSuffix(hostname, "."+suffix) &&
				strings.Count(hostname, ".") == strings.Count(suffix, ".")+1 {
				return true
			}
		}
	}
	return false
}
