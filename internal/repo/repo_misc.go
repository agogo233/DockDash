package repo

import (
	"database/sql"
	"errors"
	"strings"
)

type PositionInput struct {
	ServiceID string
	X         Field[float64]
	Y         Field[float64]
	ParentID  Field[*string]
	W         Field[*float64]
	H         Field[*float64]
}

type LinkPatch struct {
	Type        Field[string]
	Label       Field[*string]
	Description Field[*string]
	TargetPort  Field[*int]
	Protocol    Field[*string]
}

func scanPosition(s scanner) (ServicePosition, error) {
	var position ServicePosition
	var w, h sql.NullFloat64
	var parentText sql.NullString
	err := s.Scan(&position.ServiceID, &position.X, &position.Y, &parentText, &w, &h)
	if err != nil {
		return position, err
	}
	if parentText.Valid {
		value := parentText.String
		position.ParentID = &value
	}
	if w.Valid {
		value := w.Float64
		position.W = &value
	}
	if h.Valid {
		value := h.Float64
		position.H = &value
	}
	return position, nil
}

func (r *Repo) GetPositions() ([]ServicePosition, error) {
	rows, err := r.db.Query(`SELECT service_id, x, y, parent_id, w, h FROM service_positions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ServicePosition{}
	for rows.Next() {
		position, err := scanPosition(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, position)
	}
	return result, rows.Err()
}

func (r *Repo) SavePositions(inputs []PositionInput) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	parents := map[string]string{}
	rows, err := tx.Query(`SELECT service_id, parent_id FROM service_positions`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		var parent sql.NullString
		if err := rows.Scan(&id, &parent); err != nil {
			rows.Close()
			return err
		}
		parents[id] = parent.String
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, input := range inputs {
		if input.ParentID.Set {
			if input.ParentID.Value == nil {
				parents[input.ServiceID] = ""
			} else {
				parents[input.ServiceID] = *input.ParentID.Value
			}
		} else if _, ok := parents[input.ServiceID]; !ok {
			parents[input.ServiceID] = ""
		}
	}

	for id := range parents {
		visited := map[string]bool{id: true}
		parent := parents[id]
		for parent != "" {
			if _, ok := parents[parent]; !ok {
				return errors.New("Parent must be on the dashboard")
			}
			if visited[parent] {
				return errors.New("Dashboard hierarchy cannot contain cycles")
			}
			visited[parent] = true
			parent = parents[parent]
		}
	}

	for _, input := range inputs {
		var x, y float64
		var w, h sql.NullFloat64
		err := tx.QueryRow(`SELECT x, y, w, h FROM service_positions WHERE service_id = ?`, input.ServiceID).
			Scan(&x, &y, &w, &h)
		if errors.Is(err, sql.ErrNoRows) {
			x, y = 0, 0
		} else if err != nil {
			return err
		}
		if input.X.Set {
			x = input.X.Value
		}
		if input.Y.Set {
			y = input.Y.Value
		}
		var wValue, hValue any
		if w.Valid {
			wValue = w.Float64
		}
		if h.Valid {
			hValue = h.Float64
		}
		if input.W.Set {
			if input.W.Value == nil {
				wValue = nil
			} else {
				wValue = *input.W.Value
			}
		}
		if input.H.Set {
			if input.H.Value == nil {
				hValue = nil
			} else {
				hValue = *input.H.Value
			}
		}
		parent := parents[input.ServiceID]
		var parentValue any
		if parent != "" {
			parentValue = parent
		}
		if _, err := tx.Exec(`INSERT INTO service_positions (service_id, x, y, parent_id, w, h)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(service_id) DO UPDATE SET x = excluded.x, y = excluded.y,
			parent_id = excluded.parent_id, w = excluded.w, h = excluded.h`,
			input.ServiceID, x, y, parentValue, wValue, hValue); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *Repo) AddToDashboard(serviceID string) error {
	_, err := r.db.Exec(`INSERT INTO service_positions (service_id, x, y, parent_id, w, h)
		VALUES (?, 0, 0, NULL, NULL, NULL) ON CONFLICT DO NOTHING`, serviceID)
	return err
}

func (r *Repo) RemoveFromDashboard(serviceID string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	children := map[string][]string{}
	rows, err := tx.Query(`SELECT service_id, parent_id FROM service_positions WHERE parent_id IS NOT NULL`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, parent string
		if err := rows.Scan(&id, &parent); err != nil {
			rows.Close()
			return err
		}
		children[parent] = append(children[parent], id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	removed := map[string]bool{}
	pending := []string{serviceID}
	for len(pending) > 0 {
		id := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if removed[id] {
			continue
		}
		removed[id] = true
		pending = append(pending, children[id]...)
	}
	ids := make([]string, 0, len(removed))
	for id := range removed {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return tx.Commit()
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	if _, err := tx.Exec(`DELETE FROM service_positions WHERE service_id IN (`+placeholders+`)`, args...); err != nil {
		return err
	}
	return tx.Commit()
}

func scanLink(s scanner) (ServiceLink, error) {
	var link ServiceLink
	var label, description, protocol sql.NullString
	var targetPort sql.NullInt64
	err := s.Scan(&link.ID, &link.SourceID, &link.TargetID, &label, &link.Type, &description,
		&targetPort, &protocol, &link.CreatedAt)
	if err != nil {
		return link, err
	}
	if label.Valid {
		value := label.String
		link.Label = &value
	}
	if description.Valid {
		value := description.String
		link.Description = &value
	}
	if targetPort.Valid {
		value := int(targetPort.Int64)
		link.TargetPort = &value
	}
	if protocol.Valid {
		value := protocol.String
		link.Protocol = &value
	}
	return link, nil
}

func (r *Repo) GetLinks() ([]ServiceLink, error) {
	rows, err := r.db.Query(`SELECT l.id, l.source_id, l.target_id, l.label, l.type, l.description,
		l.target_port, l.protocol, l.created_at, s.name, t.name
		FROM service_links l
		JOIN services s ON s.id = l.source_id
		JOIN services t ON t.id = l.target_id
		ORDER BY l.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ServiceLink{}
	for rows.Next() {
		var link ServiceLink
		var label, description, protocol sql.NullString
		var targetPort sql.NullInt64
		if err := rows.Scan(&link.ID, &link.SourceID, &link.TargetID, &label, &link.Type,
			&description, &targetPort, &protocol, &link.CreatedAt, &link.SourceName, &link.TargetName); err != nil {
			return nil, err
		}
		if label.Valid {
			value := label.String
			link.Label = &value
		}
		if description.Valid {
			value := description.String
			link.Description = &value
		}
		if targetPort.Valid {
			value := int(targetPort.Int64)
			link.TargetPort = &value
		}
		if protocol.Valid {
			value := protocol.String
			link.Protocol = &value
		}
		result = append(result, link)
	}
	return result, rows.Err()
}

func (r *Repo) SaveLink(link ServiceLink) error {
	result, err := r.db.Exec(`INSERT INTO service_links (id, source_id, target_id, type, label, description, target_port, protocol)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`,
		link.ID, link.SourceID, link.TargetID, link.Type, link.Label, link.Description, link.TargetPort, link.Protocol)
	if err != nil {
		return err
	}
	changes, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changes == 0 {
		return errors.New("A link between these two services already exists")
	}
	return nil
}

func (r *Repo) UpdateLink(id string, patch LinkPatch) error {
	setParts := []string{}
	args := []any{}
	if patch.Type.Set {
		setParts = append(setParts, "type = ?")
		args = append(args, patch.Type.Value)
	}
	if patch.Label.Set {
		setParts = append(setParts, "label = ?")
		args = append(args, patch.Label.Value)
	}
	if patch.Description.Set {
		setParts = append(setParts, "description = ?")
		args = append(args, patch.Description.Value)
	}
	if patch.TargetPort.Set {
		setParts = append(setParts, "target_port = ?")
		args = append(args, patch.TargetPort.Value)
	}
	if patch.Protocol.Set {
		setParts = append(setParts, "protocol = ?")
		args = append(args, patch.Protocol.Value)
	}
	if len(setParts) == 0 {
		var exists int
		if err := r.db.QueryRow(`SELECT 1 FROM service_links WHERE id = ?`, id).Scan(&exists); err != nil {
			return errors.New("Link not found")
		}
		return nil
	}
	args = append(args, id)
	result, err := r.db.Exec(`UPDATE service_links SET `+strings.Join(setParts, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return err
	}
	changes, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changes == 0 {
		return errors.New("Link not found")
	}
	return nil
}

func (r *Repo) GetLink(id string) (ServiceLink, error) {
	return scanLink(r.db.QueryRow(`SELECT id, source_id, target_id, label, type, description,
		target_port, protocol, created_at FROM service_links WHERE id = ?`, id))
}

func (r *Repo) DeleteLink(id string) error {
	_, err := r.db.Exec(`DELETE FROM service_links WHERE id = ?`, id)
	return err
}

func (r *Repo) GetDashboardData() ([]ServiceWithPosition, []ServiceLink, error) {
	positions, err := r.GetPositions()
	if err != nil {
		return nil, nil, err
	}
	positionMap := map[string]ServicePosition{}
	for _, position := range positions {
		positionMap[position.ServiceID] = position
	}
	services, err := r.GetServices()
	if err != nil {
		return nil, nil, err
	}
	result := []ServiceWithPosition{}
	for _, service := range services {
		position, ok := positionMap[service.ID]
		if !ok {
			continue
		}
		copyPosition := position
		result = append(result, ServiceWithPosition{Service: service, Position: &copyPosition})
	}
	links, err := r.GetLinks()
	if err != nil {
		return nil, nil, err
	}
	return result, links, nil
}
