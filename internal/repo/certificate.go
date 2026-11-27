package repo

import "database/sql"

type CertificateNotificationState struct {
	ServiceID         string
	Health            string
	FingerprintSHA256 *string
	WarningThreshold  *int
	CertVaultStatus   *string
	UpdatedAt         string
}

func (r *Repo) GetCertificateNotificationState(serviceID string) (*CertificateNotificationState, error) {
	var state CertificateNotificationState
	var fingerprint, vaultStatus sql.NullString
	var threshold sql.NullInt64
	err := r.db.QueryRow(`SELECT service_id, health, fingerprint_sha256, warning_threshold, cert_vault_status, updated_at
		FROM certificate_notification_states WHERE service_id = ?`, serviceID).Scan(
		&state.ServiceID, &state.Health, &fingerprint, &threshold, &vaultStatus, &state.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if fingerprint.Valid {
		state.FingerprintSHA256 = &fingerprint.String
	}
	if vaultStatus.Valid {
		state.CertVaultStatus = &vaultStatus.String
	}
	if threshold.Valid {
		value := int(threshold.Int64)
		state.WarningThreshold = &value
	}
	return &state, nil
}

func (r *Repo) SaveCertificateNotificationState(state CertificateNotificationState) error {
	_, err := r.db.Exec(`INSERT INTO certificate_notification_states
		(service_id, health, fingerprint_sha256, warning_threshold, cert_vault_status, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(service_id) DO UPDATE SET
		health = excluded.health, fingerprint_sha256 = excluded.fingerprint_sha256,
		warning_threshold = excluded.warning_threshold, cert_vault_status = excluded.cert_vault_status,
		updated_at = excluded.updated_at`,
		state.ServiceID, state.Health, state.FingerprintSHA256, state.WarningThreshold,
		state.CertVaultStatus, nowTimestamp())
	return err
}
