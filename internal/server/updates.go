package server

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"dockdash/internal/notify"
	"dockdash/internal/repo"
	"dockdash/internal/updates"
)

func (a *API) registerUpdateRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/notifications/test", a.testNotificationHandler)
	mux.HandleFunc("GET /api/app-update", a.appUpdateHandler)
	mux.HandleFunc("GET /api/services/{id}/changelog", a.changelogHandler)
}

func (a *API) testNotificationHandler(writer http.ResponseWriter, request *http.Request) {
	if !a.notifier.Configured() {
		writeError(writer, http.StatusBadRequest, "Apprise not configured")
		return
	}
	title := notify.Message(a.cfg.Locale, "testTitle", nil)
	body := notify.Message(a.cfg.Locale, "testBody", nil)
	if err := a.notifier.Notify(request.Context(), title, body, notify.TypeInfo); err != nil {
		writeError(writer, http.StatusBadGateway, "Apprise notification failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"success": true})
}

func (a *API) appUpdateHandler(writer http.ResponseWriter, request *http.Request) {
	interval := time.Duration(a.cfg.UpdateCheckInterval) * time.Millisecond
	result := a.github.AppUpdate(request.Context(), a.cfg.AppRepo, a.cfg.AppVersion, interval)
	if result == nil {
		result = &updates.AppUpdate{HasUpdate: false}
	}
	writeJSON(writer, http.StatusOK, result)
}

func (a *API) changelogHandler(writer http.ResponseWriter, request *http.Request) {
	service, err := repo.New(a.db).GetService(request.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(writer, http.StatusNotFound, "Service not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, err.Error())
		return
	}
	image, _ := service.Metadata["image"].(string)
	imageTag, _ := service.Metadata["imageTag"].(string)
	hasUpdate, _ := service.Metadata["hasUpdate"].(bool)
	targetTag := imageTag
	if hasUpdate {
		if latest, ok := service.Metadata["latestVersion"].(string); ok && latest != "" {
			targetTag = latest
		}
	}
	if targetTag == "" {
		writeJSON(writer, http.StatusOK, updates.Changelog{Available: false, Reason: "No image tag available"})
		return
	}
	repository := updates.ResolveRepository(image)
	if repository == "" {
		writeJSON(writer, http.StatusOK, updates.Changelog{
			Available: false, Reason: "Could not resolve GitHub repository",
		})
		return
	}
	result := a.github.FetchChangelog(request.Context(), repository, imageTag, targetTag)
	writeJSON(writer, http.StatusOK, result)
}
