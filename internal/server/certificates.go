package server

import (
	"net/http"

	"dockdash/internal/certmonitor"
	"dockdash/internal/repo"
)

func (a *API) registerCertificateRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/tls-certificates", a.tlsCertificates)
	mux.HandleFunc("GET /api/services/{id}/tls-certificate", a.serviceTLSCertificate)
}

func (a *API) tlsCertificates(writer http.ResponseWriter, request *http.Request) {
	refresh := request.URL.Query().Get("refresh") == "true"
	certificates := a.certService.GetAll(request.Context(), refresh)
	a.enrichCertificates(request, certificates)
	writeJSON(writer, http.StatusOK, certificates)
}

func (a *API) serviceTLSCertificate(writer http.ResponseWriter, request *http.Request) {
	refresh := request.URL.Query().Get("refresh") == "true"
	certificate, ok := a.certService.GetForService(request.PathValue("id"), refresh)
	if !ok {
		writeError(writer, http.StatusNotFound, "Service has no HTTPS endpoint")
		return
	}
	list := []certmonitor.Certificate{certificate}
	a.enrichCertificates(request, list)
	writeJSON(writer, http.StatusOK, list[0])
}

func (a *API) enrichCertificates(request *http.Request, certificates []certmonitor.Certificate) {
	if len(certificates) == 0 {
		return
	}
	services, err := repo.New(a.db).GetServices()
	if err != nil {
		return
	}
	byID := map[string]repo.Service{}
	for _, service := range services {
		byID[service.ID] = service
	}
	statuses, _ := a.certVault.Statuses(request.Context(), certificates, byID)
	for index := range certificates {
		if status, ok := statuses[certificates[index].ServiceID]; ok {
			status := status
			certificates[index].CertVaultStatus = &status
		}
	}
}
