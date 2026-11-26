package config

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"golang.org/x/crypto/scrypt"
)

const (
	defaultDockerSocket = "unix:///var/run/docker.sock"
	defaultLocalName     = "Local"
	maxScryptMemory      = 128 * 1024 * 1024
)

type DockerHost struct {
	Name string
	Host string
}

type scryptParams struct {
	N     int
	R     int
	P     int
	Salt  []byte
	Hash  []byte
	KeyLen int
}

type Config struct {
	AppRepo                    string
	AppVersion                 string
	Locale                     string
	Port                       int
	DockerHosts                []DockerHost
	KubernetesEnabled          string
	KubernetesKubeconfig       string
	KubernetesContexts         []string
	KubernetesNamespaces       []string
	NetworkCIDRs               []string
	HealthCheckInterval        int
	ResourceMonitorInterval    int
	UpdateCheckInterval        int
	CertificateCheckInterval   int
	CertificateExpiryThresholds string
	ContainerControlsEnabled   bool
	HealthHistoryEnabled       bool
	HealthHistoryTTLDays       int
	ResourceMonitorEnabled     bool
	CPUSpikeThreshold          int
	MemorySpikeThreshold       int
	SpikeDurationThreshold     int
	FileExplorerEnabled        bool
	TerminalEnabled            bool
	OIDCIssuer                 string
	OIDCClientID               string
	OIDCClientSecret           string
	OIDCRedirectURI            string
	OIDCScopes                 string
	AuthUsername               string
	AuthPassword               string
	AuthPasswordHash           string
	SessionSecret              string
	SessionMaxAge              int
	TrustProxy                 string
	AppriseURL                 string
	AppriseURLs                []string
	AppriseTags                []string
	CertVaultURL               string
	CertVaultAPIKey            string
	CertVaultAPIKeyFile        string
	GitHubToken                string
	DBPath                     string
	MigrationsDir              string
	SecureCookies              bool
	AuthMode                   string
	PasswordAuthEnabled        bool
	OIDCEnabled                bool

	passwordHash *scryptParams
}

func envString(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envDisable(key string) bool {
	return os.Getenv(key) != "true"
}

func envList(key string, fallback []string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		if fallback == nil {
			return []string{}
		}
		return append([]string{}, fallback...)
	}
	result := []string{}
	for _, item := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func envInt(key string, fallback, min, max int, allowZero bool) (int, error) {
	raw, present := os.LookupEnv(key)
	if !present {
		return fallback, nil
	}
	trimmed := strings.TrimSpace(raw)
	lower := min
	if allowZero {
		lower = 0
	}
	if trimmed == "" {
		return 0, fmt.Errorf("Invalid %s: expected an integer between %d and %d", key, lower, max)
	}
	for _, char := range trimmed {
		if char < '0' || char > '9' {
			return 0, fmt.Errorf("Invalid %s: expected an integer between %d and %d", key, lower, max)
		}
	}
	value, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || value < int64(lower) || value > int64(max) {
		return 0, fmt.Errorf("Invalid %s: expected an integer between %d and %d", key, lower, max)
	}
	return int(value), nil
}

func parseDockerHost(entry string) (DockerHost, error) {
	separator := strings.Index(entry, "=")
	if separator == -1 || strings.Contains(entry[:separator], "://") {
		name := entry
		if entry == defaultDockerSocket {
			name = defaultLocalName
		}
		return DockerHost{Name: name, Host: entry}, nil
	}
	name := strings.TrimSpace(entry[:separator])
	host := strings.TrimSpace(entry[separator+1:])
	if name == "" || host == "" {
		return DockerHost{}, fmt.Errorf("Invalid DOCKER_HOSTS entry %q. Use either an endpoint or name=endpoint.", entry)
	}
	return DockerHost{Name: name, Host: host}, nil
}

func parsePasswordHash(value string) (*scryptParams, error) {
	parts := strings.Split(value, "$")
	if len(parts) != 6 || parts[0] != "scrypt" {
		return nil, errors.New("Invalid AUTH_PASSWORD_HASH: expected format scrypt$N$r$p$salt$hash")
	}
	numbers := parts[1:4]
	values := make([]int, 3)
	for i, item := range numbers {
		if item == "" {
			return nil, errors.New("Invalid AUTH_PASSWORD_HASH: expected format scrypt$N$r$p$salt$hash")
		}
		for _, char := range item {
			if char < '0' || char > '9' {
				return nil, errors.New("Invalid AUTH_PASSWORD_HASH: expected format scrypt$N$r$p$salt$hash")
			}
		}
		parsed, err := strconv.Atoi(item)
		if err != nil {
			return nil, errors.New("Invalid AUTH_PASSWORD_HASH: expected format scrypt$N$r$p$salt$hash")
		}
		values[i] = parsed
	}
	n, r, p := values[0], values[1], values[2]
	if n < 2 || n&(n-1) != 0 || n > 1048576 || r < 1 || p < 1 {
		return nil, errors.New("Invalid AUTH_PASSWORD_HASH: expected format scrypt$N$r$p$salt$hash")
	}
	salt, err := hex.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return nil, errors.New("Invalid AUTH_PASSWORD_HASH: expected format scrypt$N$r$p$salt$hash")
	}
	hash, err := hex.DecodeString(parts[5])
	if err != nil || len(hash) == 0 {
		return nil, errors.New("Invalid AUTH_PASSWORD_HASH: expected format scrypt$N$r$p$salt$hash")
	}
	if 128*n*r > maxScryptMemory {
		return nil, errors.New("Invalid AUTH_PASSWORD_HASH: expected format scrypt$N$r$p$salt$hash")
	}
	return &scryptParams{N: n, R: r, P: p, Salt: salt, Hash: hash, KeyLen: len(hash)}, nil
}

func Load() (*Config, error) {
	cfg := &Config{
		AppRepo:       envString("APP_REPO", ""),
		AppVersion:    envString("APP_VERSION", "dev"),
		Locale:        envString("LOCALE", "en"),
		KubernetesEnabled:    envString("KUBERNETES_ENABLED", "false"),
		KubernetesKubeconfig: envString("KUBERNETES_KUBECONFIG", ""),
		KubernetesContexts:   envList("KUBERNETES_CONTEXTS", nil),
		KubernetesNamespaces: envList("KUBERNETES_NAMESPACES", []string{"default"}),
		NetworkCIDRs:         envList("NETWORK_CIDRS", []string{"192.168.0.0/24"}),
		CertificateExpiryThresholds: envString("CERTIFICATE_EXPIRY_THRESHOLDS", "30,14,7,3,1"),
		ContainerControlsEnabled: envDisable("DISABLE_CONTAINER_CONTROLS"),
		HealthHistoryEnabled:     envDisable("DISABLE_HEALTH_HISTORY"),
		ResourceMonitorEnabled:   envDisable("DISABLE_RESOURCE_MONITOR"),
		FileExplorerEnabled:      envDisable("DISABLE_FILE_EXPLORER"),
		TerminalEnabled:          envDisable("DISABLE_TERMINAL"),
		OIDCIssuer:       envString("OIDC_ISSUER", ""),
		OIDCClientID:     envString("OIDC_CLIENT_ID", ""),
		OIDCClientSecret: envString("OIDC_CLIENT_SECRET", ""),
		OIDCRedirectURI:  envString("OIDC_REDIRECT_URI", ""),
		OIDCScopes:       envString("OIDC_SCOPES", "openid profile email"),
		AuthUsername:     envString("AUTH_USERNAME", ""),
		AuthPassword:     envString("AUTH_PASSWORD", ""),
		AuthPasswordHash: envString("AUTH_PASSWORD_HASH", ""),
		TrustProxy:       envString("TRUST_PROXY", "loopback, uniquelocal"),
		AppriseURL:       envString("APPRISE_URL", ""),
		AppriseURLs:      envList("APPRISE_URLS", nil),
		AppriseTags:      envList("APPRISE_TAGS", nil),
		CertVaultURL:     envString("CERTVAULT_URL", ""),
		CertVaultAPIKey:  envString("CERTVAULT_API_KEY", ""),
		CertVaultAPIKeyFile: envString("CERTVAULT_API_KEY_FILE", ""),
		GitHubToken:      envString("GITHUB_TOKEN", ""),
		DBPath:           envString("DB_PATH", "dockdash.db"),
		MigrationsDir:    envString("MIGRATIONS_DIR", "drizzle"),
		SecureCookies:    strings.TrimSpace(os.Getenv("NODE_ENV")) == "production",
	}

	var err error
	if cfg.Port, err = envInt("PORT", 3001, 1, 65535, false); err != nil {
		return nil, err
	}
	if cfg.HealthCheckInterval, err = envInt("HEALTH_CHECK_INTERVAL", 30000, 1, 2147483647, false); err != nil {
		return nil, err
	}
	if cfg.ResourceMonitorInterval, err = envInt("RESOURCE_MONITOR_INTERVAL", 5000, 1, 2147483647, false); err != nil {
		return nil, err
	}
	if cfg.UpdateCheckInterval, err = envInt("UPDATE_CHECK_INTERVAL", 3600000, 1, 2147483647, false); err != nil {
		return nil, err
	}
	if cfg.CertificateCheckInterval, err = envInt("CERTIFICATE_CHECK_INTERVAL", 21600000, 1, 2147483647, false); err != nil {
		return nil, err
	}
	if cfg.HealthHistoryTTLDays, err = envInt("HEALTH_HISTORY_TTL_DAYS", 30, 1, int(^uint(0)>>1), false); err != nil {
		return nil, err
	}
	if cfg.CPUSpikeThreshold, err = envInt("CPU_SPIKE_THRESHOLD", 90, 0, int(^uint(0)>>1), true); err != nil {
		return nil, err
	}
	if cfg.MemorySpikeThreshold, err = envInt("MEMORY_SPIKE_THRESHOLD", 90, 0, int(^uint(0)>>1), true); err != nil {
		return nil, err
	}
	if cfg.SpikeDurationThreshold, err = envInt("SPIKE_DURATION_THRESHOLD", 300, 0, int(^uint(0)>>1), true); err != nil {
		return nil, err
	}
	if cfg.SessionMaxAge, err = envInt("SESSION_MAX_AGE", 8*60*60*1000, 1, int(^uint(0)>>1), false); err != nil {
		return nil, err
	}

	for _, entry := range envList("DOCKER_HOSTS", nil) {
		host, err := parseDockerHost(entry)
		if err != nil {
			return nil, err
		}
		cfg.DockerHosts = append(cfg.DockerHosts, host)
	}
	if socketPath := strings.TrimPrefix(defaultDockerSocket, "unix://"); fileExists(socketPath) {
		found := false
		for _, host := range cfg.DockerHosts {
			if host.Host == defaultDockerSocket {
				found = true
				break
			}
		}
		if !found {
			cfg.DockerHosts = append([]DockerHost{{Name: defaultLocalName, Host: defaultDockerSocket}}, cfg.DockerHosts...)
		}
	}
	names := map[string]bool{}
	endpoints := map[string]bool{}
	for _, host := range cfg.DockerHosts {
		name := strings.ToLower(host.Name)
		if names[name] {
			return nil, fmt.Errorf("Duplicate Docker host name in DOCKER_HOSTS: %s", host.Name)
		}
		if endpoints[host.Host] {
			return nil, fmt.Errorf("Duplicate Docker endpoint in DOCKER_HOSTS: %s", host.Host)
		}
		names[name] = true
		endpoints[host.Host] = true
	}

	cfg.PasswordAuthEnabled = cfg.AuthUsername != "" && (cfg.AuthPassword != "" || cfg.AuthPasswordHash != "")
	passwordAny := cfg.AuthUsername != "" || cfg.AuthPassword != "" || cfg.AuthPasswordHash != ""
	if passwordAny && !cfg.PasswordAuthEnabled {
		return nil, errors.New("Incomplete password authentication configuration: set AUTH_USERNAME and AUTH_PASSWORD (or AUTH_PASSWORD_HASH), or unset all three to disable password authentication")
	}
	if cfg.AuthPasswordHash != "" {
		params, err := parsePasswordHash(cfg.AuthPasswordHash)
		if err != nil {
			return nil, err
		}
		cfg.passwordHash = params
	}

	if !cfg.PasswordAuthEnabled {
		oidcAnySet := false
		for _, key := range []string{"OIDC_ISSUER", "OIDC_CLIENT_ID", "OIDC_CLIENT_SECRET", "OIDC_REDIRECT_URI", "OIDC_SCOPES"} {
			if _, present := os.LookupEnv(key); present {
				oidcAnySet = true
			}
		}
		if oidcAnySet {
			missing := []string{}
			if strings.TrimSpace(cfg.OIDCIssuer) == "" {
				missing = append(missing, "OIDC_ISSUER")
			}
			if strings.TrimSpace(cfg.OIDCClientID) == "" {
				missing = append(missing, "OIDC_CLIENT_ID")
			}
			if strings.TrimSpace(cfg.OIDCClientSecret) == "" {
				missing = append(missing, "OIDC_CLIENT_SECRET")
			}
			if len(missing) > 0 {
				return nil, fmt.Errorf("Incomplete OIDC configuration: set %s, or unset all OIDC settings to disable built-in authentication", strings.Join(missing, ", "))
			}
			cfg.OIDCEnabled = true
		}
	}

	if cfg.PasswordAuthEnabled {
		cfg.AuthMode = "password"
	} else if cfg.OIDCEnabled {
		cfg.AuthMode = "oidc"
	} else {
		cfg.AuthMode = "none"
	}

	cfg.SessionSecret = envString("SESSION_SECRET", "")
	if cfg.SessionSecret == "" {
		bytes := make([]byte, 32)
		if _, err := rand.Read(bytes); err != nil {
			return nil, err
		}
		cfg.SessionSecret = hex.EncodeToString(bytes)
		log.Print("WARNING: SESSION_SECRET is not set. A random secret was generated; sessions will be invalidated on restart.")
	}

	return cfg, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func (c *Config) DockerHostEntries() []string {
	entries := make([]string, 0, len(c.DockerHosts))
	for _, host := range c.DockerHosts {
		if host.Name == host.Host {
			entries = append(entries, host.Host)
		} else {
			entries = append(entries, host.Name+"="+host.Host)
		}
	}
	return entries
}

func (c *Config) VerifyPassword(username, password string) bool {
	if !c.PasswordAuthEnabled {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(username), []byte(c.AuthUsername)) != 1 {
		return false
	}
	if c.passwordHash != nil {
		derived, err := scrypt.Key([]byte(password), c.passwordHash.Salt, c.passwordHash.N, c.passwordHash.R, c.passwordHash.P, c.passwordHash.KeyLen)
		if err != nil {
			return false
		}
		return subtle.ConstantTimeCompare(derived, c.passwordHash.Hash) == 1
	}
	return subtle.ConstantTimeCompare([]byte(password), []byte(c.AuthPassword)) == 1
}

func (c *Config) CertVaultAPIKeyValue() string {
	if c.CertVaultAPIKey != "" {
		return c.CertVaultAPIKey
	}
	if c.CertVaultAPIKeyFile == "" {
		return ""
	}
	data, err := os.ReadFile(c.CertVaultAPIKeyFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
