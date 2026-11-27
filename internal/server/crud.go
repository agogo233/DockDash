package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"dockdash/internal/repo"
)

type rawBody map[string]json.RawMessage

func decodeBody(request *http.Request, allowed ...string) (rawBody, error) {
	var body rawBody
	decoder := json.NewDecoder(request.Body)
	if err := decoder.Decode(&body); err != nil {
		return nil, errors.New("Invalid request")
	}
	if body == nil {
		return nil, errors.New("Invalid request")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("Invalid request")
	}
	allowedSet := map[string]bool{}
	for _, key := range allowed {
		allowedSet[key] = true
	}
	for key := range body {
		if !allowedSet[key] {
			return nil, fmt.Errorf("%s: Unrecognized key", key)
		}
	}
	return body, nil
}

func readString(body rawBody, key string) (string, bool, bool, error) {
	raw, ok := body[key]
	if !ok {
		return "", false, false, nil
	}
	if string(raw) == "null" {
		return "", true, true, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", true, false, fmt.Errorf("%s: Invalid string", key)
	}
	return value, true, false, nil
}

func readInt(body rawBody, key string) (int, bool, bool, error) {
	raw, ok := body[key]
	if !ok {
		return 0, false, false, nil
	}
	if string(raw) == "null" {
		return 0, true, true, nil
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, true, false, fmt.Errorf("%s: Invalid number", key)
	}
	return value, true, false, nil
}

func readFloat(body rawBody, key string) (float64, bool, bool, error) {
	raw, ok := body[key]
	if !ok {
		return 0, false, false, nil
	}
	if string(raw) == "null" {
		return 0, true, true, nil
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, true, false, fmt.Errorf("%s: Invalid number", key)
	}
	return value, true, false, nil
}

func readStrings(body rawBody, key string) ([]string, bool, bool, error) {
	raw, ok := body[key]
	if !ok {
		return nil, false, false, nil
	}
	if string(raw) == "null" {
		return nil, true, true, nil
	}
	var value []string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, true, false, fmt.Errorf("%s: Invalid array", key)
	}
	return value, true, false, nil
}

func readInts(body rawBody, key string) ([]int, bool, bool, error) {
	raw, ok := body[key]
	if !ok {
		return nil, false, false, nil
	}
	if string(raw) == "null" {
		return nil, true, true, nil
	}
	var value []int
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, true, false, fmt.Errorf("%s: Invalid array", key)
	}
	return value, true, false, nil
}

func validPort(value int) bool { return value >= 1 && value <= 65535 }

func validProtocol(value string) bool {
	switch value {
	case "http", "https", "tcp", "udp", "ssh", "ftp", "smtp", "dns", "mqtt", "grpc", "websocket", "custom":
		return true
	}
	return false
}

func validSource(value string) bool {
	return value == "docker" || value == "kubernetes" || value == "network"
}

func validLinkType(value string) bool {
	return value == "communication" || value == "dependency" || value == "other"
}

func parseLabels(values []string) ([]string, error) {
	if len(values) > 20 {
		return nil, errors.New("labels: Array must contain at most 20 element(s)")
	}
	result := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || utf8.RuneCountInString(trimmed) > 50 {
			return nil, errors.New("labels: Invalid service label")
		}
		normalized := strings.ToLower(trimmed)
		if seen[normalized] {
			return nil, errors.New("labels: Duplicate service label")
		}
		seen[normalized] = true
		result = append(result, trimmed)
	}
	return result, nil
}

func parsePorts(values []int) error {
	for _, value := range values {
		if !validPort(value) {
			return errors.New("ports: Invalid port")
		}
	}
	return nil
}

func writeRepoError(writer http.ResponseWriter, err error) {
	var batch *repo.BatchLabelError
	if errors.As(err, &batch) {
		writeJSON(writer, batch.Status, map[string]any{"error": batch.Message, "serviceIds": batch.ServiceIDs})
		return
	}
	writeError(writer, http.StatusInternalServerError, err.Error())
}

func (a *API) registerCRUDRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/services", a.listServices)
	mux.HandleFunc("GET /api/labels", a.listLabels)
	mux.HandleFunc("GET /api/serviceStatuses", a.listServiceStatuses)
	mux.HandleFunc("GET /api/dashboard", a.getDashboard)
	mux.HandleFunc("GET /api/services/{id}", a.getService)
	mux.HandleFunc("POST /api/services", a.createService)
	mux.HandleFunc("PUT /api/services/{id}", a.updateService)
	mux.HandleFunc("DELETE /api/services/{id}", a.deleteService)
	mux.HandleFunc("POST /api/services/labels", a.assignLabels)
	mux.HandleFunc("POST /api/services/{id}/dashboard", a.addToDashboard)
	mux.HandleFunc("DELETE /api/services/{id}/dashboard", a.removeFromDashboard)
	mux.HandleFunc("POST /api/positions", a.savePositions)
	mux.HandleFunc("POST /api/links", a.createLink)
	mux.HandleFunc("PUT /api/links/{id}", a.updateLink)
	mux.HandleFunc("DELETE /api/links/{id}", a.deleteLink)
}

func (a *API) listServices(writer http.ResponseWriter, _ *http.Request) {
	services, err := repo.New(a.db).GetServices()
	if err != nil {
		writeRepoError(writer, err)
		return
	}
	a.enrichServices(services)
	writeJSON(writer, http.StatusOK, services)
}

func (a *API) listLabels(writer http.ResponseWriter, _ *http.Request) {
	labels, err := repo.New(a.db).GetLabels()
	if err != nil {
		writeRepoError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, labels)
}

func (a *API) listServiceStatuses(writer http.ResponseWriter, _ *http.Request) {
	statuses, err := repo.New(a.db).GetServiceStatuses()
	if err != nil {
		writeRepoError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, statuses)
}

func (a *API) getDashboard(writer http.ResponseWriter, _ *http.Request) {
	services, links, err := repo.New(a.db).GetDashboardData()
	if err != nil {
		writeRepoError(writer, err)
		return
	}
	if services == nil {
		services = []repo.ServiceWithPosition{}
	}
	if links == nil {
		links = []repo.ServiceLink{}
	}
	for i := range services {
		if services[i].Source == "docker" {
			services[i].SourceName = a.docker.SourceName(services[i].Service)
		} else if services[i].Source == "kubernetes" {
			services[i].SourceName = a.kubernetes.SourceName(services[i].Service)
		}
	}
	writeJSON(writer, http.StatusOK, map[string]any{"services": services, "links": links})
}

func (a *API) getService(writer http.ResponseWriter, request *http.Request) {
	service, err := repo.New(a.db).GetService(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusNotFound, "Service not found")
		return
	}
	if service.Source == "docker" {
		service.SourceName = a.docker.SourceName(service)
	} else if service.Source == "kubernetes" {
		service.SourceName = a.kubernetes.SourceName(service)
	}
	writeJSON(writer, http.StatusOK, service)
}

func (a *API) createService(writer http.ResponseWriter, request *http.Request) {
	body, err := decodeBody(request, "name", "host", "protocol", "ports", "checkPort", "source", "metadata", "labels")
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	name, nameSet, nameNull, err := readString(body, "name")
	if err != nil || !nameSet || nameNull || strings.TrimSpace(name) == "" {
		writeError(writer, http.StatusBadRequest, "name: Required")
		return
	}
	host, hostSet, hostNull, err := readString(body, "host")
	if err != nil || !hostSet || hostNull || strings.TrimSpace(host) == "" {
		writeError(writer, http.StatusBadRequest, "host: Required")
		return
	}
	protocol, protocolSet, protocolNull, err := readString(body, "protocol")
	if err != nil || (protocolSet && !protocolNull && !validProtocol(protocol)) {
		writeError(writer, http.StatusBadRequest, "protocol: Invalid enum value")
		return
	}
	ports, portsSet, portsNull, err := readInts(body, "ports")
	if err != nil || (portsSet && (portsNull || parsePorts(ports) != nil)) {
		writeError(writer, http.StatusBadRequest, "ports: Invalid port")
		return
	}
	checkPort, checkPortSet, checkPortNull, err := readInt(body, "checkPort")
	if err != nil || (checkPortSet && (checkPortNull || !validPort(checkPort))) {
		writeError(writer, http.StatusBadRequest, "checkPort: Invalid port")
		return
	}
	source, sourceSet, sourceNull, err := readString(body, "source")
	if err != nil || (sourceSet && (sourceNull || !validSource(source))) {
		writeError(writer, http.StatusBadRequest, "source: Invalid enum value")
		return
	}
	var metadata map[string]any
	if raw, ok := body["metadata"]; ok {
		if string(raw) == "null" || json.Unmarshal(raw, &metadata) != nil || metadata == nil {
			writeError(writer, http.StatusBadRequest, "metadata: Invalid object")
			return
		}
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	labels, labelsSet, labelsNull, err := readStrings(body, "labels")
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	if labelsSet {
		if labelsNull {
			writeError(writer, http.StatusBadRequest, "labels: Invalid array")
			return
		}
		labels, err = parseLabels(labels)
		if err != nil {
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		}
	} else {
		labels = []string{}
	}
	if !portsSet || portsNull {
		ports = []int{}
	}
	if !sourceSet || sourceNull {
		source = "network"
	}
	now := repo.Now()
	service := repo.Service{
		ID: repo.NewID(), Name: strings.TrimSpace(name), Host: strings.TrimSpace(host),
		Ports: ports, CheckPort: nil, Source: source, Status: "unknown",
		Metadata: metadata, Labels: []string{}, CreatedAt: now, UpdatedAt: now,
	}
	if protocolSet && !protocolNull {
		service.Protocol = &protocol
	}
	if checkPortSet && !checkPortNull {
		service.CheckPort = &checkPort
	}
	database := repo.New(a.db)
	if err := database.SaveService(service, labels); err != nil {
		writeRepoError(writer, err)
		return
	}
	saved, err := database.GetService(service.ID)
	if err != nil {
		writeRepoError(writer, err)
		return
	}
	enriched := []repo.Service{saved}
	a.enrichServices(enriched)
	writeJSON(writer, http.StatusCreated, enriched[0])
}

func (a *API) updateService(writer http.ResponseWriter, request *http.Request) {
	body, err := decodeBody(request, "name", "host", "protocol", "ports", "checkPort", "labels")
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	var patch repo.ServicePatch
	if value, set, isNull, err := readString(body, "name"); err != nil || isNull {
		writeError(writer, http.StatusBadRequest, "name: Required")
		return
	} else if set {
		if strings.TrimSpace(value) == "" {
			writeError(writer, http.StatusBadRequest, "name: Required")
			return
		}
		patch.Name = repo.Field[string]{Set: true, Value: strings.TrimSpace(value)}
	}
	if value, set, isNull, err := readString(body, "host"); err != nil || isNull {
		writeError(writer, http.StatusBadRequest, "host: Required")
		return
	} else if set {
		if strings.TrimSpace(value) == "" {
			writeError(writer, http.StatusBadRequest, "host: Required")
			return
		}
		patch.Host = repo.Field[string]{Set: true, Value: strings.TrimSpace(value)}
	}
	if value, set, isNull, err := readString(body, "protocol"); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	} else if set {
		if !isNull && !validProtocol(value) {
			writeError(writer, http.StatusBadRequest, "protocol: Invalid enum value")
			return
		}
		var target *string
		if !isNull {
			target = &value
		}
		patch.Protocol = repo.Field[*string]{Set: true, Value: target}
	}
	if value, set, isNull, err := readInts(body, "ports"); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	} else if set {
		if isNull {
			patch.Ports = repo.Field[[]int]{Set: true, Value: []int{}}
		} else {
			if parsePorts(value) != nil {
				writeError(writer, http.StatusBadRequest, "ports: Invalid port")
				return
			}
			patch.Ports = repo.Field[[]int]{Set: true, Value: value}
		}
	}
	if value, set, isNull, err := readInt(body, "checkPort"); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	} else if set {
		if !isNull && !validPort(value) {
			writeError(writer, http.StatusBadRequest, "checkPort: Invalid port")
			return
		}
		var target *int
		if !isNull {
			target = &value
		}
		patch.CheckPort = repo.Field[*int]{Set: true, Value: target}
	}
	if value, set, isNull, err := readStrings(body, "labels"); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	} else if set {
		if isNull {
			writeError(writer, http.StatusBadRequest, "labels: Invalid array")
			return
		}
		parsed, err := parseLabels(value)
		if err != nil {
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		}
		patch.Labels = repo.Field[[]string]{Set: true, Value: parsed}
	}
	database := repo.New(a.db)
	id := request.PathValue("id")
	if err := database.UpdateService(id, patch); err != nil {
		writeError(writer, http.StatusNotFound, err.Error())
		return
	}
	service, err := database.GetService(id)
	if err != nil {
		writeError(writer, http.StatusNotFound, "Service not found")
		return
	}
	enriched := []repo.Service{service}
	a.enrichServices(enriched)
	writeJSON(writer, http.StatusOK, enriched[0])
}

func (a *API) deleteService(writer http.ResponseWriter, request *http.Request) {
	if err := repo.New(a.db).DeleteService(request.PathValue("id")); err != nil {
		writeRepoError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"success": true})
}

func (a *API) assignLabels(writer http.ResponseWriter, request *http.Request) {
	body, err := decodeBody(request, "serviceIds", "labels")
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	ids, idsSet, idsNull, err := readStrings(body, "serviceIds")
	if err != nil || !idsSet || idsNull || len(ids) == 0 {
		writeError(writer, http.StatusBadRequest, "serviceIds: Required")
		return
	}
	labels, labelsSet, labelsNull, err := readStrings(body, "labels")
	if err != nil || !labelsSet || labelsNull || len(labels) == 0 {
		writeError(writer, http.StatusBadRequest, "labels: At least one label is required")
		return
	}
	parsed, err := parseLabels(labels)
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	services, err := repo.New(a.db).AssignLabels(ids, parsed)
	if err != nil {
		writeRepoError(writer, err)
		return
	}
	a.enrichServices(services)
	writeJSON(writer, http.StatusOK, services)
}

func (a *API) addToDashboard(writer http.ResponseWriter, request *http.Request) {
	database := repo.New(a.db)
	id := request.PathValue("id")
	if _, err := database.GetService(id); err != nil {
		writeError(writer, http.StatusNotFound, "Service not found")
		return
	}
	if err := database.AddToDashboard(id); err != nil {
		writeRepoError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"success": true})
}

func (a *API) removeFromDashboard(writer http.ResponseWriter, request *http.Request) {
	if err := repo.New(a.db).RemoveFromDashboard(request.PathValue("id")); err != nil {
		writeRepoError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"success": true})
}

func (a *API) savePositions(writer http.ResponseWriter, request *http.Request) {
	body, err := decodeBody(request, "positions")
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	raw, ok := body["positions"]
	if !ok || string(raw) == "null" {
		writeError(writer, http.StatusBadRequest, "positions: Required")
		return
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil || items == nil {
		writeError(writer, http.StatusBadRequest, "positions: Invalid array")
		return
	}
	inputs := []repo.PositionInput{}
	for _, item := range items {
		for key := range item {
			switch key {
			case "serviceId", "x", "y", "parentId", "w", "h":
			default:
				writeError(writer, http.StatusBadRequest, key+": Unrecognized key")
				return
			}
		}
		rawBody := rawBody(item)
		id, set, isNull, err := readString(rawBody, "serviceId")
		if err != nil || !set || isNull || strings.TrimSpace(id) == "" {
			writeError(writer, http.StatusBadRequest, "serviceId: Required")
			return
		}
		input := repo.PositionInput{ServiceID: id}
		if value, set, isNull, err := readFloat(rawBody, "x"); err != nil {
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		} else if set {
			if isNull {
				writeError(writer, http.StatusBadRequest, "x: Invalid number")
				return
			}
			input.X = repo.Field[float64]{Set: true, Value: value}
		}
		if value, set, isNull, err := readFloat(rawBody, "y"); err != nil {
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		} else if set {
			if isNull {
				writeError(writer, http.StatusBadRequest, "y: Invalid number")
				return
			}
			input.Y = repo.Field[float64]{Set: true, Value: value}
		}
		if value, set, isNull, err := readString(rawBody, "parentId"); err != nil {
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		} else if set {
			var target *string
			if !isNull {
				target = &value
			}
			input.ParentID = repo.Field[*string]{Set: true, Value: target}
		}
		if value, set, isNull, err := readFloat(rawBody, "w"); err != nil {
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		} else if set {
			var target *float64
			if !isNull {
				target = &value
			}
			input.W = repo.Field[*float64]{Set: true, Value: target}
		}
		if value, set, isNull, err := readFloat(rawBody, "h"); err != nil {
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		} else if set {
			var target *float64
			if !isNull {
				target = &value
			}
			input.H = repo.Field[*float64]{Set: true, Value: target}
		}
		inputs = append(inputs, input)
	}
	database := repo.New(a.db)
	if err := database.SavePositions(inputs); err != nil {
		if err.Error() == "Parent must be on the dashboard" || err.Error() == "Dashboard hierarchy cannot contain cycles" {
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		}
		writeRepoError(writer, err)
		return
	}
	positions, err := database.GetPositions()
	if err != nil {
		writeRepoError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"positions": positions})
}

func (a *API) createLink(writer http.ResponseWriter, request *http.Request) {
	body, err := decodeBody(request, "sourceId", "targetId", "type", "label", "description", "targetPort", "protocol")
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	sourceID, sourceSet, sourceNull, err := readString(body, "sourceId")
	if err != nil || !sourceSet || sourceNull || strings.TrimSpace(sourceID) == "" {
		writeError(writer, http.StatusBadRequest, "sourceId: Required")
		return
	}
	targetID, targetSet, targetNull, err := readString(body, "targetId")
	if err != nil || !targetSet || targetNull || strings.TrimSpace(targetID) == "" {
		writeError(writer, http.StatusBadRequest, "targetId: Required")
		return
	}
	if sourceID == targetID {
		writeError(writer, http.StatusBadRequest, "targetId: source and target cannot be the same")
		return
	}
	linkType := "communication"
	if value, set, isNull, err := readString(body, "type"); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	} else if set {
		if isNull || !validLinkType(value) {
			writeError(writer, http.StatusBadRequest, "type: Invalid enum value")
			return
		}
		linkType = value
	}
	label := ""
	if value, set, isNull, err := readString(body, "label"); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	} else if set {
		if isNull {
			writeError(writer, http.StatusBadRequest, "label: Invalid string")
			return
		}
		label = value
	}
	description := ""
	if value, set, isNull, err := readString(body, "description"); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	} else if set {
		if isNull {
			writeError(writer, http.StatusBadRequest, "description: Invalid string")
			return
		}
		description = value
	}
	var targetPort *int
	if value, set, isNull, err := readInt(body, "targetPort"); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	} else if set {
		if isNull || !validPort(value) {
			writeError(writer, http.StatusBadRequest, "targetPort: Invalid port")
			return
		}
		targetPort = &value
	}
	var protocol *string
	if value, set, isNull, err := readString(body, "protocol"); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	} else if set {
		if isNull || !validProtocol(value) {
			writeError(writer, http.StatusBadRequest, "protocol: Invalid enum value")
			return
		}
		protocol = &value
	}
	link := repo.ServiceLink{
		ID: repo.NewID(), SourceID: sourceID, TargetID: targetID, Type: linkType,
		Label: &label, Description: &description, TargetPort: targetPort, Protocol: protocol,
	}
	if err := repo.New(a.db).SaveLink(link); err != nil {
		writeError(writer, http.StatusConflict, err.Error())
		return
	}
	writeJSON(writer, http.StatusCreated, link)
}

func (a *API) updateLink(writer http.ResponseWriter, request *http.Request) {
	body, err := decodeBody(request, "type", "label", "description", "targetPort", "protocol")
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	var patch repo.LinkPatch
	if value, set, isNull, err := readString(body, "type"); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	} else if set {
		if isNull || !validLinkType(value) {
			writeError(writer, http.StatusBadRequest, "type: Invalid enum value")
			return
		}
		patch.Type = repo.Field[string]{Set: true, Value: value}
	}
	if value, set, isNull, err := readString(body, "label"); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	} else if set {
		var target *string
		if !isNull {
			target = &value
		}
		patch.Label = repo.Field[*string]{Set: true, Value: target}
	}
	if value, set, isNull, err := readString(body, "description"); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	} else if set {
		var target *string
		if !isNull {
			target = &value
		}
		patch.Description = repo.Field[*string]{Set: true, Value: target}
	}
	if value, set, isNull, err := readInt(body, "targetPort"); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	} else if set {
		var target *int
		if !isNull {
			if !validPort(value) {
				writeError(writer, http.StatusBadRequest, "targetPort: Invalid port")
				return
			}
			target = &value
		}
		patch.TargetPort = repo.Field[*int]{Set: true, Value: target}
	}
	if value, set, isNull, err := readString(body, "protocol"); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	} else if set {
		var target *string
		if !isNull {
			if !validProtocol(value) {
				writeError(writer, http.StatusBadRequest, "protocol: Invalid enum value")
				return
			}
			target = &value
		}
		patch.Protocol = repo.Field[*string]{Set: true, Value: target}
	}
	database := repo.New(a.db)
	id := request.PathValue("id")
	if err := database.UpdateLink(id, patch); err != nil {
		writeError(writer, http.StatusNotFound, err.Error())
		return
	}
	link, err := database.GetLink(id)
	if err != nil {
		writeError(writer, http.StatusNotFound, "Link not found")
		return
	}
	writeJSON(writer, http.StatusOK, link)
}

func (a *API) deleteLink(writer http.ResponseWriter, request *http.Request) {
	if err := repo.New(a.db).DeleteLink(request.PathValue("id")); err != nil {
		writeRepoError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"success": true})
}

func intFromQuery(value string, fallback, min, max int) int {
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	if parsed < min {
		return min
	}
	if parsed > max {
		return max
	}
	return parsed
}
