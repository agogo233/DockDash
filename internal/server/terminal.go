package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"dockdash/internal/repo"
)

func safeErrorMessage(err error) string {
	message := strings.Join(strings.Fields(err.Error()), " ")
	if len(message) > 500 {
		message = message[:500]
	}
	return message
}

func (a *API) registerTerminalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/services/{id}/terminal/stream", a.terminalStream)
	mux.HandleFunc("POST /api/services/{id}/terminal/input", a.terminalInput)
}

func (a *API) terminalStream(writer http.ResponseWriter, request *http.Request) {
	if !a.cfg.TerminalEnabled {
		writeError(writer, http.StatusForbidden, "Terminal is disabled")
		return
	}
	cols := intFromQuery(request.URL.Query().Get("cols"), 80, 1, 1000)
	rows := intFromQuery(request.URL.Query().Get("rows"), 24, 1, 1000)
	service, err := repo.New(a.db).GetService(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "Unable to initialize terminal session")
		return
	}
	sess := sessionFrom(request)
	if sess == nil {
		sess = a.store.Blank()
	}
	if sess.Values["terminalInitialized"] != true {
		sess.Values["terminalInitialized"] = true
		if err := a.store.Save(writer, sess); err != nil {
			writeError(writer, http.StatusInternalServerError, "Unable to initialize terminal session")
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

	session, err := a.docker.OpenTerminal(request.Context(), sess.ID, service, cols, rows)
	if err != nil {
		payload, _ := json.Marshal(map[string]string{"message": safeErrorMessage(err)})
		sseWrite(writer, flusher, "terminal-error", string(payload))
		return
	}
	defer a.docker.CloseTerminal(session.ID)
	go func() {
		<-request.Context().Done()
		a.docker.CloseTerminal(session.ID)
	}()

	payload, _ := json.Marshal(map[string]string{"sessionId": session.ID})
	sseWrite(writer, flusher, "terminal-session", string(payload))

	buffer := make([]byte, 4096)
	for {
		read, readErr := session.Reader.Read(buffer)
		if read > 0 {
			a.docker.TouchTerminal(session.ID)
			if request.Context().Err() != nil {
				return
			}
			encoded, _ := json.Marshal(base64.StdEncoding.EncodeToString(buffer[:read]))
			sseWrite(writer, flusher, "", string(encoded))
		}
		if readErr != nil {
			if request.Context().Err() != nil {
				return
			}
			if errors.Is(readErr, io.EOF) {
				sseWrite(writer, flusher, "done", "{}")
			} else {
				message, _ := json.Marshal(map[string]string{"message": safeErrorMessage(readErr)})
				sseWrite(writer, flusher, "terminal-error", string(message))
			}
			return
		}
		if request.Context().Err() != nil {
			return
		}
	}
}

func (a *API) terminalInput(writer http.ResponseWriter, request *http.Request) {
	if !a.cfg.TerminalEnabled {
		writeError(writer, http.StatusForbidden, "Terminal is disabled")
		return
	}
	body, err := decodeBody(request, "sessionId", "data")
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	id, idSet, idNull, err := readString(body, "sessionId")
	if err != nil || !idSet || idNull || strings.TrimSpace(id) == "" {
		writeError(writer, http.StatusBadRequest, "Invalid request")
		return
	}
	data, dataSet, dataNull, err := readString(body, "data")
	if err != nil || !dataSet || dataNull {
		writeError(writer, http.StatusBadRequest, "Invalid request")
		return
	}
	sess := sessionFrom(request)
	if sess == nil {
		writeError(writer, http.StatusNotFound, "Session not found")
		return
	}
	terminal, ok := a.docker.GetTerminal(sess.ID, id)
	if !ok {
		writeError(writer, http.StatusNotFound, "Session not found")
		return
	}
	a.docker.TouchTerminal(id)
	if err := terminal.Write(data); err != nil {
		writeError(writer, http.StatusGone, "Session stream is closed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"success": true})
}
