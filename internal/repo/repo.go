package repo

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type Service struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Host          string         `json:"host"`
	Protocol      *string        `json:"protocol,omitempty"`
	Ports         []int          `json:"ports"`
	CheckPort     *int           `json:"checkPort,omitempty"`
	Source        string         `json:"source"`
	Status        string         `json:"status"`
	Metadata      map[string]any `json:"metadata"`
	Labels        []string       `json:"labels"`
	SourceName    string         `json:"sourceName,omitempty"`
	OnDashboard   *bool          `json:"onDashboard,omitempty"`
	CPUPercent    *float64       `json:"cpuPercent,omitempty"`
	MemoryPercent *float64       `json:"memoryPercent,omitempty"`
	CreatedAt     string         `json:"createdAt"`
	UpdatedAt     string         `json:"updatedAt"`
}

type ServiceWithPosition struct {
	Service
	Position *ServicePosition `json:"position"`
}

type ServicePosition struct {
	ServiceID string   `json:"serviceId"`
	X         float64  `json:"x"`
	Y         float64  `json:"y"`
	ParentID  *string  `json:"parentId,omitempty"`
	W         *float64 `json:"w,omitempty"`
	H         *float64 `json:"h,omitempty"`
}

type ServiceLink struct {
	ID          string  `json:"id"`
	SourceID    string  `json:"sourceId"`
	SourceName  string  `json:"sourceName,omitempty"`
	TargetID    string  `json:"targetId"`
	TargetName  string  `json:"targetName,omitempty"`
	Type        string  `json:"type"`
	Label       *string `json:"label,omitempty"`
	Description *string `json:"description,omitempty"`
	TargetPort  *int    `json:"targetPort,omitempty"`
	Protocol    *string `json:"protocol,omitempty"`
	CreatedAt   string  `json:"createdAt,omitempty"`
}

type StatusMetadata struct {
	ImageTag      *string `json:"imageTag,omitempty"`
	HasUpdate     *bool   `json:"hasUpdate,omitempty"`
	LatestVersion *string `json:"latestVersion,omitempty"`
}

type ServiceStatusItem struct {
	ID       string          `json:"id"`
	Status   string          `json:"status"`
	Metadata *StatusMetadata `json:"metadata,omitempty"`
}

type Field[T any] struct {
	Set   bool
	Value T
}

type ServicePatch struct {
	Name      Field[string]
	Host      Field[string]
	Protocol  Field[*string]
	Ports     Field[[]int]
	CheckPort Field[*int]
	Labels    Field[[]string]
}

type BatchLabelError struct {
	Message    string
	Status     int
	ServiceIDs []string
}

func (e *BatchLabelError) Error() string { return e.Message }

type Repo struct {
	db *sql.DB
}

func New(database *sql.DB) *Repo { return &Repo{db: database} }

func NewID() string {
	return newID()
}

func newID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	hexValue := hex.EncodeToString(buf)
	return hexValue[:8] + "-" + hexValue[8:12] + "-" + hexValue[12:16] + "-" + hexValue[16:20] + "-" + hexValue[20:]
}

func encodeJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func parseStringArray(raw string) []string {
	result := []string{}
	_ = json.Unmarshal([]byte(raw), &result)
	if result == nil {
		return []string{}
	}
	return result
}

func parseIntArray(raw string) []int {
	result := []int{}
	_ = json.Unmarshal([]byte(raw), &result)
	if result == nil {
		return []int{}
	}
	return result
}

func parseMetadata(raw string) map[string]any {
	result := map[string]any{}
	_ = json.Unmarshal([]byte(raw), &result)
	return result
}

func normalizeLabel(label string) string {
	return strings.ToLower(strings.TrimSpace(label))
}

type scanner interface {
	Scan(dest ...any) error
}

func scanService(s scanner) (Service, error) {
	var service Service
	var protocol sql.NullString
	var checkPort sql.NullInt64
	var ports, metadata string
	err := s.Scan(&service.ID, &service.Name, &service.Host, &protocol, &ports, &checkPort,
		&service.Source, &service.Status, &metadata, &service.CreatedAt, &service.UpdatedAt)
	if err != nil {
		return service, err
	}
	service.Ports = parseIntArray(ports)
	service.Metadata = parseMetadata(metadata)
	service.Labels = []string{}
	if protocol.Valid {
		value := protocol.String
		service.Protocol = &value
	}
	if checkPort.Valid {
		value := int(checkPort.Int64)
		service.CheckPort = &value
	}
	return service, nil
}

const serviceColumns = `id, name, host, protocol, ports, check_port, source, status, metadata, created_at, updated_at`

func (r *Repo) labelsByService() (map[string][]string, error) {
	rows, err := r.db.Query(`SELECT sl.service_id, l.name FROM service_labels sl
		JOIN labels l ON l.id = sl.label_id ORDER BY l.name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string][]string{}
	for rows.Next() {
		var serviceID, name string
		if err := rows.Scan(&serviceID, &name); err != nil {
			return nil, err
		}
		result[serviceID] = append(result[serviceID], name)
	}
	return result, rows.Err()
}

func (r *Repo) dashboardIDs() (map[string]bool, error) {
	rows, err := r.db.Query(`SELECT service_id FROM service_positions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result[id] = true
	}
	return result, rows.Err()
}

func (r *Repo) GetServices() ([]Service, error) {
	labels, err := r.labelsByService()
	if err != nil {
		return nil, err
	}
	dashboard, err := r.dashboardIDs()
	if err != nil {
		return nil, err
	}
	rows, err := r.db.Query(`SELECT ` + serviceColumns + ` FROM services ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Service{}
	for rows.Next() {
		service, err := scanService(rows)
		if err != nil {
			return nil, err
		}
		service.Labels = labels[service.ID]
		if service.Labels == nil {
			service.Labels = []string{}
		}
		value := dashboard[service.ID]
		service.OnDashboard = &value
		result = append(result, service)
	}
	return result, rows.Err()
}

func (r *Repo) GetService(id string) (Service, error) {
	service, err := scanService(r.db.QueryRow(`SELECT `+serviceColumns+` FROM services WHERE id = ?`, id))
	if err != nil {
		return service, err
	}
	service.Labels, err = r.GetLabelsForService(id)
	if err != nil {
		return service, err
	}
	return service, nil
}

func (r *Repo) GetServiceStatuses() ([]ServiceStatusItem, error) {
	rows, err := r.db.Query(`SELECT id, status, metadata FROM services`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ServiceStatusItem{}
	for rows.Next() {
		var item ServiceStatusItem
		var raw string
		if err := rows.Scan(&item.ID, &item.Status, &raw); err != nil {
			return nil, err
		}
		metadata := parseMetadata(raw)
		status := &StatusMetadata{}
		if value, ok := metadata["imageTag"].(string); ok {
			status.ImageTag = &value
		}
		if value, ok := metadata["hasUpdate"].(bool); ok {
			status.HasUpdate = &value
		}
		if value, ok := metadata["latestVersion"].(string); ok {
			status.LatestVersion = &value
		}
		item.Metadata = status
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *Repo) SaveService(service Service, labels []string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO services (`+serviceColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		service.ID, service.Name, service.Host, service.Protocol, encodeJSON(service.Ports), service.CheckPort,
		service.Source, service.Status, encodeJSON(service.Metadata), service.CreatedAt, service.UpdatedAt)
	if err != nil {
		return err
	}
	if err := replaceLabels(tx, service.ID, labels); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Repo) UpdateService(id string, patch ServicePatch) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRow(`SELECT 1 FROM services WHERE id = ?`, id).Scan(&exists); err != nil {
		return errors.New("Service not found")
	}
	setParts := []string{}
	args := []any{}
	if patch.Name.Set {
		setParts = append(setParts, "name = ?")
		args = append(args, patch.Name.Value)
	}
	if patch.Host.Set {
		setParts = append(setParts, "host = ?")
		args = append(args, patch.Host.Value)
	}
	if patch.Protocol.Set {
		setParts = append(setParts, "protocol = ?")
		args = append(args, patch.Protocol.Value)
	}
	if patch.Ports.Set {
		setParts = append(setParts, "ports = ?")
		args = append(args, encodeJSON(patch.Ports.Value))
	}
	if patch.CheckPort.Set {
		setParts = append(setParts, "check_port = ?")
		args = append(args, patch.CheckPort.Value)
	}
	setParts = append(setParts, "updated_at = ?")
	args = append(args, nowTimestamp())
	args = append(args, id)
	if _, err := tx.Exec(`UPDATE services SET `+strings.Join(setParts, ", ")+` WHERE id = ?`, args...); err != nil {
		return err
	}
	if patch.Labels.Set {
		if err := replaceLabels(tx, id, patch.Labels.Value); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *Repo) UpdateServiceMetadata(id string, patch map[string]any) error {
	return r.updateServiceMetadata(id, "", "", "", patch)
}

func (r *Repo) UpdateServiceMetadataIfUnchanged(id, image, imageTag, imageDigest string, patch map[string]any) (bool, error) {
	err := r.updateServiceMetadata(id, image, imageTag, imageDigest, patch)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (r *Repo) updateServiceMetadata(id, image, imageTag, imageDigest string, patch map[string]any) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw string
	if err := tx.QueryRow(`SELECT metadata FROM services WHERE id = ?`, id).Scan(&raw); err != nil {
		return err
	}
	metadata := parseMetadata(raw)
	if image != "" {
		if current, _ := metadata["image"].(string); current != image {
			return sql.ErrNoRows
		}
		if current, _ := metadata["imageTag"].(string); current != imageTag {
			return sql.ErrNoRows
		}
		if current, _ := metadata["imageDigest"].(string); current != imageDigest {
			return sql.ErrNoRows
		}
	}
	for key, value := range patch {
		metadata[key] = value
	}
	if _, err := tx.Exec(`UPDATE services SET metadata = ?, updated_at = ? WHERE id = ?`,
		encodeJSON(metadata), nowTimestamp(), id); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Repo) DeleteService(id string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM services WHERE id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM labels WHERE NOT EXISTS
		(SELECT 1 FROM service_labels WHERE service_labels.label_id = labels.id)`); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Repo) GetLabels() ([]string, error) {
	rows, err := r.db.Query(`SELECT name FROM labels ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		result = append(result, name)
	}
	return result, rows.Err()
}

type queryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

func getLabelsForService(q queryer, serviceID string) ([]string, error) {
	rows, err := q.Query(`SELECT l.name FROM service_labels sl JOIN labels l ON l.id = sl.label_id
		WHERE sl.service_id = ? ORDER BY l.name ASC`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		result = append(result, name)
	}
	return result, rows.Err()
}

func (r *Repo) GetLabelsForService(serviceID string) ([]string, error) {
	return getLabelsForService(r.db, serviceID)
}

func deleteOrphanLabels(tx *sql.Tx) error {
	_, err := tx.Exec(`DELETE FROM labels WHERE NOT EXISTS
		(SELECT 1 FROM service_labels WHERE service_labels.label_id = labels.id)`)
	return err
}

func replaceLabels(tx *sql.Tx, serviceID string, names []string) error {
	unique := map[string]string{}
	for _, name := range names {
		display := strings.TrimSpace(name)
		normalized := normalizeLabel(display)
		if display != "" {
			if _, exists := unique[normalized]; !exists {
				unique[normalized] = display
			}
		}
	}
	if _, err := tx.Exec(`DELETE FROM service_labels WHERE service_id = ?`, serviceID); err != nil {
		return err
	}
	keys := make([]string, 0, len(unique))
	for key := range unique {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, normalized := range keys {
		var id string
		err := tx.QueryRow(`SELECT id FROM labels WHERE normalized_name = ?`, normalized).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			id = newID()
			if _, err := tx.Exec(`INSERT INTO labels (id, name, normalized_name, created_at) VALUES (?, ?, ?, ?)`,
				id, unique[normalized], normalized, nowTimestamp()); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO service_labels (service_id, label_id) VALUES (?, ?)
			ON CONFLICT DO NOTHING`, serviceID, id); err != nil {
			return err
		}
	}
	return deleteOrphanLabels(tx)
}

func (r *Repo) AssignLabels(serviceIDs, additions []string) ([]Service, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	uniqueIDs := []string{}
	seenIDs := map[string]bool{}
	for _, id := range serviceIDs {
		if !seenIDs[id] {
			seenIDs[id] = true
			uniqueIDs = append(uniqueIDs, id)
		}
	}
	services := []Service{}
	missing := []string{}
	for _, id := range uniqueIDs {
		service, err := scanService(tx.QueryRow(`SELECT `+serviceColumns+` FROM services WHERE id = ?`, id))
		if err != nil {
			missing = append(missing, id)
			continue
		}
		labels, err := getLabelsForService(tx, id)
		if err != nil {
			return nil, err
		}
		service.Labels = labels
		services = append(services, service)
	}
	if len(missing) > 0 {
		return nil, &BatchLabelError{
			Message:    "Services not found: " + strings.Join(missing, ", "),
			Status:     404,
			ServiceIDs: missing,
		}
	}
	updated := []Service{}
	overLimit := []string{}
	overLimitIDs := []string{}
	for _, service := range services {
		labels := append([]string{}, service.Labels...)
		existing := map[string]bool{}
		for _, label := range labels {
			existing[normalizeLabel(label)] = true
		}
		for _, label := range additions {
			normalized := normalizeLabel(label)
			if !existing[normalized] {
				labels = append(labels, strings.TrimSpace(label))
				existing[normalized] = true
			}
		}
		if len(labels) > 20 {
			overLimit = append(overLimit, service.Name)
			overLimitIDs = append(overLimitIDs, service.ID)
			continue
		}
		if err := replaceLabels(tx, service.ID, labels); err != nil {
			return nil, err
		}
		sort.Strings(labels)
		service.Labels = labels
		updated = append(updated, service)
	}
	if len(overLimit) > 0 {
		return nil, &BatchLabelError{
			Message:    fmt.Sprintf("Label limit (20) exceeded for: %s", strings.Join(overLimit, ", ")),
			Status:     400,
			ServiceIDs: overLimitIDs,
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return updated, nil
}

func Now() string {
	return nowTimestamp()
}

func nowTimestamp() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}
