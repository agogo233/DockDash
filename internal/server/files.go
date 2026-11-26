package server

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"dockdash/internal/docker"
	"dockdash/internal/repo"
)

const maxLogBufferBytes = 1024 * 1024

func validContainerPath(path string) bool {
	return strings.HasPrefix(path, "/") && !strings.Contains(path, "\x00") && len(path) <= 4096
}

func (a *API) registerFileRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/services/{id}/logs/stream", a.logsStream)
	mux.HandleFunc("GET /api/services/{id}/files", a.listFiles)
	mux.HandleFunc("GET /api/services/{id}/files/content", a.readFile)
	mux.HandleFunc("PUT /api/services/{id}/files/content", a.writeFile)
}

func (a *API) logsStream(writer http.ResponseWriter, request *http.Request) {
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

	sendError := func(message string) {
		payload, _ := json.Marshal(map[string]string{"message": message})
		sseWrite(writer, flusher, "log-error", string(payload))
	}
	service, err := repo.New(a.db).GetService(request.PathValue("id"))
	if err != nil {
		sendError("Service not found")
		return
	}
	stream, tty, err := a.docker.Logs(request.Context(), service)
	if err != nil {
		sendError(err.Error())
		return
	}
	defer stream.Close()

	buffer := []byte{}
	emitted := 0
	windowStart := time.Now()
	chunk := make([]byte, 32*1024)
	for {
		read, readErr := stream.Read(chunk)
		if read > 0 {
			buffer = append(buffer, chunk[:read]...)
			if len(buffer) > maxLogBufferBytes {
				sendError("Log output buffer limit exceeded")
				return
			}
			lines, err := demuxLogLines(buffer, tty)
			if err != nil {
				sendError(err.Error())
				return
			}
			buffer = lines.remaining
			if time.Since(windowStart) > time.Minute {
				emitted = 0
				windowStart = time.Now()
			}
			for _, line := range lines.values {
				line = strings.TrimSuffix(line, "\r")
				emitted += len(line)
				if emitted > maxLogBufferBytes {
					sendError("Log output buffer limit exceeded")
					return
				}
				if request.Context().Err() != nil {
					return
				}
				sseWrite(writer, flusher, "", line)
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) && request.Context().Err() == nil {
				sendError(readErr.Error())
			}
			return
		}
		if request.Context().Err() != nil {
			return
		}
	}
}

type logLines struct {
	remaining []byte
	values    []string
}

func demuxLogLines(data []byte, tty bool) (logLines, error) {
	result := logLines{values: []string{}}
	if tty {
		parts := strings.Split(string(data), "\n")
		result.remaining = []byte(parts[len(parts)-1])
		for _, part := range parts[:len(parts)-1] {
			if part != "" {
				result.values = append(result.values, part)
			}
		}
		return result, nil
	}
	for len(data) >= 8 {
		size := int(binary.BigEndian.Uint32(data[4:8]))
		if size > maxLogBufferBytes {
			return logLines{}, errors.New("Log frame size limit exceeded")
		}
		if len(data) < 8+size {
			break
		}
		if data[0] == 1 || data[0] == 2 {
			for _, part := range strings.Split(string(data[8:8+size]), "\n") {
				if part != "" {
					result.values = append(result.values, part)
				}
			}
		}
		data = data[8+size:]
	}
	result.remaining = data
	return result, nil
}

func (a *API) listFiles(writer http.ResponseWriter, request *http.Request) {
	if !a.cfg.FileExplorerEnabled {
		writeError(writer, http.StatusForbidden, "File explorer is disabled")
		return
	}
	path := request.URL.Query().Get("path")
	if path == "" {
		path = "/"
	}
	if !validContainerPath(path) {
		writeError(writer, http.StatusBadRequest, "Invalid path")
		return
	}
	service, err := repo.New(a.db).GetService(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "Service not found")
		return
	}
	entries, err := a.docker.ListFiles(request.Context(), service, path)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"path": path, "entries": entries})
}

func (a *API) readFile(writer http.ResponseWriter, request *http.Request) {
	if !a.cfg.FileExplorerEnabled {
		writeError(writer, http.StatusForbidden, "File explorer is disabled")
		return
	}
	path := request.URL.Query().Get("path")
	if !validContainerPath(path) {
		writeError(writer, http.StatusBadRequest, "Invalid path")
		return
	}
	service, err := repo.New(a.db).GetService(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "Service not found")
		return
	}
	content, err := a.docker.ReadFile(request.Context(), service, path, 8*1024*1024)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			writeError(writer, http.StatusGatewayTimeout, "File read timed out")
			return
		}
		var readError *docker.FileReadError
		if errors.As(err, &readError) {
			writeError(writer, readError.Status, readError.Message)
			return
		}
		writeError(writer, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, content)
}

func (a *API) writeFile(writer http.ResponseWriter, request *http.Request) {
	if !a.cfg.FileExplorerEnabled {
		writeError(writer, http.StatusForbidden, "File explorer is disabled")
		return
	}
	body, err := decodeBody(request, "path", "content")
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	path, pathSet, pathNull, err := readString(body, "path")
	if err != nil || !pathSet || pathNull || !validContainerPath(path) {
		writeError(writer, http.StatusBadRequest, "Invalid path")
		return
	}
	content, contentSet, contentNull, err := readString(body, "content")
	if err != nil || !contentSet || contentNull {
		writeError(writer, http.StatusBadRequest, "Invalid request")
		return
	}
	service, err := repo.New(a.db).GetService(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "Service not found")
		return
	}
	if err := a.docker.WriteFile(request.Context(), service, path, content); err != nil {
		writeError(writer, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"success": true})
}
