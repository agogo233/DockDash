package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"dockdash/internal/repo"
)

func (a *API) enrichServices(services []repo.Service) {
	for i := range services {
		if services[i].Source == "docker" {
			services[i].SourceName = a.docker.SourceName(services[i])
		} else if services[i].Source == "kubernetes" {
			services[i].SourceName = a.kubernetes.SourceName(services[i])
		}
	}
}

func sseWrite(writer http.ResponseWriter, flusher http.Flusher, event, data string) {
	if event != "" {
		_, _ = fmt.Fprintf(writer, "event: %s\n", event)
	}
	_, _ = fmt.Fprintf(writer, "data: %s\n\n", data)
	flusher.Flush()
}

func (a *API) registerContainerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/docker/health", a.dockerHealth)
	mux.HandleFunc("GET /api/docker/scan/stream", a.dockerScanStream)
	mux.HandleFunc("POST /api/services/{id}/container/{action}", a.containerAction)
	mux.HandleFunc("GET /api/services/{id}/stats", a.containerStats)
}

func (a *API) dockerHealth(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, a.docker.HostHealth(request.Context()))
}

func (a *API) dockerScanStream(writer http.ResponseWriter, request *http.Request) {
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeError(writer, http.StatusInternalServerError, "Streaming not supported")
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	writer.WriteHeader(http.StatusOK)
	flusher.Flush()

	services, err := a.docker.Scan(request.Context())
	if err != nil {
		payload, _ := json.Marshal(map[string]string{"message": err.Error()})
		sseWrite(writer, flusher, "scan-error", string(payload))
		sseDone, _ := json.Marshal(map[string]int{"count": 0})
		sseWrite(writer, flusher, "done", string(sseDone))
		return
	}
	a.enrichServices(services)
	count := 0
	for _, service := range services {
		if request.Context().Err() != nil {
			return
		}
		payload, err := json.Marshal(service)
		if err != nil {
			continue
		}
		sseWrite(writer, flusher, "", string(payload))
		count++
	}
	sseDone, _ := json.Marshal(map[string]int{"count": count})
	sseWrite(writer, flusher, "done", string(sseDone))
}

func (a *API) containerAction(writer http.ResponseWriter, request *http.Request) {
	if !a.cfg.ContainerControlsEnabled {
		writeError(writer, http.StatusForbidden, "Container controls are disabled")
		return
	}
	action := request.PathValue("action")
	if action != "start" && action != "stop" && action != "restart" {
		writeError(writer, http.StatusBadRequest, "Invalid action")
		return
	}
	service, err := repo.New(a.db).GetService(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "Service not found")
		return
	}
	if err := a.docker.Action(request.Context(), service, action); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"success": true})
}

func (a *API) containerStats(writer http.ResponseWriter, request *http.Request) {
	if !a.cfg.ResourceMonitorEnabled {
		writeError(writer, http.StatusForbidden, "Resource monitor is disabled")
		return
	}
	service, err := repo.New(a.db).GetService(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "Service not found")
		return
	}
	stats, err := a.docker.Stats(request.Context(), service)
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, stats)
}
