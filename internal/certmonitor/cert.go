package certmonitor

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"math"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"dockdash/internal/certtypes"
	"dockdash/internal/repo"
)

const (
	cacheTTL         = time.Minute
	probeTimeout     = 5 * time.Second
	warningDays      = 30
	probeConcurrency = 10
)

type Certificate = certtypes.Certificate

type Service struct {
	repo  *repo.Repo
	mu    sync.Mutex
	cache map[string]cachedCertificate
}

type cachedCertificate struct {
	expiresAt time.Time
	value     Certificate
}

func New(database *repo.Repo) *Service {
	return &Service{repo: database, cache: map[string]cachedCertificate{}}
}

func ResolveEndpoint(service repo.Service) (string, int, bool) {
	if service.Protocol == nil || *service.Protocol != "https" {
		return "", 0, false
	}
	host := strings.TrimSpace(service.Host)
	if host == "" {
		return "", 0, false
	}
	candidate := host
	if !strings.Contains(candidate, "://") {
		candidate = "https://" + candidate
	}
	parsed, err := url.Parse(candidate)
	if err != nil || parsed.Scheme != "https" {
		return "", 0, false
	}
	hostname := parsed.Hostname()
	if hostname == "" {
		return "", 0, false
	}
	port := 443
	if raw := parsed.Port(); raw != "" {
		parsedPort, err := strconv.Atoi(raw)
		if err != nil {
			return "", 0, false
		}
		port = parsedPort
	} else if service.CheckPort != nil {
		port = *service.CheckPort
	}
	return hostname, port, true
}

func (s *Service) GetAll(ctx context.Context, force bool) []Certificate {
	services, err := s.repo.GetServices()
	if err != nil {
		return []Certificate{}
	}
	candidates := make([]repo.Service, 0, len(services))
	for _, service := range services {
		if _, _, ok := ResolveEndpoint(service); ok {
			candidates = append(candidates, service)
		}
	}
	results := make([]Certificate, len(candidates))
	semaphore := make(chan struct{}, probeConcurrency)
	var waitGroup sync.WaitGroup
	for index, service := range candidates {
		if ctx.Err() != nil {
			break
		}
		waitGroup.Add(1)
		semaphore <- struct{}{}
		go func(index int, service repo.Service) {
			defer waitGroup.Done()
			defer func() { <-semaphore }()
			certificate, ok := s.GetForService(service.ID, force)
			if ok {
				results[index] = certificate
			}
		}(index, service)
	}
	waitGroup.Wait()
	output := make([]Certificate, 0, len(results))
	for _, certificate := range results {
		if certificate.ServiceID != "" {
			output = append(output, certificate)
		}
	}
	return output
}

func (s *Service) GetForService(serviceID string, force bool) (Certificate, bool) {
	service, err := s.repo.GetService(serviceID)
	if err != nil {
		return Certificate{}, false
	}
	hostname, port, ok := ResolveEndpoint(service)
	if !ok {
		return Certificate{}, false
	}
	if !force {
		s.mu.Lock()
		cached, present := s.cache[serviceID]
		s.mu.Unlock()
		if present && time.Now().Before(cached.expiresAt) {
			return cached.value, true
		}
	}
	certificate := probe(hostname, port, serviceID)
	s.mu.Lock()
	s.cache[serviceID] = cachedCertificate{expiresAt: time.Now().Add(cacheTTL), value: certificate}
	s.mu.Unlock()
	return certificate, true
}

func probe(hostname string, port int, serviceID string) Certificate {
	result := Certificate{
		ServiceID: serviceID, Hostname: hostname, Port: port, Health: "error",
		Domains: []string{},
	}
	servername := ""
	if net.ParseIP(hostname) == nil {
		servername = hostname
	}
	dialer := &net.Dialer{Timeout: probeTimeout}
	connection, err := tls.DialWithDialer(dialer, "tcp", net.JoinHostPort(hostname, strconv.Itoa(port)),
		&tls.Config{InsecureSkipVerify: true, ServerName: servername, MinVersion: tls.VersionTLS12})
	if err != nil && connection == nil {
		result.Error = safeMessage(err)
		return result
	}
	state := connection.ConnectionState()
	_ = connection.Close()
	if len(state.PeerCertificates) == 0 {
		result.Error = "No certificate was served"
		return result
	}
	leaf := state.PeerCertificates[0]
	now := time.Now()
	intermediates := x509.NewCertPool()
	for _, candidate := range state.PeerCertificates[1:] {
		intermediates.AddCert(candidate)
	}
	verifyOptions := x509.VerifyOptions{
		CurrentTime: now, Roots: nil, Intermediates: intermediates, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if servername != "" {
		verifyOptions.DNSName = servername
	}
	_, chainErr := leaf.Verify(verifyOptions)
	hostnameErr := leaf.VerifyHostname(hostname)
	validFrom := leaf.NotBefore.Format(time.RFC3339)
	validTo := leaf.NotAfter.Format(time.RFC3339)
	daysRemaining := remainingDays(leaf.NotAfter, now)
	issuer := firstNonEmpty(leaf.Issuer.Organization, leaf.Issuer.CommonName)
	serial := leaf.SerialNumber.String()
	fingerprintSum := sha256.Sum256(leaf.Raw)
	fingerprint := hex.EncodeToString(fingerprintSum[:])
	result.Trusted = chainErr == nil
	result.HostnameValid = hostnameErr == nil
	result.ValidFrom = &validFrom
	result.ValidTo = &validTo
	result.DaysRemaining = &daysRemaining
	result.Issuer = &issuer
	result.Serial = &serial
	result.FingerprintSHA256 = &fingerprint
	if len(leaf.DNSNames) > 0 {
		result.Domains = append([]string{}, leaf.DNSNames...)
	} else if leaf.Subject.CommonName != "" {
		result.Domains = []string{leaf.Subject.CommonName}
	}
	switch {
	case now.Before(leaf.NotBefore) || now.After(leaf.NotAfter):
		result.Error = "Certificate is expired, not active, or has invalid dates"
	case hostnameErr != nil:
		result.Error = hostnameErr.Error()
	case chainErr != nil:
		result.Error = chainErr.Error()
	}
	if result.Error == "" && daysRemaining <= warningDays {
		result.Health = "warning"
	} else if result.Error == "" {
		result.Health = "healthy"
	}
	return result
}

func remainingDays(validTo, now time.Time) int {
	hours := validTo.Sub(now).Hours()
	if hours <= 0 {
		return 0
	}
	return int(math.Ceil(hours / 24))
}

func firstNonEmpty(values []string, fallback string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return fallback
}

func safeMessage(err error) string {
	message := strings.Join(strings.Fields(err.Error()), " ")
	if len(message) > 500 {
		message = message[:500]
	}
	return message
}
