package certmonitor

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"sort"
	"strconv"
	"strings"

	"dockdash/internal/certvault"
	"dockdash/internal/notify"
	"dockdash/internal/repo"
)

type Monitor struct {
	service    *Service
	certVault  *certvault.Client
	notifier   *notify.Client
	repo       *repo.Repo
	locale     string
	thresholds string
}

type notice struct {
	titleKey string
	bodyKey  string
	kind     notify.NotificationType
	values   map[string]string
}

func NewMonitor(service *Service, vault *certvault.Client, notifier *notify.Client, database *repo.Repo, locale, thresholds string) *Monitor {
	return &Monitor{service: service, certVault: vault, notifier: notifier, repo: database,
		locale: locale, thresholds: thresholds}
}

func (m *Monitor) CheckAll(ctx context.Context) error {
	if !m.notifier.Configured() {
		return nil
	}
	certificates := m.service.GetAll(ctx, true)
	if len(certificates) == 0 || ctx.Err() != nil {
		return nil
	}
	services, err := m.repo.GetServices()
	if err != nil {
		return err
	}
	byID := map[string]repo.Service{}
	for _, service := range services {
		byID[service.ID] = service
	}
	vaultStatuses, vaultAvailable := m.certVault.Statuses(ctx, certificates, byID)
	for _, certificate := range certificates {
		if ctx.Err() != nil {
			return nil
		}
		service, ok := byID[certificate.ServiceID]
		if !ok {
			continue
		}
		previous, err := m.repo.GetCertificateNotificationState(certificate.ServiceID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if errors.Is(err, sql.ErrNoRows) {
			previous = nil
		}
		threshold := expiryThreshold(certificate, m.thresholds)
		vaultStatus := ""
		if vaultAvailable {
			vaultStatus = vaultStatuses[certificate.ServiceID]
		} else if previous != nil && previous.CertVaultStatus != nil {
			vaultStatus = *previous.CertVaultStatus
		}
		if certificate.Health == "error" && certificate.FingerprintSHA256 == nil {
			continue
		}
		notices := m.events(certificate, service, previous, threshold, vaultStatus)
		deliveryFailed := false
		for _, item := range notices {
			title := notify.Message(m.locale, item.titleKey, item.values)
			body := notify.Message(m.locale, item.bodyKey, item.values)
			if err := m.notifier.Notify(ctx, title, body, item.kind); err != nil {
				log.Printf("Certificate monitor: notification failed: %v", err)
				deliveryFailed = true
			}
		}
		if deliveryFailed {
			continue
		}
		state := repo.CertificateNotificationState{
			ServiceID: certificate.ServiceID, Health: certificate.Health,
			FingerprintSHA256: certificate.FingerprintSHA256, WarningThreshold: threshold,
			CertVaultStatus: nil,
		}
		if vaultStatus != "" {
			state.CertVaultStatus = &vaultStatus
		}
		if err := m.repo.SaveCertificateNotificationState(state); err != nil {
			log.Printf("Certificate monitor: failed to save state for %q: %v", service.Name, err)
		}
	}
	return nil
}

func (m *Monitor) events(certificate Certificate, service repo.Service, previous *repo.CertificateNotificationState, threshold *int, vaultStatus string) []notice {
	if certificate.Health == "error" && certificate.FingerprintSHA256 == nil {
		return nil
	}
	values := map[string]string{"name": service.Name, "target": certificate.Hostname + ":" + strconv.Itoa(certificate.Port)}
	notices := []notice{}
	previousFingerprint := ""
	previousHealth := ""
	previousThreshold := 0
	previousVault := ""
	var hadPreviousThreshold bool
	if previous != nil {
		if previous.FingerprintSHA256 != nil {
			previousFingerprint = *previous.FingerprintSHA256
		}
		previousHealth = previous.Health
		if previous.WarningThreshold != nil {
			previousThreshold = *previous.WarningThreshold
			hadPreviousThreshold = true
		}
		if previous.CertVaultStatus != nil {
			previousVault = *previous.CertVaultStatus
		}
	}
	currentFingerprint := ""
	if certificate.FingerprintSHA256 != nil {
		currentFingerprint = *certificate.FingerprintSHA256
	}
	if previousFingerprint != "" && currentFingerprint != "" && previousFingerprint != currentFingerprint {
		notices = append(notices, notice{"certificateRenewed", "certificateRenewedBody", notify.TypeSuccess, values})
	}
	if certificate.Health == "error" && previousHealth != "error" && currentFingerprint != "" {
		values["error"] = certificate.Error
		notices = append(notices, notice{"certificateError", "certificateErrorBody", notify.TypeFailure, values})
	}
	if certificate.Health == "healthy" && previousHealth == "error" && currentFingerprint != "" {
		notices = append(notices, notice{"certificateRecovered", "certificateRecoveredBody", notify.TypeSuccess, values})
	}
	if threshold != nil && (!hadPreviousThreshold || previousThreshold != *threshold) {
		values["days"] = strconv.Itoa(*certificate.DaysRemaining)
		values["date"] = validToDate(certificate)
		notices = append(notices, notice{"certificateExpiring", "certificateExpiringBody", notify.TypeWarning, values})
	}
	if vaultStatus == "different" && previousVault != "different" {
		notices = append(notices, notice{"certificateMismatch", "certificateMismatchBody", notify.TypeWarning, values})
	} else if vaultStatus == "in-use" && previousVault == "different" {
		notices = append(notices, notice{"certificateMismatchResolved", "certificateMismatchResolvedBody", notify.TypeSuccess, values})
	}
	return notices
}

func expiryThreshold(certificate Certificate, raw string) *int {
	if certificate.Health != "warning" || certificate.DaysRemaining == nil {
		return nil
	}
	crossed := []int{}
	for _, part := range strings.Split(raw, ",") {
		value, err := strconv.Atoi(strings.TrimSpace(part))
		if err == nil && value > 0 && *certificate.DaysRemaining <= value {
			crossed = append(crossed, value)
		}
	}
	if len(crossed) == 0 {
		return nil
	}
	sort.Sort(sort.Reverse(sort.IntSlice(crossed)))
	result := crossed[len(crossed)-1]
	return &result
}

func validToDate(certificate Certificate) string {
	if certificate.ValidTo == nil {
		return ""
	}
	return *certificate.ValidTo
}
