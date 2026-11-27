package certmonitor

import (
	"testing"

	"dockdash/internal/notify"
	"dockdash/internal/repo"
)

func stringPtr(value string) *string { return &value }
func intPtr(value int) *int          { return &value }

func TestExpiryThreshold(t *testing.T) {
	certificate := Certificate{Health: "warning", DaysRemaining: intPtr(7)}
	if got := expiryThreshold(certificate, "30,14,7,3,1"); got == nil || *got != 7 {
		t.Fatalf("unexpected threshold: %v", got)
	}
	certificate.Health = "healthy"
	if got := expiryThreshold(certificate, "30,14,7,3,1"); got != nil {
		t.Fatalf("unexpected threshold: %v", got)
	}
}

func TestEvents(t *testing.T) {
	monitor := &Monitor{locale: "en"}
	service := repo.Service{Name: "web"}
	certificate := Certificate{
		Hostname: "web.example.com", Port: 443, Health: "error", Error: "expired",
		FingerprintSHA256: stringPtr("abc"), DaysRemaining: intPtr(0), ValidTo: stringPtr("2026-01-01T00:00:00Z"),
	}
	previous := &repo.CertificateNotificationState{Health: "healthy", FingerprintSHA256: stringPtr("abc")}
	notices := monitor.events(certificate, service, previous, nil, "")
	if len(notices) != 1 || notices[0].titleKey != "certificateError" || notices[0].kind != notify.TypeFailure {
		t.Fatalf("unexpected notices: %#v", notices)
	}

	certificate.Health = "healthy"
	certificate.Error = ""
	certificate.DaysRemaining = intPtr(20)
	previous = &repo.CertificateNotificationState{Health: "error", FingerprintSHA256: stringPtr("abc")}
	notices = monitor.events(certificate, service, previous, nil, "")
	if len(notices) != 1 || notices[0].titleKey != "certificateRecovered" {
		t.Fatalf("unexpected notices: %#v", notices)
	}
}
