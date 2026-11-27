package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"dockdash/internal/repo"
)

func (a *API) registerHealthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/checkAllServices", a.checkAllServices)
	mux.HandleFunc("GET /api/services/{id}/health-history", a.healthHistory)
	mux.HandleFunc("GET /api/services/{id}/resource-history", a.resourceHistory)
}

func (a *API) checkAllServices(writer http.ResponseWriter, _ *http.Request) {
	go a.healthJob.Run(context.Background())
	writeJSON(writer, http.StatusOK, map[string]string{
		"status":  "running",
		"message": "Health check started in background",
	})
}

func (a *API) historyService(writer http.ResponseWriter, request *http.Request) bool {
	_, err := repo.New(a.db).GetService(request.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(writer, http.StatusNotFound, "Service not found")
		return false
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, err.Error())
		return false
	}
	return true
}

func (a *API) healthHistory(writer http.ResponseWriter, request *http.Request) {
	if !a.cfg.HealthHistoryEnabled {
		writeError(writer, http.StatusForbidden, "Health history is disabled")
		return
	}
	if !a.historyService(writer, request) {
		return
	}
	days := intFromQuery(request.URL.Query().Get("days"), 7, 1, 3650)
	buckets := intFromQuery(request.URL.Query().Get("buckets"), 80, 1, 200)
	writeJSON(writer, http.StatusOK, repo.New(a.db).HealthHistory(request.PathValue("id"), days, buckets))
}

func (a *API) resourceHistory(writer http.ResponseWriter, request *http.Request) {
	if !a.cfg.ResourceMonitorEnabled {
		writeError(writer, http.StatusForbidden, "Resource monitoring is disabled")
		return
	}
	if !a.historyService(writer, request) {
		return
	}
	days := intFromQuery(request.URL.Query().Get("days"), 7, 1, 3650)
	buckets := intFromQuery(request.URL.Query().Get("buckets"), 80, 1, 200)
	writeJSON(writer, http.StatusOK, repo.New(a.db).ResourceHistory(request.PathValue("id"), days, buckets))
}
