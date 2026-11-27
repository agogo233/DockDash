package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"dockdash/internal/discovery"
	"dockdash/internal/repo"
)

func (a *API) registerDiscoveryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/kubernetes/health", a.kubernetesHealth)
	mux.HandleFunc("GET /api/kubernetes/scan/stream", a.kubernetesScanStream)
	mux.HandleFunc("GET /api/network/scan/stream", a.networkScanStream)
}

func (a *API) kubernetesHealth(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, a.kubernetes.Health(request.Context()))
}

func (a *API) kubernetesScanStream(writer http.ResponseWriter, request *http.Request) {
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

	count := 0
	err := a.kubernetes.Scan(request.Context(), func(service repo.Service) {
		if request.Context().Err() != nil {
			return
		}
		service.SourceName = a.kubernetes.SourceName(service)
		payload, marshalErr := json.Marshal(service)
		if marshalErr != nil {
			return
		}
		sseWrite(writer, flusher, "", string(payload))
		count++
	})
	if err != nil && request.Context().Err() == nil {
		payload, _ := json.Marshal(map[string]string{"message": err.Error()})
		sseWrite(writer, flusher, "scan-error", string(payload))
	}
	if request.Context().Err() == nil {
		sseDone, _ := json.Marshal(map[string]int{"count": count})
		sseWrite(writer, flusher, "done", string(sseDone))
	}
}

func (a *API) networkScanStream(writer http.ResponseWriter, request *http.Request) {
	cidrs := []string{}
	if raw := request.URL.Query().Get("cidrs"); raw != "" {
		for _, cidr := range strings.Split(raw, ",") {
			if trimmed := strings.TrimSpace(cidr); trimmed != "" {
				cidrs = append(cidrs, trimmed)
			}
		}
	} else {
		cidrs = append(cidrs, a.cfg.NetworkCIDRs...)
	}
	if len(cidrs) == 0 {
		writeError(writer, http.StatusBadRequest, "No CIDRs configured")
		return
	}
	for _, cidr := range cidrs {
		if err := discovery.ValidateCIDR(cidr); err != nil {
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		}
	}

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

	count := 0
	err := a.network.Scan(request.Context(), cidrs, request.URL.Query().Get("deepScan") == "true", func(service repo.Service) {
		if request.Context().Err() != nil {
			return
		}
		payload, marshalErr := json.Marshal(service)
		if marshalErr != nil {
			return
		}
		sseWrite(writer, flusher, "", string(payload))
		count++
	})
	if err != nil && request.Context().Err() == nil {
		payload, _ := json.Marshal(map[string]string{"message": err.Error()})
		sseWrite(writer, flusher, "scan-error", string(payload))
	}
	if request.Context().Err() == nil {
		sseDone, _ := json.Marshal(map[string]int{"count": count})
		sseWrite(writer, flusher, "done", string(sseDone))
	}
}
